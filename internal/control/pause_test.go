package control_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestPauseAndResumeScope(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{known: map[string]repo.Repository{"acme/app": repository("app")}}
	store := status.New(status.Options{})
	c := control.New(
		control.Options{
			Store:      store,
			Targets:    []config.Target{{Name: "github"}},
			Dispatcher: d,
			Logger:     slog.New(slog.DiscardHandler),
		},
	)
	if err := c.Pause(
		t.Context(),
		"evil/repo",
		"github",
		"",
		time.Time{},
	); !errors.Is(
		err,
		control.ErrUnknownRepository,
	) {
		t.Fatalf("unknown repo: %v", err)
	}
	if err := c.Pause(t.Context(), "", "evil", "", time.Time{}); !errors.Is(err, control.ErrUnknownTarget) {
		t.Fatalf("unknown target: %v", err)
	}
	if err := c.Pause(t.Context(), "", "github", "maintenance", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Resume(t.Context(), "", "github"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Paused("acme/app", "github"); ok {
		t.Fatal("resume retained hold")
	}
	if len(d.events) != 1 || d.events[0].Kind != webhook.EventSync || d.events[0].Target != "github" {
		t.Fatalf("resume event=%+v", d.events)
	}
}
