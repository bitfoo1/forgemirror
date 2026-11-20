package status_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestRetryPersistenceAndExhaustion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := status.New(status.Options{ControlPersistence: db.Namespace("controls"), Now: func() time.Time { return now }})
	if err = s.RecordRetry(t.Context(), "acme/app", "github", "sync", false, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	jobs := s.Retries()
	if len(jobs) != 1 || jobs[0].State != "waiting" || !jobs[0].Next.After(now) {
		t.Fatalf("jobs=%+v", jobs)
	}
	if err = s.RecordRetry(t.Context(), "acme/app", "github", "repair", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.Retries()) != 1 {
		t.Fatal("object repair cancelled publication retry")
	}
	if err = s.RecordRetry(t.Context(), "acme/app", "github", "sync", false, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if len(s.Retries()) != 1 {
		t.Fatal("shutdown cancelled durable retry")
	}
	restored := status.New(
		status.Options{ControlPersistence: db.Namespace("controls"), Now: func() time.Time { return now }},
	)
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err = restored.RecordRetry(
			t.Context(),
			"acme/app",
			"github",
			"sync",
			false,
			context.DeadlineExceeded,
		); err != nil {
			t.Fatal(err)
		}
	}
	if jobs = restored.Retries(); len(jobs) != 1 || jobs[0].State != "exhausted" {
		t.Fatalf("jobs=%+v", jobs)
	}
	if err = restored.RecordRetry(t.Context(), "acme/app", "github", "sync", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(restored.Retries()) != 0 {
		t.Fatal("success did not clear retry")
	}
}
