package status_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestAttemptSurvivesComparisonAndRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(t.Context(), path, "source")
	if err != nil {
		t.Fatal(err)
	}
	s := status.New(status.Options{Persistence: db})
	p := plan.Repo{
		Name:    "acme/app",
		At:      time.Now(),
		Apply:   true,
		Targets: []plan.Target{{Name: "gh", Refs: []plan.RefChange{{Ref: "refs/heads/main", Old: "old", New: "new"}}}},
	}
	s.RunStarted(t.Context(), p.Name, true)
	s.PlanFinished(t.Context(), p)
	if err = s.Checkpoint(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	s.TargetFinished(t.Context(), p.Name, "gh", time.Second, nil)
	s.PlanFinished(t.Context(), plan.Repo{Name: p.Name, At: time.Now(), Targets: []plan.Target{{Name: "gh"}}})
	s.RunFinished(t.Context(), p.Name)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(t.Context(), path, "source")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restored := status.New(status.Options{Persistence: db})
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := restored.Snapshot().Repos[0]
	if r.LastAttempt == nil || r.LastAttempt.Status != "succeeded" || r.LastAttempt.Plan.Changes() != 1 {
		t.Fatalf("attempt = %+v", r.LastAttempt)
	}
	if r.Plan == nil || r.Plan.Apply || r.Plan.Changes() != 0 || !r.Stale || r.Running {
		t.Fatalf("restored repo = %+v", r)
	}
	if len(r.LastAttempt.Targets) != 1 {
		t.Fatalf("attempt results = %+v", r.LastAttempt.Targets)
	}
}

type brokenPersistence struct{}

func (brokenPersistence) Load(context.Context) (map[string][]byte, error) {
	return map[string][]byte{}, nil
}

func (brokenPersistence) Save(context.Context, string, []byte) error { return errors.New("disk full") }

func TestCheckpointFailsClosed(t *testing.T) {
	t.Parallel()
	s := status.New(status.Options{Persistence: brokenPersistence{}})
	p := plan.Repo{Name: "acme/app", Apply: true}
	s.RunStarted(t.Context(), p.Name, true)
	s.PlanFinished(t.Context(), p)
	if err := s.Checkpoint(t.Context(), p); err == nil {
		t.Fatal("failed persistence did not block apply")
	}
	if s.Snapshot().Error == "" {
		t.Fatal("missing persistence warning")
	}
}

func TestInterruptedAttempt(t *testing.T) {
	t.Parallel()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := status.New(status.Options{Persistence: db})
	s.RunStarted(t.Context(), "acme/app", true)
	s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true})
	restored := status.New(status.Options{Persistence: db})
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().Repos[0].LastAttempt.Status; got != "interrupted" {
		t.Fatalf("status = %s", got)
	}
}
