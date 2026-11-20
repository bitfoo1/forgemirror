package status_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestPausePersistence(t *testing.T) {
	t.Parallel()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	s := status.New(status.Options{ControlPersistence: db.Namespace("controls")})
	if err = s.Pause(t.Context(), status.Pause{Target: "github", Reason: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	restored := status.New(status.Options{Persistence: db, ControlPersistence: db.Namespace("controls")})
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if pause, ok := restored.Paused("acme/app", "github"); !ok || pause.Reason != "maintenance" {
		t.Fatal("pause did not survive restore")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = restored.Resume(t.Context(), "", "github"); err == nil {
		t.Fatal("acknowledged failed persistence")
	}
	if _, ok := restored.Paused("acme/app", "github"); !ok {
		t.Fatal("failed resume removed pause")
	}
}

func TestPauseWaitsForMutation(t *testing.T) {
	t.Parallel()
	s := status.New(status.Options{})
	release, err := s.BeginMutation("acme/app", "github")
	if err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan error, 1)
	go func() { close(started); finished <- s.Pause(t.Context(), status.Pause{Target: "github"}) }()
	<-started
	select {
	case err = <-finished:
		t.Fatalf("pause acknowledged while mutation active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not complete")
	}
	if permit, permitErr := s.BeginMutation("acme/app", "github"); !errors.Is(permitErr, status.ErrPaused) {
		if permit != nil {
			permit()
		}
		t.Fatal("new mutation admitted")
	}
}

func TestPauseScopeAndExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s := status.New(status.Options{Now: func() time.Time { return now }})
	if err := s.Pause(
		t.Context(),
		status.Pause{Repo: "acme/app", Target: "github", Reason: "maintenance", Until: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		repo, target string
		blocked      bool
	}{
		{"acme/app", "github", true}, {"acme/other", "github", false}, {"acme/app", "codeberg", false},
	} {
		release, err := s.BeginMutation(tt.repo, tt.target)
		if release != nil {
			release()
		}
		if errors.Is(err, status.ErrPaused) != tt.blocked {
			t.Errorf("%s/%s: %v", tt.repo, tt.target, err)
		}
	}
	now = now.Add(2 * time.Hour)
	release, err := s.BeginMutation("acme/app", "github")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err = s.Pause(t.Context(), status.Pause{Target: "github"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Resume(t.Context(), "acme/app", "github"); err != nil {
		t.Fatal(err)
	}
	if release, err = s.BeginMutation("acme/app", "github"); !errors.Is(err, status.ErrPaused) {
		if release != nil {
			release()
		}
		t.Fatalf("global pause bypassed: %v", err)
	}
}
