package control_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestScopedHoldController(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	d := &fakeDispatcher{
		known: map[string]repo.Repository{"acme/app": repository("app"), "acme/other": repository("other")},
	}
	c := control.New(
		control.Options{
			Store:      store,
			Targets:    []config.Target{{Name: "github"}, {Name: "codeberg"}},
			Dispatcher: d,
			Logger:     slog.New(slog.DiscardHandler),
		},
	)
	for _, scope := range []hold.Scope{{AllTargets: true, Repositories: []string{"acme/app", "evil/repo"}}, {Targets: []string{"github", "evil"}, AllRepositories: true}, {AllTargets: true, Repositories: []string{"missing/*"}}} {
		if err := c.PauseScope(t.Context(), scope, "", time.Time{}); err == nil {
			t.Fatal("invalid scope admitted")
		}
		if len(store.Pauses()) != 0 {
			t.Fatal("partial hold saved")
		}
	}
	if err := c.PauseScope(
		t.Context(),
		hold.Scope{AllTargets: true, Repositories: []string{"acme/*"}},
		"maintenance",
		time.Time{},
	); err != nil {
		t.Fatal(err)
	}
	id := store.Pauses()[0].ID
	if err := store.Pause(t.Context(), status.Pause{Target: "codeberg"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ResumeHold(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if len(d.events) != 2 {
		t.Fatalf("events=%+v", d.events)
	}
	for _, event := range d.events {
		if event.Kind != webhook.EventSync || len(event.Targets) != 2 || event.Targets[0] != "github" ||
			event.Targets[1] != "codeberg" {
			t.Errorf("event=%+v", event)
		}
	}
	if _, ok := store.Paused("acme/app", "codeberg"); !ok {
		t.Fatal("resume removed another hold")
	}
	if err := c.ResumeHold(t.Context(), id); !errors.Is(err, status.ErrUnknownHold) {
		t.Fatalf("stale ID=%v", err)
	}
}

func TestAllRepositoryHoldBeforeDiscovery(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	d := &fakeDispatcher{known: map[string]repo.Repository{}}
	c := control.New(
		control.Options{
			Store:      store,
			Targets:    []config.Target{{Name: "github"}},
			Dispatcher: d,
			Logger:     slog.New(slog.DiscardHandler),
		},
	)
	if err := c.PauseScope(
		t.Context(),
		hold.Scope{AllTargets: true, AllRepositories: true},
		"",
		time.Time{},
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Paused("future/repo", "new-target"); !ok {
		t.Fatal("undiscovered publication was not held")
	}
}

func TestResumeHoldRejectsCloneBeforeRemovingHold(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	d := &fakeDispatcher{known: map[string]repo.Repository{"acme/app": repository("app")}}
	c := control.New(
		control.Options{
			Store:         store,
			Targets:       []config.Target{{Name: "github"}},
			Dispatcher:    d,
			Logger:        slog.New(slog.DiscardHandler),
			CheckCloneURL: func(string) error { return errors.New("rejected") },
		},
	)
	if err := c.PauseScope(
		t.Context(),
		hold.Scope{AllTargets: true, AllRepositories: true},
		"",
		time.Time{},
	); err != nil {
		t.Fatal(err)
	}
	if err := c.ResumeHold(t.Context(), store.Pauses()[0].ID); err == nil {
		t.Fatal("clone URL check bypassed")
	}
	if len(store.Pauses()) != 1 || len(d.events) != 0 {
		t.Fatal("rejected resume changed hold or dispatched work")
	}
}
