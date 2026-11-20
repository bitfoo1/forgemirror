// Package dispatch runs mirror operations in the background: one clone per
// event, pushes to all targets in parallel, at most one run per repository
// at a time, and global limits on concurrent git processes and forge API
// steps. Targets with a
// forge type additionally get their repository created and their settings
// and labels synchronised through the forge API.
//
// Every run first computes a [plan.Repo] with read-only calls. A run that
// applies then executes it and logs every change; in review mode
// (sync.auto false) only runs requested from the UI apply.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/registry"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// Observer is notified about mirror activity (metrics, status). All methods
// must be safe for concurrent use. repo is the full name ("owner/name") of the
// source repository.
type Observer interface {
	// RunStarted and RunFinished bracket one run (mirror or deletion) of one
	// repository; apply is false for a run that only plans.
	RunStarted(ctx context.Context, repo string, apply bool)
	RunFinished(ctx context.Context, repo string)
	// PlanFinished reports the plan of a run, before it is applied.
	PlanFinished(ctx context.Context, p plan.Repo)
	// TargetFinished reports the outcome for one target; err is nil on success.
	TargetFinished(ctx context.Context, repo, target string, duration time.Duration, err error)
	// SyncFinished reports one forge API operation ("create", "settings",
	// "labels", "releases", "packages" or "delete") on a typed target; err is
	// nil on success.
	SyncFinished(ctx context.Context, repo, target, operation string, err error)
}

// Options configures a [Dispatcher].
type Options struct {
	Mirrorer mirror.Mirrorer
	// Mutation grants a permit held through one remote operation.
	Mutation func(repo, target string) (func(), error)
	Retries  RetryState
	// Source holds the credentials for cloning from Forgejo.
	Source  config.Source
	Targets []config.Target
	// ForgeSource reads repository settings and labels from the Forgejo API.
	// It is nil when no target has a type. It cannot be called Source, which
	// already names the clone credentials.
	ForgeSource forge.Source
	// Forges holds the API client of every typed target, keyed by target name.
	Forges map[string]forge.Target
	// Registry copies container packages; nil disables package sync.
	Registry Registry
	// TransferTimeout bounds the release and package step of one target
	// (mirror.timeout); zero means no bound beyond the run's context.
	TransferTimeout time.Duration
	// MaxConcurrent caps git processes (clones and pushes) across all runs,
	// and separately the forge API steps across all runs.
	MaxConcurrent int
	// Auto applies push, scheduled and delete events (sync.auto); otherwise
	// they only plan and only [webhook.EventSync] applies.
	Auto bool
	// BeforeApply must durably record the plan before any remote mutation.
	BeforeApply func(context.Context, plan.Repo) error
	// VerifyAfterApply publishes one fresh read-only comparison after each sync.
	VerifyAfterApply bool
	Logger           *slog.Logger
	// Observer is optional.
	Observer Observer
	// Now returns the current time; nil means [time.Now].
	Now func() time.Time
}

// Registry reads and copies container image tags.
type Registry interface {
	Digests(ctx context.Context, r registry.Repository, tags []string, deniedIsMissing bool) (map[string]string, error)
	Copy(ctx context.Context, src, dst registry.Repository, tag string) error
}

// Dispatcher implements [webhook.Dispatcher].
type Dispatcher struct {
	opts Options

	// sem bounds concurrent git processes; a buffered channel is the idiomatic
	// counting semaphore and, unlike a mutex, can be acquired with a timeout.
	sem chan struct{}
	// apiSem bounds concurrent forge API steps (reading the source, preparing,
	// syncing or deleting one target). Without it a scheduled sweep starts
	// every repository at once and its API calls hit forge rate limits for
	// concurrent requests; reusing mirror.max_concurrent avoids another knob.
	//
	// Lock order: a goroutine never holds an API slot and a git slot at the
	// same time, so the two semaphores cannot deadlock each other.
	apiSem chan struct{}

	// base outlives individual requests; cancelling it aborts all running git
	// processes when the graceful shutdown runs out of time.
	base   context.Context
	cancel context.CancelFunc

	// mu guards running, known and closed. Handler goroutines call Dispatch
	// concurrently, and runs remove themselves when done: without the mutex
	// two pushes to one repository could both start a run (data race on the
	// map, duplicate work).
	mu      sync.Mutex
	running map[string]*repoState
	// known holds the newest repository data per full name, so that UI
	// actions, which only carry a name, get the clone URL. Every entry passed
	// the filter on its way in.
	known  map[string]repo.Repository
	closed bool

	retryStop    context.CancelFunc
	retryBlocked bool
	wg           sync.WaitGroup
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
	ctx    context.Context
	event  webhook.Event
	apply  bool
	repair bool
}

var _ webhook.Dispatcher = (*Dispatcher)(nil)

// New returns a [Dispatcher].
func New(opts Options) *Dispatcher {
	if opts.Observer == nil {
		opts.Observer = noopObserver{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	base, cancel := context.WithCancel(context.Background())

	return &Dispatcher{
		opts:    opts,
		sem:     make(chan struct{}, opts.MaxConcurrent),
		apiSem:  make(chan struct{}, opts.MaxConcurrent),
		base:    base,
		cancel:  cancel,
		running: make(map[string]*repoState),
		known:   make(map[string]repo.Repository),
	}
}

// applies reports whether a run for e changes the targets.
func (d *Dispatcher) applies(e webhook.Event) bool {
	if e.Operator && e.Kind != webhook.EventRefresh {
		return true
	}
	switch e.Kind {
	case webhook.EventSync:
		return true
	case webhook.EventRefresh, webhook.EventRepairLFS:
		return false
	case webhook.EventPush, webhook.EventScheduled, webhook.EventDelete:
		return d.opts.Auto
	}

	return d.opts.Auto
}

// Dispatch implements [webhook.Dispatcher]. It never blocks on git.
func (d *Dispatcher) Dispatch(ctx context.Context, e webhook.Event) {
	if e.Kind == webhook.EventSync || e.Kind == webhook.EventRepairLFS {
		e.Operator = true
	}
	key := e.Repository.FullName
	logger := d.eventLogger(e)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		logger.WarnContext(ctx, "shutting down; event dropped")

		return
	}

	d.known[key] = e.Repository
	current := &queuedEvent{ctx: ctx, event: e, apply: d.applies(e), repair: e.Kind == webhook.EventRepairLFS}

	if state, busy := d.running[key]; busy {
		if queued := state.next; queued != nil {
			// A queued sync must survive a later event that only plans, or a
			// push in review mode would silently cancel the operator's sync.
			current.event.Target = mergedScope(current, queued)
			current.apply = current.apply || queued.apply
			current.repair = current.repair || queued.repair
			current.event.Operator = current.event.Operator || queued.event.Operator
			current.event.Retry = current.event.Retry && queued.event.Retry
			// The repository is gone once a deletion is queued; letting a later
			// push replace it would recreate the target repository.
			if queued.event.Kind == webhook.EventDelete && e.Kind != webhook.EventDelete {
				queued.apply = current.apply
				queued.event.Operator = current.event.Operator
				queued.event.Target = current.event.Target
				queued.event.Retry = current.event.Retry
				logger.InfoContext(ctx, "deletion already queued; event merged into it")

				return
			}
		}
		state.next = current
		logger.InfoContext(ctx, "run already in progress; queued one follow-up run")

		return
	}

	d.running[key] = &repoState{}
	d.wg.Go(func() { d.runLoop(current) })
}

func mergedScope(current, queued *queuedEvent) string {
	currentWrites, queuedWrites := current.apply || current.repair, queued.apply || queued.repair
	if currentWrites && !queuedWrites {
		return current.event.Target
	}
	if queuedWrites && !currentWrites {
		return queued.event.Target
	}
	return mergedTarget(current.event.Target, queued.event.Target)
}

func mergedTarget(a, b string) string {
	if a == b {
		return a
	}
	return ""
}

// Known returns the newest repository data seen for fullName ("owner/name").
func (d *Dispatcher) Known(fullName string) (repo.Repository, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	r, ok := d.known[fullName]

	return r, ok
}

// KnownNames returns the full names of all known repositories, sorted.
func (d *Dispatcher) KnownNames() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Sorted(maps.Keys(d.known))
}

// forget drops a repository that no longer exists on the source.
func (d *Dispatcher) forget(fullName string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.known, fullName)
}

// Queued returns the repositories that have a run in progress, each mapped to
// whether a follow-up run is queued behind it.
func (d *Dispatcher) Queued() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make(map[string]bool, len(d.running))
	for repo, state := range d.running {
		out[repo] = state.next != nil
	}

	return out
}

// Shutdown stops accepting events and waits for running mirrors. If ctx ends
// first, running git processes are killed and ctx's error is returned.
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	d.closed = true
	if d.retryStop != nil {
		d.retryStop()
	}
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

// runLoop runs first, then any follow-up event queued meanwhile.
func (d *Dispatcher) runLoop(first *queuedEvent) {
	for current := first; current != nil; current = d.nextRun(current.event) {
		d.run(current.ctx, current.event, current.apply, current.repair)
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

// run plans e and, when apply is set, executes the plan: a deletion or a
// mirror run.
func (d *Dispatcher) run(eventCtx context.Context, e webhook.Event, apply, repair bool) {
	// The run keeps the event's values but is cancelled by the dispatcher's
	// lifetime, not by the (already finished) HTTP request.
	ctx, cancel := context.WithCancel(eventCtx)
	defer cancel()
	stop := context.AfterFunc(d.base, cancel)
	defer stop()

	name := e.Repository.FullName
	d.opts.Observer.RunStarted(ctx, name, apply || repair)
	defer d.opts.Observer.RunFinished(ctx, name)

	logger := d.eventLogger(e)
	start := time.Now()

	runs := d.targetRuns(name)
	if e.Target != "" {
		runs = slices.DeleteFunc(runs, func(tr *targetRun) bool { return tr.cfg.Name != e.Target })
	}
	p := d.plan(ctx, logger, runs, e)
	p.Apply = apply || repair
	if p.Apply && !e.Retry {
		d.clearRetryBudget(ctx, runs, e, repair && !apply)
	}
	d.opts.Observer.PlanFinished(ctx, p)
	logPlan(ctx, logger, p)
	if err := d.checkpoint(ctx, p); err != nil {
		d.mu.Lock()
		d.retryBlocked = true
		d.mu.Unlock()
		logger.ErrorContext(ctx, "sync blocked: persistent checkpoint failed")
		for _, tr := range runs {
			d.finishTarget(ctx, logger, tr, start, nil, err)
		}
		return
	}

	switch {
	case repair && !apply && !p.SourceDeleted:
		d.repairLFS(ctx, logger, runs, e, start)
	case !apply:
		if p.SourceDeleted && p.Changes() == 0 {
			d.forget(name)
		}
	case p.SourceDeleted:
		if d.deleteRepos(ctx, logger, runs) == nil {
			d.forget(name)
		}
	default:
		d.apply(ctx, logger, runs, e, start, repair)
	}
	if apply || repair {
		d.recordRetryOutcomes(ctx, runs, e, apply, repair, p.SourceDeleted)
	}
	if apply && d.opts.VerifyAfterApply && ctx.Err() == nil {
		d.verify(ctx, logger, e)
	}
}

func (d *Dispatcher) checkpoint(ctx context.Context, p plan.Repo) error {
	if !p.Apply || d.opts.BeforeApply == nil {
		return nil
	}
	return d.opts.BeforeApply(ctx, p)
}

// apply executes the plan of a mirror run: create and privatise target
// repositories, push, then bring settings and labels in line.
func (d *Dispatcher) apply(
	ctx context.Context,
	logger *slog.Logger,
	runs []*targetRun,
	e webhook.Event,
	start time.Time,
	repair bool,
) {
	logger.InfoContext(ctx, "mirror started")

	// Targets whose plan failed show up as failed pushes, as before the plan
	// existed, so metrics and status count them.
	for _, tr := range runs {
		if tr.err != nil {
			d.finishTarget(ctx, logger, tr, start, nil, tr.err)
		}
	}

	parallel(pendingRuns(runs), func(tr *targetRun) {
		if tr.typed() {
			if err := d.mutate(tr, func() error { return d.prepareTarget(ctx, logger, tr) }); err != nil {
				d.fail(ctx, logger, tr, start, err)
			}
		}
	})
	d.mirror(ctx, logger, runs, e, start, repair, true)
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

// mirror clones once and pushes to every target whose plan has ref changes.
func (d *Dispatcher) mirror(
	ctx context.Context,
	logger *slog.Logger,
	runs []*targetRun,
	e webhook.Event,
	start time.Time,
	repair, publish bool,
) {
	var pending []*targetRun
	for _, tr := range pendingRuns(runs) {
		if tr.plan.NeedsPush() || repair {
			pending = append(pending, tr)
		}
	}
	if len(pending) == 0 {
		logger.InfoContext(ctx, "refs already in sync; clone and push skipped")

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

	if err = d.fetchLFS(ctx, ws, e); err != nil {
		for _, tr := range pending {
			d.opts.Observer.SyncFinished(ctx, e.Repository.FullName, tr.cfg.Name, "lfs", err)
			d.fail(ctx, logger, tr, start, err)
		}
		return
	}

	parallel(pending, func(tr *targetRun) {
		pushStart := time.Now()
		lfsErr := d.uploadLFS(ctx, ws, tr.cfg, e)
		d.opts.Observer.SyncFinished(ctx, e.Repository.FullName, tr.cfg.Name, "lfs", lfsErr)
		if lfsErr != nil {
			d.fail(ctx, logger, tr, pushStart, lfsErr)
			return
		}
		if !publish || !tr.plan.NeedsPush() {
			return
		}
		var updates []mirror.RefUpdate
		updates, tr.err = d.push(ctx, ws, tr.cfg, e)
		d.finishTarget(ctx, logger, tr, pushStart, updates, tr.err)
	})
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

func (d *Dispatcher) push(
	ctx context.Context,
	ws mirror.Workspace,
	t *config.Target,
	e webhook.Event,
) ([]mirror.RefUpdate, error) {
	permit, permitErr := d.beginMutation(e.Repository.FullName, t.Name)
	if permitErr != nil {
		return nil, permitErr
	}
	defer permit()
	remote, err := targetRemote(t, e)
	if err != nil {
		return nil, err
	}

	release, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	return ws.Push(ctx, remote)
}

func (d *Dispatcher) acquire(ctx context.Context) (func(), error) {
	return acquireSlot(ctx, d.sem, "git")
}

func (d *Dispatcher) acquireAPI(ctx context.Context) (func(), error) {
	return acquireSlot(ctx, d.apiSem, "forge API")
}

func acquireSlot(ctx context.Context, sem chan struct{}, kind string) (func(), error) {
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free %s slot: %w", kind, ctx.Err())
	}
}

func (d *Dispatcher) finishTarget(
	ctx context.Context,
	logger *slog.Logger,
	tr *targetRun,
	start time.Time,
	updates []mirror.RefUpdate,
	err error,
) {
	duration := time.Since(start)
	d.opts.Observer.TargetFinished(ctx, tr.repo, tr.cfg.Name, duration, err)

	logger = logger.With(slog.String("target", tr.cfg.Name))
	if err != nil {
		logger.ErrorContext(ctx, "target failed", slog.Duration("duration", duration),
			slog.String("outcome", "failure"), slog.Any("error", err))

		return
	}
	logger.InfoContext(ctx, "target mirrored", slog.Duration("duration", duration), slog.String("outcome", "success"))
	logRefUpdates(ctx, logger, updates)
}

func (d *Dispatcher) eventLogger(e webhook.Event) *slog.Logger {
	return d.opts.Logger.With(
		slog.String("repository", e.Repository.FullName),
		slog.String("ref", e.Ref),
		slog.String("delivery", e.Delivery),
	)
}

type noopObserver struct{}

func (noopObserver) RunStarted(context.Context, string, bool)                             {}
func (noopObserver) PlanFinished(context.Context, plan.Repo)                              {}
func (noopObserver) RunFinished(context.Context, string)                                  {}
func (noopObserver) TargetFinished(context.Context, string, string, time.Duration, error) {}
func (noopObserver) SyncFinished(context.Context, string, string, string, error)          {}

// Observers fans every event out to each of its observers in order.
type Observers []Observer

// RunStarted implements [Observer].
func (obs Observers) RunStarted(ctx context.Context, repo string, apply bool) {
	for _, o := range obs {
		o.RunStarted(ctx, repo, apply)
	}
}

// PlanFinished implements [Observer].
func (obs Observers) PlanFinished(ctx context.Context, p plan.Repo) {
	for _, o := range obs {
		o.PlanFinished(ctx, p)
	}
}

// RunFinished implements [Observer].
func (obs Observers) RunFinished(ctx context.Context, repo string) {
	for _, o := range obs {
		o.RunFinished(ctx, repo)
	}
}

// TargetFinished implements [Observer].
func (obs Observers) TargetFinished(ctx context.Context, repo, target string, duration time.Duration, err error) {
	for _, o := range obs {
		o.TargetFinished(ctx, repo, target, duration, err)
	}
}

// SyncFinished implements [Observer].
func (obs Observers) SyncFinished(ctx context.Context, repo, target, operation string, err error) {
	for _, o := range obs {
		o.SyncFinished(ctx, repo, target, operation, err)
	}
}
