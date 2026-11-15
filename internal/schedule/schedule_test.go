package schedule_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/schedule"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const waitTimeout = 5 * time.Second

// fakeSource answers Repos with one queued result per call, repeating the
// last one.
type fakeSource struct {
	forge.Source // only Repos is used

	mu      sync.Mutex
	results []reposResult
}

type reposResult struct {
	repos []repo.Repository
	err   error
}

func (s *fakeSource) Repos(context.Context) ([]repo.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}

	return r.repos, r.err
}

type rejectFilter struct{ name string }

func (f rejectFilter) Matches(r repo.Repository) bool { return r.Name != f.name }

type recordingDispatcher struct {
	mu     sync.Mutex
	events []webhook.Event
}

func (d *recordingDispatcher) Dispatch(_ context.Context, e webhook.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, e)
}

func (d *recordingDispatcher) snapshot() []webhook.Event {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]webhook.Event(nil), d.events...)
}

type runResult struct {
	repos int
	err   error
}

// chanObserver reports every finished run on a channel so tests can wait
// for runs without sleeping.
type chanObserver chan runResult

func (o chanObserver) ScheduledRunFinished(_ context.Context, repos int, err error) {
	o <- runResult{repos: repos, err: err}
}

func repos(names ...string) []repo.Repository {
	out := make([]repo.Repository, 0, len(names))
	for _, n := range names {
		out = append(out, repo.Repository{Owner: "acme", Name: n, FullName: "acme/" + n})
	}

	return out
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

type harness struct {
	dispatcher *recordingDispatcher
	observer   chanObserver
	tick       chan time.Time
	cancel     context.CancelFunc
	done       chan struct{}
}

func start(t *testing.T, src forge.Source, flt interface{ Matches(repo.Repository) bool }) *harness {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	h := &harness{
		dispatcher: &recordingDispatcher{},
		observer:   make(chanObserver, 10),
		tick:       make(chan time.Time),
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	go func() {
		defer close(h.done)
		schedule.Run(ctx, schedule.Options{
			Interval:   time.Hour,
			Source:     src,
			Filter:     flt,
			Dispatcher: h.dispatcher,
			Logger:     discard(),
			Observer:   h.observer,
			Tick:       h.tick,
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-h.done
	})

	return h
}

func (h *harness) waitRun(t *testing.T) runResult {
	t.Helper()

	select {
	case r := <-h.observer:
		return r
	case <-time.After(waitTimeout):
		t.Fatal("scheduled run did not finish")

		return runResult{}
	}
}

func assertScheduledEvent(t *testing.T, e webhook.Event, wantRepo string) {
	t.Helper()

	if e.Repository.FullName != wantRepo {
		t.Errorf("event repository = %q, want %q", e.Repository.FullName, wantRepo)
	}
	if e.Kind != webhook.EventScheduled {
		t.Errorf("event %s kind = %v, want EventScheduled", wantRepo, e.Kind)
	}
	stamp, ok := strings.CutPrefix(e.Delivery, "schedule-")
	if !ok {
		t.Errorf("event %s delivery = %q, want schedule-<time>", wantRepo, e.Delivery)
	}
	if _, err := time.Parse(time.RFC3339, stamp); err != nil {
		t.Errorf("event %s delivery time: %v", wantRepo, err)
	}
}

func TestRunDispatchesFilteredRepos(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reject string
		want   []string
	}{
		{name: "one rejected", reject: "b", want: []string{"acme/a", "acme/c"}},
		{name: "none rejected", reject: "", want: []string{"acme/a", "acme/b", "acme/c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := &fakeSource{results: []reposResult{{repos: repos("a", "b", "c")}}}
			h := start(t, src, rejectFilter{name: tc.reject})

			if r := h.waitRun(t); r.err != nil || r.repos != len(tc.want) {
				t.Errorf("run = %+v, want %d repositories without error", r, len(tc.want))
			}

			events := h.dispatcher.snapshot()
			if len(events) != len(tc.want) {
				t.Fatalf("dispatched %d events, want %d: %+v", len(events), len(tc.want), events)
			}
			for i, e := range events {
				assertScheduledEvent(t, e, tc.want[i])
			}
		})
	}
}

func TestRunImmediatelyAndOnTick(t *testing.T) {
	t.Parallel()

	src := &fakeSource{results: []reposResult{{repos: repos("a")}}}
	h := start(t, src, rejectFilter{})

	h.waitRun(t)
	select {
	case r := <-h.observer:
		t.Fatalf("second run %+v before any tick", r)
	case <-time.After(50 * time.Millisecond):
	}

	h.tick <- time.Now()
	h.waitRun(t)

	if n := len(h.dispatcher.snapshot()); n != 2 {
		t.Errorf("dispatched %d events after start and one tick, want 2", n)
	}
}

func TestRunListErrorContinues(t *testing.T) {
	t.Parallel()

	listErr := errors.New("forgejo down")
	src := &fakeSource{results: []reposResult{{err: listErr}, {repos: repos("a", "b")}}}
	h := start(t, src, rejectFilter{})

	if r := h.waitRun(t); !errors.Is(r.err, listErr) {
		t.Errorf("first run error = %v, want %v", r.err, listErr)
	}
	if n := len(h.dispatcher.snapshot()); n != 0 {
		t.Errorf("dispatched %d events after a list error, want 0", n)
	}

	h.tick <- time.Now()
	if r := h.waitRun(t); r.err != nil || r.repos != 2 {
		t.Errorf("second run = %+v, want 2 repositories without error", r)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tick bool
	}{
		{name: "manual tick", tick: true},
		{name: "real ticker", tick: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			observer := make(chanObserver, 10)
			opts := schedule.Options{
				Interval:   time.Hour,
				Source:     &fakeSource{results: []reposResult{{repos: repos("a")}}},
				Filter:     rejectFilter{},
				Dispatcher: &recordingDispatcher{},
				Logger:     discard(),
				Observer:   observer,
			}
			if tc.tick {
				opts.Tick = make(chan time.Time)
			}

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				schedule.Run(ctx, opts)
			}()

			<-observer
			cancel()
			select {
			case <-done:
			case <-time.After(waitTimeout):
				t.Fatal("Run did not return after cancel")
			}
		})
	}
}

// blockingSource hangs in Repos until the caller's context ends.
type blockingSource struct {
	forge.Source

	listing chan struct{}
}

func (s blockingSource) Repos(ctx context.Context) ([]repo.Repository, error) {
	close(s.listing)
	<-ctx.Done()

	return nil, ctx.Err()
}

// TestRunCancelledListingNotReported: a listing aborted by shutdown says
// nothing about Forgejo and must not count as a failed run.
func TestRunCancelledListingNotReported(t *testing.T) {
	t.Parallel()

	observer := make(chanObserver, 10)
	src := blockingSource{listing: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx, schedule.Options{
			Source:     src,
			Filter:     rejectFilter{},
			Dispatcher: &recordingDispatcher{},
			Logger:     discard(),
			Observer:   observer,
			Tick:       make(chan time.Time),
		})
	}()

	<-src.listing
	cancel()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("Run did not return after cancel")
	}
	if len(observer) != 0 {
		t.Errorf("observer saw %+v, want no report for a cancelled run", <-observer)
	}
}
