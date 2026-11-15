// Package dispatch runs mirror operations in the background: one clone per
// event, pushes to all targets in parallel, at most one run per repository
// at a time, and a global limit on concurrent git processes. Targets with a
// forge type additionally get their repository created and their settings
// and labels synchronised through the forge API.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// Observer is notified about mirror activity (metrics). All methods must be
// safe for concurrent use.
type Observer interface {
	// RunStarted and RunFinished bracket one mirror run of one repository.
	RunStarted(ctx context.Context)
	RunFinished(ctx context.Context)
	// TargetFinished reports the outcome for one target; err is nil on success.
	TargetFinished(ctx context.Context, target string, duration time.Duration, err error)
	// SyncFinished reports one forge API operation ("create", "settings",
	// "labels" or "delete") on a typed target; err is nil on success.
	SyncFinished(ctx context.Context, target, operation string, err error)
}

// Options configures a [Dispatcher].
type Options struct {
	Mirrorer mirror.Mirrorer
	// Source holds the credentials for cloning from Forgejo.
	Source  config.Source
	Targets []config.Target
	// ForgeSource reads repository settings and labels from the Forgejo API.
	// It is nil when no target has a type. It cannot be called Source, which
	// already names the clone credentials.
	ForgeSource forge.Source
	// Forges holds the API client of every typed target, keyed by target name.
	Forges map[string]forge.Target
	// MaxConcurrent caps git processes (clones and pushes) across all runs.
	MaxConcurrent int
	Logger        *slog.Logger
	// Observer is optional.
	Observer Observer
}

// Dispatcher implements [webhook.Dispatcher].
type Dispatcher struct {
	opts Options

	// sem bounds concurrent git processes; a buffered channel is the idiomatic
	// counting semaphore and, unlike a mutex, can be acquired with a timeout.
	sem chan struct{}

	// base outlives individual requests; cancelling it aborts all running git
	// processes when the graceful shutdown runs out of time.
	base   context.Context
	cancel context.CancelFunc

	// mu guards running and closed. Handler goroutines call Dispatch
	// concurrently, and runs remove themselves when done: without the mutex
	// two pushes to one repository could both start a run (data race on the
	// map, duplicate work).
	mu      sync.Mutex
	running map[string]*repoState
	closed  bool

	wg sync.WaitGroup
}

// repoState tracks the single active run of a repository.
type repoState struct {
	// next is the newest event that arrived during the active run; nil if
	// none. Only the newest matters, since a run mirrors the full current
	// state of the repository.
	next *queuedEvent
}

type queuedEvent struct {
	// ctx carries the event's values (not its cancellation) until its run starts.
	ctx   context.Context
	event webhook.Event
}

var _ webhook.Dispatcher = (*Dispatcher)(nil)

// New returns a [Dispatcher].
func New(opts Options) *Dispatcher {
	if opts.Observer == nil {
		opts.Observer = noopObserver{}
	}
	base, cancel := context.WithCancel(context.Background())

	return &Dispatcher{
		opts:    opts,
		sem:     make(chan struct{}, opts.MaxConcurrent),
		base:    base,
		cancel:  cancel,
		running: make(map[string]*repoState),
	}
}

// Dispatch implements [webhook.Dispatcher]. It never blocks on git.
func (d *Dispatcher) Dispatch(ctx context.Context, e webhook.Event) {
	key := e.Repository.FullName
	logger := d.eventLogger(e)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		logger.WarnContext(ctx, "shutting down; event dropped")

		return
	}

	if state, busy := d.running[key]; busy {
		// The repository is gone once a deletion is queued; letting a later
		// push replace it would recreate the target repository.
		if state.next != nil && state.next.event.Kind == webhook.EventDelete && e.Kind != webhook.EventDelete {
			logger.InfoContext(ctx, "deletion already queued; event dropped")

			return
		}
		state.next = &queuedEvent{ctx: ctx, event: e}
		logger.InfoContext(ctx, "mirror already running; queued one follow-up run")

		return
	}

	d.running[key] = &repoState{}
	d.wg.Go(func() { d.runLoop(ctx, e) })
}

// Shutdown stops accepting events and waits for running mirrors. If ctx ends
// first, running git processes are killed and ctx's error is returned.
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.cancel()

		return nil
	case <-ctx.Done():
		d.cancel()
		<-done // git processes exit promptly once killed

		return fmt.Errorf("waiting for running mirrors: %w", ctx.Err())
	}
}

// runLoop mirrors e, then any follow-up event queued meanwhile.
func (d *Dispatcher) runLoop(ctx context.Context, e webhook.Event) {
	for current := (&queuedEvent{ctx: ctx, event: e}); current != nil; current = d.nextRun(current.event) {
		d.run(current.ctx, current.event)
	}
}

// nextRun returns the follow-up event queued during the run of e, or nil
// after deregistering the repository when there is none.
func (d *Dispatcher) nextRun(e webhook.Event) *queuedEvent {
	d.mu.Lock()
	defer d.mu.Unlock()

	state := d.running[e.Repository.FullName]
	next := state.next
	state.next = nil

	if next != nil && !d.closed {
		return next
	}

	delete(d.running, e.Repository.FullName)
	if next != nil {
		d.eventLogger(next.event).WarnContext(next.ctx, "shutting down; queued follow-up run dropped")
	}

	return nil
}

// run performs one run of e: a deletion, or a mirror run.
func (d *Dispatcher) run(eventCtx context.Context, e webhook.Event) {
	// The run keeps the event's values but is cancelled by the dispatcher's
	// lifetime, not by the (already finished) HTTP request.
	ctx, cancel := context.WithCancel(eventCtx)
	defer cancel()
	stop := context.AfterFunc(d.base, cancel)
	defer stop()

	d.opts.Observer.RunStarted(ctx)
	defer d.opts.Observer.RunFinished(ctx)

	logger := d.eventLogger(e)
	if e.Kind == webhook.EventDelete {
		d.deleteRepos(ctx, logger, e)

		return
	}

	start := time.Now()
	logger.InfoContext(ctx, "mirror started")

	runs := d.targetRuns()
	d.prepare(ctx, logger, runs, e, start)
	d.mirror(ctx, logger, runs, e, start)
	parallel(runs, func(tr *targetRun) {
		if tr.typed() && tr.err == nil {
			d.syncTarget(ctx, logger, tr)
		}
	})

	if err := runErrors(runs); err != nil {
		logger.ErrorContext(ctx, "mirror finished with errors",
			slog.Duration("duration", time.Since(start)), slog.Any("error", err))

		return
	}
	logger.InfoContext(ctx, "mirror finished", slog.Duration("duration", time.Since(start)))
}

// mirror clones once and pushes to every target that is still in the run.
func (d *Dispatcher) mirror(
	ctx context.Context,
	logger *slog.Logger,
	runs []*targetRun,
	e webhook.Event,
	start time.Time,
) {
	pending := pendingRuns(runs)
	if len(pending) == 0 {
		return
	}

	if e.Kind == webhook.EventScheduled && d.inSync(ctx, logger, pending, e) {
		logger.InfoContext(ctx, "already in sync; clone and push skipped")

		return
	}

	ws, err := d.clone(ctx, e)
	if err != nil {
		for _, tr := range pending {
			d.fail(ctx, logger, tr, start, err)
		}

		return
	}
	defer func() {
		if closeErr := ws.Close(); closeErr != nil {
			logger.WarnContext(ctx, "cleaning up clone failed", slog.Any("error", closeErr))
		}
	}()

	parallel(pending, func(tr *targetRun) {
		pushStart := time.Now()
		tr.err = d.push(ctx, ws, tr.cfg, e)
		d.finishTarget(ctx, logger, tr.cfg.Name, pushStart, tr.err)
	})
}

// inSync reports whether every target already has exactly the source refs.
// Any failure to list refs means "not known to be in sync", so the run falls
// back to a full mirror instead of silently skipping it.
func (d *Dispatcher) inSync(ctx context.Context, logger *slog.Logger, runs []*targetRun, e webhook.Event) bool {
	want, err := d.refs(ctx, d.sourceRemote(e))
	if err != nil {
		logger.WarnContext(ctx, "listing source refs failed; mirroring anyway", slog.Any("error", err))

		return false
	}

	for _, tr := range runs {
		remote, remoteErr := targetRemote(tr.cfg, e)
		if remoteErr != nil {
			return false
		}
		have, refsErr := d.refs(ctx, remote)
		if refsErr != nil {
			logger.WarnContext(ctx, "listing target refs failed; mirroring anyway",
				slog.String("target", tr.cfg.Name), slog.Any("error", refsErr))

			return false
		}
		if !maps.Equal(want, have) {
			return false
		}
	}

	return true
}

func (d *Dispatcher) refs(ctx context.Context, r mirror.Remote) (map[string]string, error) {
	release, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	return d.opts.Mirrorer.Refs(ctx, r)
}

func (d *Dispatcher) sourceRemote(e webhook.Event) mirror.Remote {
	return mirror.Remote{
		URL:      e.Repository.CloneURL,
		Username: d.opts.Source.Username,
		Token:    d.opts.Source.Token,
	}
}

func targetRemote(t *config.Target, e webhook.Event) (mirror.Remote, error) {
	url, err := t.RemoteURL(e.Repository)
	if err != nil {
		return mirror.Remote{}, err
	}

	return mirror.Remote{URL: url, Username: t.Username, Token: t.Token}, nil
}

func (d *Dispatcher) clone(ctx context.Context, e webhook.Event) (mirror.Workspace, error) {
	release, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	return d.opts.Mirrorer.Clone(ctx, d.sourceRemote(e))
}

func (d *Dispatcher) push(ctx context.Context, ws mirror.Workspace, t *config.Target, e webhook.Event) error {
	remote, err := targetRemote(t, e)
	if err != nil {
		return err
	}

	release, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	return ws.Push(ctx, remote)
}

func (d *Dispatcher) acquire(ctx context.Context) (func(), error) {
	select {
	case d.sem <- struct{}{}:
		return func() { <-d.sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free git slot: %w", ctx.Err())
	}
}

func (d *Dispatcher) finishTarget(ctx context.Context, logger *slog.Logger, target string, start time.Time, err error) {
	duration := time.Since(start)
	d.opts.Observer.TargetFinished(ctx, target, duration, err)

	logger = logger.With(slog.String("target", target), slog.Duration("duration", duration))
	if err != nil {
		logger.ErrorContext(ctx, "target failed", slog.String("outcome", "failure"), slog.Any("error", err))

		return
	}
	logger.InfoContext(ctx, "target mirrored", slog.String("outcome", "success"))
}

func (d *Dispatcher) eventLogger(e webhook.Event) *slog.Logger {
	return d.opts.Logger.With(
		slog.String("repository", e.Repository.FullName),
		slog.String("ref", e.Ref),
		slog.String("delivery", e.Delivery),
	)
}

type noopObserver struct{}

func (noopObserver) RunStarted(context.Context)                                   {}
func (noopObserver) RunFinished(context.Context)                                  {}
func (noopObserver) TargetFinished(context.Context, string, time.Duration, error) {}
func (noopObserver) SyncFinished(context.Context, string, string, error)          {}
