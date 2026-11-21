package status_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestHoldScopeCopies(t *testing.T) {
	t.Parallel()
	s := status.New(status.Options{})
	p, err := s.AddHold(
		t.Context(),
		hold.Scope{Targets: []string{"github"}, Repositories: []string{"acme/*"}},
		"",
		time.Time{},
	)
	if err != nil {
		t.Fatal(err)
	}
	p.Scope.Targets[0] = "evil"
	listed := s.Pauses()
	listed[0].Scope.Repositories[0] = "evil/*"
	queried, ok := s.Hold(p.ID)
	if !ok {
		t.Fatal("missing ID")
	}
	queried.Scope.Targets[0] = "evil"
	if _, ok = s.Paused("acme/app", "github"); !ok {
		t.Fatal("caller mutated persisted hold")
	}
	if _, ok = s.Paused("evil/app", "evil"); ok {
		t.Fatal("caller widened hold")
	}
}

func TestHoldCountBoundAndExpiredPruning(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := status.New(status.Options{Now: func() time.Time { return now }})
	for range hold.MaxHolds {
		if _, err := s.AddHold(
			t.Context(),
			hold.Scope{AllTargets: true, AllRepositories: true},
			"",
			now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddHold(
		t.Context(),
		hold.Scope{AllTargets: true, AllRepositories: true},
		"",
		time.Time{},
	); !errors.Is(
		err,
		hold.ErrInvalid,
	) {
		t.Fatalf("hold count unbounded: %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := s.AddHold(
		t.Context(),
		hold.Scope{AllTargets: true, AllRepositories: true},
		"",
		time.Time{},
	); err != nil {
		t.Fatal(err)
	}
	if len(s.Pauses()) != 1 {
		t.Fatal("expired holds retained as current")
	}
}

func TestScopedHoldReasonRedactedBeforePersistence(t *testing.T) {
	t.Parallel()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	controls := db.Namespace("controls")
	s := status.New(status.Options{ControlPersistence: controls, Redact: state.Redactor([]string{"token-secret"})})
	if _, err = s.AddHold(
		t.Context(),
		hold.Scope{AllTargets: true, AllRepositories: true},
		"maintenance token-secret",
		time.Time{},
	); err != nil {
		t.Fatal(err)
	}
	records, err := controls.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(records["controls"], []byte("token-secret")) ||
		!bytes.Contains(records["controls"], []byte("[REDACTED]")) {
		t.Fatal("reason not redacted before durable save")
	}
}

func TestInvalidPersistedHoldsFailClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, data string }{
		{"unknown version", `{"version":99}`},
		{"empty scope", `{"version":4,"pauses":[{"id":"hold1","scope":{}}]}`},
		{"invalid glob", `{"version":4,"pauses":[{"id":"hold1","scope":{"all_targets":true,"repositories":["acme/[a-z]"]}}]}`},
		{"duplicate IDs", `{"version":4,"pauses":[{"id":"same","scope":{"all_targets":true,"all_repositories":true}},{"id":"same","scope":{"all_targets":true,"all_repositories":true}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			controls := db.Namespace("controls")
			if err = controls.Save(t.Context(), "controls", []byte(tc.data)); err != nil {
				t.Fatal(err)
			}
			s := status.New(status.Options{Persistence: db, ControlPersistence: controls})
			if err = s.Restore(t.Context()); err == nil {
				t.Fatal("corrupt controls admitted")
			}
		})
	}
}
