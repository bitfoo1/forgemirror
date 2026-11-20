package control_test

import (
	"errors"
	"log/slog"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestApproveCurrentDestination(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(
		config.Options{
			Environ: func() []string { return nil },
			Overrides: map[string]any{
				"webhook.secret":                  "s",
				"ui.enabled":                      true,
				"sync.first_publication_approval": true,
				"targets": []map[string]any{
					{"name": "github", "url": "https://github.com/{{ .FullName }}.git", "token": "x"},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	source := repository("app")
	d := &fakeDispatcher{known: map[string]repo.Repository{"acme/app": source}}
	store := status.New(status.Options{})
	c := control.New(
		control.Options{
			Store:                    store,
			Targets:                  cfg.Targets,
			FirstPublicationApproval: true,
			Dispatcher:               d,
			Logger:                   slog.New(slog.DiscardHandler),
		},
	)
	if err = c.Approve(t.Context(), "acme/app", "github", "stale"); !errors.Is(err, control.ErrStaleApproval) {
		t.Fatalf("stale mapping approved: %v", err)
	}
	id := status.PublicationID(source.CloneURL, "https://github.com/acme/app.git")
	if err = c.Approve(t.Context(), "acme/app", "github", id); err != nil {
		t.Fatal(err)
	}
	if !store.Approved("acme/app", "github", id) || len(d.events) != 1 || d.events[0].Target != "github" ||
		d.events[0].Kind != webhook.EventSync {
		t.Fatal("approval did not persist and request fresh target sync")
	}
}
