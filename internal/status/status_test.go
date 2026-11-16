package status_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

// clock is a fake time source advancing one second per call.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)

	return c.now
}

func newStore() *status.Store {
	c := &clock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}

	return status.New(status.Options{ScheduleInterval: time.Hour, Now: c.Now})
}

func TestRunLifecycle(t *testing.T) {
	t.Parallel()

	s := newStore()
	ctx := t.Context()

	s.RunStarted(ctx, "acme/b", true)
	if snap := s.Snapshot(); len(snap.Repos) != 1 || !snap.Repos[0].Running {
		t.Fatalf("after RunStarted: %+v, want acme/b running", snap.Repos)
	}

	s.SyncFinished(ctx, "acme/b", "gh", "settings", nil)
	s.SyncFinished(ctx, "acme/b", "gh", "create", errors.New("forbidden"))
	s.SyncFinished(ctx, "acme/b", "gh", "create", nil)
	s.TargetFinished(ctx, "acme/b", "gh", 2*time.Second, nil)
	s.TargetFinished(ctx, "acme/b", "cb", time.Second, errors.New("pushing to https://codeberg.org/acme/b.git: denied"))
	s.RunFinished(ctx, "acme/b")
	s.RunStarted(ctx, "acme/a", true)
	s.RunFinished(ctx, "acme/a")

	snap := s.Snapshot()
	if len(snap.Repos) != 2 || snap.Repos[0].Name != "acme/a" || snap.Repos[1].Name != "acme/b" {
		t.Fatalf("repos = %+v, want acme/a, acme/b", snap.Repos)
	}
	b := snap.Repos[1]
	if b.Running || b.LastFinished.Before(b.LastStarted) {
		t.Errorf("acme/b = %+v, want finished after start", b)
	}
	if len(b.Targets) != 2 || b.Targets[0].Name != "cb" || b.Targets[1].Name != "gh" {
		t.Fatalf("targets = %+v, want cb, gh", b.Targets)
	}
	if cb := b.Targets[0]; cb.Error == "" || cb.Duration != time.Second || cb.LastRun.IsZero() {
		t.Errorf("cb = %+v, want a failed push", cb)
	}
	gh := b.Targets[1]
	if gh.Error != "" || len(gh.Operations) != 2 ||
		gh.Operations[0].Name != "create" || gh.Operations[0].Error != "" ||
		gh.Operations[1].Name != "settings" {
		t.Errorf("gh = %+v, want success with latest create and settings results", gh)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	t.Parallel()

	s := newStore()
	s.SyncFinished(t.Context(), "acme/a", "gh", "labels", nil)

	snap := s.Snapshot()
	snap.Repos[0].Targets[0].Operations[0].Error = "changed"

	if got := s.Snapshot().Repos[0].Targets[0].Operations[0].Error; got != "" {
		t.Errorf("store changed through a snapshot: %q", got)
	}
}

func TestDeletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		errs     []error
		wantKept bool
	}{
		{name: "all deleted", errs: []error{nil, nil}},
		{name: "one failed", errs: []error{nil, errors.New("forbidden")}, wantKept: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			ctx := t.Context()
			s.RunStarted(ctx, "acme/a", true)
			s.TargetFinished(ctx, "acme/a", "gh", time.Second, nil)
			s.RunFinished(ctx, "acme/a")

			s.RunStarted(ctx, "acme/a", true)
			for i, err := range tc.errs {
				s.SyncFinished(ctx, "acme/a", fmt.Sprintf("t%d", i), "delete", err)
			}
			s.RunFinished(ctx, "acme/a")

			if kept := len(s.Snapshot().Repos) == 1; kept != tc.wantKept {
				t.Errorf("repository kept = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}

func TestDeletionFailureDoesNotStick(t *testing.T) {
	t.Parallel()

	s := newStore()
	ctx := t.Context()
	s.RunStarted(ctx, "acme/a", true)
	s.SyncFinished(ctx, "acme/a", "gh", "delete", errors.New("forbidden"))
	s.RunFinished(ctx, "acme/a")
	// A later successful deletion (e.g. after fixing the token) forgets it.
	s.RunStarted(ctx, "acme/a", true)
	s.SyncFinished(ctx, "acme/a", "gh", "delete", nil)
	s.RunFinished(ctx, "acme/a")

	if n := len(s.Snapshot().Repos); n != 0 {
		t.Errorf("repos = %d, want 0", n)
	}
}

func TestPlans(t *testing.T) {
	t.Parallel()

	s := newStore()
	ctx := t.Context()

	refs := make([]plan.RefChange, 150)
	for i := range refs {
		refs[i] = plan.RefChange{Ref: fmt.Sprintf("refs/tags/v%d", i), New: "x"}
	}
	s.RunStarted(ctx, "acme/a", false)
	if r := s.Snapshot().Repos[0]; !r.Running || r.Applying || r.Plan != nil {
		t.Fatalf("planning run = %+v, want running, not applying, no plan yet", r)
	}
	s.PlanFinished(ctx, plan.Repo{Name: "acme/a", Targets: []plan.Target{{Name: "gh", Refs: refs}}})
	s.RunFinished(ctx, "acme/a")

	r := s.Snapshot().Repos[0]
	if r.Plan == nil || len(r.Plan.Targets[0].Refs) != 100 || r.Plan.Targets[0].RefsOmitted != 50 {
		t.Fatalf("plan = %+v, want 100 refs kept and 50 omitted", r.Plan)
	}
	if r.Plan.Changes() != 150 {
		t.Errorf("Changes = %d, want 150", r.Plan.Changes())
	}

	s.RunStarted(ctx, "acme/a", true)
	if applying := s.Snapshot().Repos[0]; !applying.Applying || applying.Plan == nil {
		t.Errorf("applying run = %+v, want applying and the old plan kept", applying)
	}
	s.RunFinished(ctx, "acme/a")
	if s.Snapshot().Repos[0].Applying {
		t.Error("Applying still set after the run finished")
	}
}

func TestSourceDeletedPlans(t *testing.T) {
	t.Parallel()

	pending := plan.Repo{SourceDeleted: true, Targets: []plan.Target{{Name: "gh", Delete: true}}}
	tests := []struct {
		name      string
		apply     bool
		plan      plan.Repo
		deleteErr error
		wantKept  bool
	}{
		{name: "reviewed deletion stays visible", plan: pending, wantKept: true},
		{name: "nothing left to delete", plan: plan.Repo{SourceDeleted: true}},
		{name: "applied deletion", apply: true, plan: pending},
		{name: "failed deletion", apply: true, plan: pending, deleteErr: errors.New("forbidden"), wantKept: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			ctx := t.Context()
			tc.plan.Name, tc.plan.Apply = "acme/a", tc.apply
			s.RunStarted(ctx, "acme/a", tc.apply)
			s.PlanFinished(ctx, tc.plan)
			if tc.apply {
				s.SyncFinished(ctx, "acme/a", "gh", "delete", tc.deleteErr)
			}
			s.RunFinished(ctx, "acme/a")

			if kept := len(s.Snapshot().Repos) == 1; kept != tc.wantKept {
				t.Errorf("repository kept = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}

func TestSchedule(t *testing.T) {
	t.Parallel()

	s := newStore()
	if snap := s.Snapshot(); snap.Schedule.Interval != time.Hour || !snap.Schedule.LastRun.IsZero() {
		t.Fatalf("initial schedule = %+v", snap.Schedule)
	}

	s.ScheduledRunFinished(t.Context(), 0, errors.New("forgejo down"))
	s.ScheduledRunFinished(t.Context(), 3, nil)

	got := s.Snapshot().Schedule
	if got.Runs != 2 || got.Repos != 3 || got.Error != "" || got.LastRun.IsZero() {
		t.Errorf("schedule = %+v, want 2 runs, last with 3 repos and no error", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	s := status.New(status.Options{})
	ctx := t.Context()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			repo := fmt.Sprintf("acme/r%d", i%3)
			for range 50 {
				s.RunStarted(ctx, repo, true)
				s.TargetFinished(ctx, repo, "gh", time.Millisecond, nil)
				s.SyncFinished(ctx, repo, "gh", "labels", nil)
				_ = s.Snapshot()
				s.RunFinished(ctx, repo)
			}
		})
	}
	wg.Wait()

	snap := s.Snapshot()
	if len(snap.Repos) != 3 {
		t.Fatalf("repos = %d, want 3", len(snap.Repos))
	}
	for _, r := range snap.Repos {
		if r.Running {
			t.Errorf("%s still running after all runs finished", r.Name)
		}
	}
}
