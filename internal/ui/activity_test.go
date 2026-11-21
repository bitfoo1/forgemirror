package ui_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
)

func TestActivityIndicators(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                               string
		running, applying, finished, mixed bool
		queued                             map[string]bool
		phase, summary                     string
	}{
		{name: "idle"},
		{name: "refresh", running: true, phase: "refreshing", summary: "Refreshing 1 repository"},
		{name: "sync", running: true, applying: true, phase: "syncing", summary: "Syncing 1 repository"},
		{name: "dispatcher handoff", queued: map[string]bool{"acme/app": false}, phase: "starting", summary: "Starting 1 repository"},
		{name: "deduplicated", running: true, applying: true, queued: map[string]bool{"acme/app": true}, phase: "syncing", summary: "Syncing 1 repository"},
		{name: "failed completion", running: true, applying: true, finished: true},
		{name: "mixed", running: true, mixed: true, phase: "refreshing", summary: "2 repositories active"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixtureWithQueued(t, nil, nil, func() map[string]bool { return tt.queued })
			f.store.PlanFinished(t.Context(), plan.Repo{Name: "acme/app"})
			if tt.running {
				f.store.RunStarted(t.Context(), "acme/app", "", tt.applying)
			}
			if tt.finished {
				f.store.TargetFinished(t.Context(), "acme/app", "github", time.Second, errors.New("failed"))
				f.store.RunFinished(t.Context(), "acme/app")
			}
			if tt.mixed {
				f.store.RunStarted(t.Context(), "acme/other", "", true)
			}
			response := f.do(t, http.MethodGet, "/fragments/repos")
			if response.Code != http.StatusOK {
				t.Fatal(response.Code)
			}
			assertActivity(t, response.Body.String(), tt.phase, tt.summary)
		})
	}
}

func assertActivity(t *testing.T, body, phase, summary string) {
	t.Helper()
	if phase == "" {
		if strings.Contains(body, "data-repository-activity") || strings.Contains(body, "data-global-activity") {
			t.Fatal("idle or completed work still spins")
		}
		return
	}
	if !strings.Contains(body, `data-activity="`+phase+`"`) {
		t.Fatalf("missing %s repository indicator", phase)
	}
	if !strings.Contains(body, "data-global-activity") || !strings.Contains(body, summary) {
		t.Fatalf("missing aggregate activity %q", summary)
	}
}
