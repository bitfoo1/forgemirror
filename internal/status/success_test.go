package status_test

import (
	"errors"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestSuccessfulNoOpHasEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store := status.New(status.Options{Now: func() time.Time { return now }})
	store.RunStarted(t.Context(), "acme/app", true)
	store.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true, Targets: []plan.Target{{Name: "github"}}})
	store.RunFinished(t.Context(), "acme/app")
	if got := store.LastSuccessfulSync("acme/app", "github"); got != now {
		t.Fatalf("successful no-op was lost: %s", got)
	}
}

func TestLastSuccessfulTargetRunIgnoresLaterFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	success := now
	store := status.New(status.Options{Now: func() time.Time { return now }})
	store.RunStarted(t.Context(), "acme/app", true)
	store.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true})
	store.TargetFinished(t.Context(), "acme/app", "github", time.Second, nil)
	store.TargetFinished(t.Context(), "acme/app", "codeberg", time.Second, nil)
	store.SyncFinished(t.Context(), "acme/app", "codeberg", "labels", errors.New("failed"))
	store.RunFinished(t.Context(), "acme/app")
	now = now.Add(time.Hour)
	store.RunStarted(t.Context(), "acme/app", true)
	store.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true})
	store.TargetFinished(t.Context(), "acme/app", "github", time.Second, errors.New("failed"))
	store.RunFinished(t.Context(), "acme/app")
	if got := store.LastSuccessfulSync("acme/app", "github"); got != success {
		t.Fatalf("last successful sync=%s", got)
	}
	if got := store.LastSuccessfulSync("acme/app", "codeberg"); !got.IsZero() {
		t.Fatal("partial target failure reported as success")
	}
}
