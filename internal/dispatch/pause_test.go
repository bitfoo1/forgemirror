package dispatch_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestPausePreventsPublication(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	if err := store.Pause(t.Context(), status.Pause{Target: "github"}); err != nil {
		t.Fatal(err)
	}
	m := &fakeMirrorer{pushLFSHook: func(_ context.Context, r mirror.Remote) error {
		if strings.Contains(r.URL, "github.com") {
			t.Error("paused LFS uploaded")
		}
		return nil
	}}
	obs := &recordingObserver{}
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      m,
			Targets:       targets(t),
			MaxConcurrent: 2,
			Auto:          true,
			Logger:        slog.New(slog.DiscardHandler),
			Observer:      obs,
			Mutation:      store.BeginMutation,
		},
	)
	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)
	_, pushes, _, _ := m.snapshot()
	if len(pushes) != 1 || !strings.Contains(pushes[0].URL, "codeberg.org") {
		t.Fatalf("pushes=%v", pushes)
	}
	if len(obs.planSnapshot()) != 1 {
		t.Fatal("pause prevented read-only planning")
	}
}

func TestPausePreventsTargetCreation(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	if err := store.Pause(t.Context(), status.Pause{Target: "github"}); err != nil {
		t.Fatal(err)
	}
	gh := &fakeForge{missing: true}
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      &fakeMirrorer{},
			Targets:       loadTargets(t, githubTarget),
			ForgeSource:   sourceRepo(),
			Forges:        map[string]forge.Target{"github": gh},
			MaxConcurrent: 2,
			Auto:          true,
			Logger:        slog.New(slog.DiscardHandler),
			Mutation:      store.BeginMutation,
		},
	)
	e := event("app")
	e.Kind = webhook.EventSync
	d.Dispatch(t.Context(), e)
	shutdown(t, d)
	for _, call := range gh.snapshot() {
		if call != "Get" && call != "Labels" {
			t.Errorf("paused mutation: %s", call)
		}
	}
}
