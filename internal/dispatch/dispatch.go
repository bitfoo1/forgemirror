// Package dispatch runs mirror operations in the background: one clone per
// event, pushes to all targets in parallel, at most one run per repository
// at a time, and a global limit on concurrent git processes.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
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
}

// Options configures a [Dispatcher].
type Options struct {
	Mirrorer mirror.Mirrorer
	Source   config.Source
	Targets  []config.Target
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

// run performs one mirror run: clone once, push to every target in parallel.
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
	start := time.Now()
	logger.InfoContext(ctx, "mirror started")

	ws, err := d.clone(ctx, e)
	if err != nil {
		for _, t := range d.opts.Targets {
			d.finishTarget(ctx, logger, t.Name, start, err)
		}
		logger.ErrorContext(ctx, "mirror failed", slog.Duration("duration", time.Since(start)), slog.Any("error", err))

		return
	}
	defer func() {
		if closeErr := ws.Close(); closeErr != nil {
			logger.WarnContext(ctx, "cleaning up clone failed", slog.Any("error", closeErr))
		}
	}()

	errs := make([]error, len(d.opts.Targets))
	var wg sync.WaitGroup
	for i := range d.opts.Targets {
		target := &d.opts.Targets[i]
		// Each goroutine writes only its own slice element, so the results
		// need no lock; wg.Wait below makes them visible to this goroutine.
		wg.Go(func() {
			pushStart := time.Now()
			errs[i] = d.push(ctx, ws, target, e)
			d.finishTarget(ctx, logger, target.Name, pushStart, errs[i])
		})
	}
	wg.Wait()

	if pushErr := errors.Join(errs...); pushErr != nil {
		logger.ErrorContext(ctx, "mirror finished with errors",
			slog.Duration("duration", time.Since(start)), slog.Any("error", pushErr))

		return
	}
	logger.InfoContext(ctx, "mirror finished", slog.Duration("duration", time.Since(start)))
}

func (d *Dispatcher) clone(ctx context.Context, e webhook.Event) (mirror.Workspace, error) {
	release, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	return d.opts.Mirrorer.Clone(ctx, mirror.Remote{
		URL:      e.Repository.CloneURL,
		Username: d.opts.Source.Username,
		Token:    d.opts.Source.Token,
	})
}

func (d *Dispatcher) push(ctx context.Context, ws mirror.Workspace, t *config.Target, e webhook.Event) error {
	url, err := t.RemoteURL(e.Repository)
	if err != nil {
		return err
	}

	release, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	return ws.Push(ctx, mirror.Remote{URL: url, Username: t.Username, Token: t.Token})
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
