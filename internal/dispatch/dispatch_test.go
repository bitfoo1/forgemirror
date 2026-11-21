package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const waitTimeout = 5 * time.Second

// fakeMirrorer records clones and pushes. Hooks let tests block or fail.
type fakeMirrorer struct {
	mu      sync.Mutex
	clones  []mirror.Remote
	pushes  []mirror.Remote
	closed  int
	active  int
	maxSeen int

	fetchLFSHook func(context.Context, mirror.Remote) error
	pushLFSHook  func(context.Context, mirror.Remote) error
	cloneHook    func(ctx context.Context, src mirror.Remote) error
	pushHook     func(ctx context.Context, dst mirror.Remote) error
	refsHook     func(ctx context.Context, r mirror.Remote) (map[string]string, error)
}

func (f *fakeMirrorer) Refs(ctx context.Context, r mirror.Remote) (map[string]string, error) {
	if f.refsHook != nil {
		return f.refsHook(ctx, r)
	}

	// Different on every remote, so every target needs a push by default.
	return map[string]string{"refs/heads/main": r.URL}, nil
}

func (f *fakeMirrorer) enter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active++
	f.maxSeen = max(f.maxSeen, f.active)
}

func (f *fakeMirrorer) leave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
}

func (f *fakeMirrorer) Clone(ctx context.Context, src mirror.Remote) (mirror.Workspace, error) {
	f.enter()
	defer f.leave()

	f.mu.Lock()
	f.clones = append(f.clones, src)
	f.mu.Unlock()

	if f.cloneHook != nil {
		if err := f.cloneHook(ctx, src); err != nil {
			return nil, err
		}
	}

	return &fakeWorkspace{f: f}, nil
}

type fakeWorkspace struct{ f *fakeMirrorer }

func (w *fakeWorkspace) FetchLFS(ctx context.Context, src mirror.Remote) error {
	if w.f.fetchLFSHook != nil {
		return w.f.fetchLFSHook(ctx, src)
	}
	return nil
}

func (w *fakeWorkspace) PushLFS(ctx context.Context, dst mirror.Remote) error {
	if w.f.pushLFSHook != nil {
		return w.f.pushLFSHook(ctx, dst)
	}
	return nil
}

func (w *fakeWorkspace) Push(ctx context.Context, dst mirror.Remote) ([]mirror.RefUpdate, error) {
	w.f.enter()
	defer w.f.leave()

	w.f.mu.Lock()
	w.f.pushes = append(w.f.pushes, dst)
	w.f.mu.Unlock()

	if w.f.pushHook != nil {
		if err := w.f.pushHook(ctx, dst); err != nil {
			return nil, err
		}
	}

	return []mirror.RefUpdate{{Ref: "refs/heads/main", Kind: mirror.RefUpdated, Summary: "a..b"}}, nil
}

func (w *fakeWorkspace) Close() error {
	w.f.mu.Lock()
	defer w.f.mu.Unlock()
	w.f.closed++

	return nil
}

func (f *fakeMirrorer) snapshot() (clones, pushes []mirror.Remote, closed, maxSeen int) { //nolint:nonamedreturns // documents the tuple
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.clones), slices.Clone(f.pushes), f.closed, f.maxSeen
}

type targetResult struct {
	target string
	err    error
}

type syncResult struct {
	target, operation string
	err               error
}

type recordingObserver struct {
	mu      sync.Mutex
	started atomic.Int32
	done    atomic.Int32
	results []targetResult
	syncs   []syncResult
	plans   []plan.Repo
	applies []bool
	// repos collects the repository of every target and sync event.
	repos []string
}

func (o *recordingObserver) SyncFinished(_ context.Context, repo, target, operation string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.repos = append(o.repos, repo)
	o.syncs = append(o.syncs, syncResult{target: target, operation: operation, err: err})
}

func (o *recordingObserver) snapshot() ([]targetResult, []syncResult) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.results), slices.Clone(o.syncs)
}

func (o *recordingObserver) RunStarted(_ context.Context, repo, _ string, apply bool) {
	o.started.Add(1)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.repos = append(o.repos, repo)
	o.applies = append(o.applies, apply)
}

func (o *recordingObserver) PlanFinished(_ context.Context, p plan.Repo) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.plans = append(o.plans, p)
}

func (o *recordingObserver) planSnapshot() []plan.Repo {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.plans)
}

func (o *recordingObserver) RunFinished(context.Context, string) { o.done.Add(1) }

func (o *recordingObserver) TargetFinished(_ context.Context, repo, target string, _ time.Duration, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.repos = append(o.repos, repo)
	o.results = append(o.results, targetResult{target: target, err: err})
}

// observedRepos returns the distinct repositories seen in events.
func (o *recordingObserver) observedRepos() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	repos := slices.Clone(o.repos)
	slices.Sort(repos)

	return slices.Compact(repos)
}

// targets loads two validated targets through the real config package, so
// their URL templates are compiled exactly as in production.
func targets(t *testing.T) []config.Target {
	t.Helper()

	path := filepath.Join(t.TempDir(), "c.yaml")
	yaml := `
webhook:
  secret: s
targets:
  - name: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: gh-token
  - name: codeberg
    url: "https://codeberg.org/{{ .FullName }}.git"
    username: bot
    token: cb-token
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{File: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}

	return cfg.Targets
}

func event(name string) webhook.Event {
	return webhook.Event{
		Ref: "refs/heads/main",
		Repository: repo.Repository{
			Owner: "acme", Name: name, FullName: "acme/" + name,
			CloneURL: "https://git.example/acme/" + name + ".git",
		},
	}
}

func newDispatcher(t *testing.T, m *fakeMirrorer, obs dispatch.Observer, maxConcurrent int) *dispatch.Dispatcher {
	t.Helper()

	return dispatch.New(dispatch.Options{
		Mirrorer:      m,
		Source:        config.Source{Username: "oauth2", Token: "src-token"},
		Targets:       targets(t),
		MaxConcurrent: maxConcurrent,
		Auto:          true,
		Logger:        slog.New(slog.DiscardHandler),
		Observer:      obs,
	})
}

func shutdown(t *testing.T, d *dispatch.Dispatcher) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestFanOut(t *testing.T) {
	t.Parallel()

	m := &fakeMirrorer{}
	obs := &recordingObserver{}
	d := newDispatcher(t, m, obs, 4)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	clones, pushes, closed, _ := m.snapshot()
	wantClone := mirror.Remote{URL: "https://git.example/acme/app.git", Username: "oauth2", Token: "src-token"}
	if len(clones) != 1 || clones[0] != wantClone {
		t.Errorf("clones = %+v, want [%+v]", clones, wantClone)
	}

	slices.SortFunc(pushes, func(a, b mirror.Remote) int { return compareStrings(a.URL, b.URL) })
	wantPushes := []mirror.Remote{
		{URL: "https://codeberg.org/acme/app.git", Username: "bot", Token: "cb-token"},
		{URL: "https://github.com/acme/app.git", Username: "oauth2", Token: "gh-token"},
	}
	if !slices.Equal(pushes, wantPushes) {
		t.Errorf("pushes = %+v\nwant %+v", pushes, wantPushes)
	}
	if closed != 1 {
		t.Errorf("workspace closed %d times, want 1", closed)
	}
	if obs.started.Load() != 1 || obs.done.Load() != 1 {
		t.Errorf("runs started/finished = %d/%d, want 1/1", obs.started.Load(), obs.done.Load())
	}
	if got := obs.observedRepos(); !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("observed repositories = %q, want [acme/app]", got)
	}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestOneTargetFailureDoesNotStopOthers(t *testing.T) {
	t.Parallel()

	errRejected := errors.New("rejected")
	m := &fakeMirrorer{pushHook: func(_ context.Context, dst mirror.Remote) error {
		if dst.Token == "gh-token" {
			return errRejected
		}

		return nil
	}}
	obs := &recordingObserver{}
	d := newDispatcher(t, m, obs, 4)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	_, pushes, closed, _ := m.snapshot()
	if len(pushes) != 2 || closed != 1 {
		t.Errorf("pushes = %d, closed = %d; want 2 and 1", len(pushes), closed)
	}

	results := map[string]error{}
	for _, r := range obs.results {
		results[r.target] = r.err
	}
	if !errors.Is(results["github"], errRejected) || results["codeberg"] != nil {
		t.Errorf("target results = %v, want github failed and codeberg succeeded", results)
	}
}

func TestCloneFailureFailsAllTargets(t *testing.T) {
	t.Parallel()

	errClone := errors.New("clone failed")
	m := &fakeMirrorer{cloneHook: func(context.Context, mirror.Remote) error { return errClone }}
	obs := &recordingObserver{}
	d := newDispatcher(t, m, obs, 4)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	_, pushes, closed, _ := m.snapshot()
	if len(pushes) != 0 || closed != 0 {
		t.Errorf("pushes = %d, closed = %d; want none after failed clone", len(pushes), closed)
	}
	if len(obs.results) != 2 {
		t.Fatalf("target results = %v, want one per target", obs.results)
	}
	for _, r := range obs.results {
		if !errors.Is(r.err, errClone) {
			t.Errorf("target %s error = %v, want clone error", r.target, r.err)
		}
	}
}

// TestCoalescing: pushes arriving while a repository is being mirrored
// result in exactly one follow-up run, with the newest event.
func TestCoalescing(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan struct{}, 10)
	var cloneCount atomic.Int32
	m := &fakeMirrorer{cloneHook: func(ctx context.Context, _ mirror.Remote) error {
		started <- struct{}{}
		if cloneCount.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		return nil
	}}
	d := newDispatcher(t, m, nil, 4)

	d.Dispatch(t.Context(), event("app"))
	waitFor(t, started)
	if got := d.Queued(); !maps.Equal(got, map[string]bool{"acme/app": false}) {
		t.Errorf("Queued() while running = %v, want acme/app without follow-up", got)
	}

	for i := range 3 {
		e := event("app")
		e.Delivery = fmt.Sprintf("follow-up-%d", i)
		d.Dispatch(t.Context(), e)
	}
	if got := d.Queued(); !maps.Equal(got, map[string]bool{"acme/app": true}) {
		t.Errorf("Queued() with follow-up = %v, want acme/app queued", got)
	}
	close(release)

	waitFor(t, started) // the single follow-up run
	shutdown(t, d)

	if got := cloneCount.Load(); got != 2 {
		t.Errorf("clones = %d, want 2 (initial run + one coalesced follow-up)", got)
	}
	if got := d.Queued(); len(got) != 0 {
		t.Errorf("Queued() after shutdown = %v, want empty", got)
	}
}

func TestObserversFanOut(t *testing.T) {
	t.Parallel()

	a, b := &recordingObserver{}, &recordingObserver{}
	obs := dispatch.Observers{a, b}
	ctx := t.Context()
	obs.RunStarted(ctx, "acme/app", "", true)
	obs.TargetFinished(ctx, "acme/app", "gh", time.Second, nil)
	obs.SyncFinished(ctx, "acme/app", "gh", "labels", nil)
	obs.RunFinished(ctx, "acme/app")

	for i, o := range []*recordingObserver{a, b} {
		results, syncs := o.snapshot()
		if o.started.Load() != 1 || o.done.Load() != 1 || len(results) != 1 || len(syncs) != 1 {
			t.Errorf("observer %d: started=%d done=%d results=%d syncs=%d, want 1 each",
				i, o.started.Load(), o.done.Load(), len(results), len(syncs))
		}
	}
}

func TestDifferentRepositoriesRunConcurrently(t *testing.T) {
	t.Parallel()

	var inClone sync.WaitGroup
	inClone.Add(2)
	bothInside := make(chan struct{})
	go func() {
		inClone.Wait()
		close(bothInside)
	}()

	m := &fakeMirrorer{cloneHook: func(ctx context.Context, _ mirror.Remote) error {
		inClone.Done()
		select {
		case <-bothInside: // only reachable if both clones run at once
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	d := newDispatcher(t, m, nil, 4)

	d.Dispatch(t.Context(), event("one"))
	d.Dispatch(t.Context(), event("two"))

	select {
	case <-bothInside:
	case <-time.After(waitTimeout):
		t.Fatal("clones of different repositories did not run concurrently")
	}
	shutdown(t, d)
}

func TestConcurrencyLimit(t *testing.T) {
	t.Parallel()

	m := &fakeMirrorer{
		cloneHook: func(context.Context, mirror.Remote) error { time.Sleep(20 * time.Millisecond); return nil },
		pushHook:  func(context.Context, mirror.Remote) error { time.Sleep(20 * time.Millisecond); return nil },
	}
	d := newDispatcher(t, m, nil, 1)

	for _, name := range []string{"a", "b", "c"} {
		d.Dispatch(t.Context(), event(name))
	}
	shutdown(t, d)

	clones, pushes, _, maxSeen := m.snapshot()
	if len(clones) != 3 || len(pushes) != 6 {
		t.Errorf("clones = %d, pushes = %d; want 3 and 6", len(clones), len(pushes))
	}
	if maxSeen != 1 {
		t.Errorf("max concurrent git operations = %d, want 1", maxSeen)
	}
}

func TestShutdownDropsNewEvents(t *testing.T) {
	t.Parallel()

	m := &fakeMirrorer{}
	d := newDispatcher(t, m, nil, 4)
	shutdown(t, d)

	d.Dispatch(t.Context(), event("late"))
	shutdown(t, d) // idempotent, and must not wait for anything

	if clones, _, _, _ := m.snapshot(); len(clones) != 0 {
		t.Errorf("clones = %d after shutdown, want 0", len(clones))
	}
}

func TestShutdownWaitsForRunningMirror(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	var finished atomic.Bool
	m := &fakeMirrorer{cloneHook: func(context.Context, mirror.Remote) error {
		started <- struct{}{}
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)

		return nil
	}}
	d := newDispatcher(t, m, nil, 4)

	d.Dispatch(t.Context(), event("app"))
	waitFor(t, started)
	shutdown(t, d)

	if !finished.Load() {
		t.Error("Shutdown returned before the running mirror finished")
	}
}

func TestShutdownTimeoutCancelsMirror(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	cancelled := make(chan error, 1)
	m := &fakeMirrorer{cloneHook: func(ctx context.Context, _ mirror.Remote) error {
		started <- struct{}{}
		<-ctx.Done()
		cancelled <- ctx.Err()

		return ctx.Err()
	}}
	d := newDispatcher(t, m, nil, 4)

	d.Dispatch(t.Context(), event("app"))
	d.Dispatch(t.Context(), event("app")) // queued follow-up, dropped on shutdown
	waitFor(t, started)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := d.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown() error = %v, want context.DeadlineExceeded", err)
	}

	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("mirror context error = %v, want context.Canceled", err)
		}
	default:
		t.Error("running mirror was not cancelled")
	}

	if clones, _, _, _ := m.snapshot(); len(clones) != 1 {
		t.Errorf("clones = %d, want 1 (follow-up dropped)", len(clones))
	}
}

func waitFor(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for the mirror to start")
	}
}
