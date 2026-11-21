package status_test

import (
	"path/filepath"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/state"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestHistoryRestoresInterruptedAttempt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	opts := status.Options{Persistence: db, Now: func() time.Time { return now }}
	s := status.New(opts)
	s.RunStarted(t.Context(), "acme/app", "", true)
	s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true})
	s.RunFinished(t.Context(), "acme/app")
	s.RunStarted(t.Context(), "acme/app", "", true)
	s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true, Retry: true, ParentID: 1})
	restored := status.New(opts)
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, total := restored.History("acme/app", 0, 20)
	if total != 2 || entries[0].ID != 2 || entries[0].ParentID != 1 || entries[0].Status != "interrupted" ||
		entries[1].Status != "succeeded" {
		t.Fatalf("history=%+v total=%d", entries, total)
	}
}

func TestHistoryRetentionAndPagination(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s := status.New(status.Options{HistoryLimit: 2, HistoryRetention: time.Hour, Now: func() time.Time { return now }})
	for range 3 {
		s.RunStarted(t.Context(), "acme/app", "", true)
		s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", At: now, Apply: true, Trigger: "manual"})
		s.TargetFinished(t.Context(), "acme/app", "github", time.Second, nil)
		s.RunFinished(t.Context(), "acme/app")
		now = now.Add(time.Minute)
	}
	entries, total := s.History("acme/app", 0, 1)
	if total != 2 || len(entries) != 1 || entries[0].ID != 3 || entries[0].Status != "succeeded" {
		t.Fatalf("history=%+v total=%d", entries, total)
	}
	entries, total = s.History("acme/app", 1, 20)
	if total != 2 || len(entries) != 1 || entries[0].ID != 2 {
		t.Fatalf("second page=%+v total=%d", entries, total)
	}
	now = now.Add(2 * time.Hour)
	if entries, total = s.History("acme/app", 0, 20); len(entries) != 0 || total != 0 {
		t.Fatal("expired history retained")
	}
}
