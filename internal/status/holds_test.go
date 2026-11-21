package status_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestIndependentHolds(t *testing.T) {
	t.Parallel()
	s := status.New(status.Options{})
	broad, err := s.AddHold(t.Context(), hold.Scope{AllTargets: true, AllRepositories: true}, "global", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := s.AddHold(
		t.Context(),
		hold.Scope{Targets: []string{"github"}, Repositories: []string{"acme/*"}},
		"maintenance",
		time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if broad.ID == "" || narrow.ID == "" || broad.ID == narrow.ID {
		t.Fatal("holds need independent IDs")
	}
	if _, err = s.ResumeHold(t.Context(), narrow.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Paused("acme/future", "new-target"); !ok {
		t.Fatal("narrow resume bypassed global hold")
	}
	if err = s.Resume(t.Context(), "", "github"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Paused("acme/app", "github"); !ok {
		t.Fatal("legacy resume removed independent hold")
	}
	broad.Scope.Targets = []string{"evil"}
	if _, err = s.ResumeHold(t.Context(), broad.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Pauses()) != 0 {
		t.Fatal("resume retained hold")
	}
	if _, err = s.ResumeHold(t.Context(), broad.ID); !errors.Is(err, status.ErrUnknownHold) {
		t.Fatalf("unknown ID: %v", err)
	}
}

func TestHoldPersistenceAndLegacyRestore(t *testing.T) {
	t.Parallel()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	controls := db.Namespace("controls")
	if err = controls.Save(
		t.Context(),
		"controls",
		[]byte(`{"version":3,"pauses":[{"repo":"","target":"github","reason":"legacy"}]}`),
	); err != nil {
		t.Fatal(err)
	}
	s := status.New(status.Options{Persistence: db, ControlPersistence: controls})
	if err = s.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	legacyID := s.Pauses()[0].ID
	if legacyID == "" {
		t.Fatal("legacy hold missing ID")
	}
	added, err := s.AddHold(
		t.Context(),
		hold.Scope{AllTargets: true, Repositories: []string{"acme/*"}},
		"new",
		time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	restored := status.New(status.Options{Persistence: db, ControlPersistence: controls})
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if restored.Pauses()[0].ID != legacyID {
		t.Fatal("legacy ID changed")
	}
	if _, err = restored.ResumeHold(t.Context(), added.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Paused("acme/app", "github"); !ok {
		t.Fatal("new resume removed legacy hold")
	}
	if err = restored.Resume(t.Context(), "", "github"); err != nil {
		t.Fatal(err)
	}
	if len(restored.Pauses()) != 0 {
		t.Fatal("legacy resume failed")
	}
}

func TestHoldPersistenceFailure(t *testing.T) {
	t.Parallel()
	p := &switchPersistence{fail: true}
	s := status.New(status.Options{ControlPersistence: p})
	if _, err := s.AddHold(
		t.Context(),
		hold.Scope{AllTargets: true, AllRepositories: true},
		"",
		time.Time{},
	); err == nil {
		t.Fatal("failed save acknowledged")
	}
	if len(s.Pauses()) != 0 {
		t.Fatal("failed save changed controls")
	}
	p.fail = false
	added, err := s.AddHold(t.Context(), hold.Scope{AllTargets: true, AllRepositories: true}, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	p.fail = true
	if _, err = s.ResumeHold(t.Context(), added.ID); err == nil {
		t.Fatal("failed resume acknowledged")
	}
	if _, ok := s.Paused("acme/app", "github"); !ok {
		t.Fatal("failed resume removed hold")
	}
}

func TestScopedHoldWaitsForPublication(t *testing.T) {
	t.Parallel()
	s := status.New(status.Options{})
	release, err := s.BeginMutation("acme/app", "github")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, addErr := s.AddHold(
			t.Context(),
			hold.Scope{AllTargets: true, Repositories: []string{"acme/*"}},
			"",
			time.Time{},
		)
		done <- addErr
	}()
	select {
	case err = <-done:
		t.Fatalf("acknowledged active publication: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if permit, beginErr := s.BeginMutation("acme/new", "gitlab"); !errors.Is(beginErr, status.ErrPaused) {
		if permit != nil {
			permit()
		}
		t.Fatalf("future match not blocked: %v", beginErr)
	}
}
