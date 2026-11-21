package dispatch_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/mirror"

	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestPlanOnlyEventDoesNotWidenTargetedSync(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m := &fakeMirrorer{refsHook: func(_ context.Context, r mirror.Remote) (map[string]string, error) {
		once.Do(func() { close(entered); <-release })
		return map[string]string{"refs/heads/main": r.URL}, nil
	}}
	obs := &recordingObserver{}
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      m,
			Targets:       targets(t),
			MaxConcurrent: 2,
			Logger:        slog.New(slog.DiscardHandler),
			Observer:      obs,
		},
	)
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	<-entered
	e := withKind(event("app"), webhook.EventSync)
	e.Target = "github"
	d.Dispatch(t.Context(), e)
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	close(release)
	waitRuns(t, obs, 2)
	shutdown(t, d)
	_, pushes, _, _ := m.snapshot()
	if len(pushes) != 1 || pushes[0].URL != "https://github.com/acme/app.git" {
		t.Fatalf("target scope widened: %v", pushes)
	}
}

func TestRetrySchedulerDrainsOnShutdown(t *testing.T) {
	t.Parallel()
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      &fakeMirrorer{},
			MaxConcurrent: 1,
			Logger:        slog.New(slog.DiscardHandler),
			Retries:       status.New(status.Options{}),
		},
	)
	d.StartRetries(t.Context())
	d.StartRetries(t.Context())
	shutdown(t, d)
}

func TestDueRetryReadmitsOnlyItsTarget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store := status.New(status.Options{Now: func() time.Time { return now }})
	m := &fakeMirrorer{}
	obs := &recordingObserver{}
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      m,
			Targets:       targets(t),
			MaxConcurrent: 2,
			Auto:          false,
			Logger:        slog.New(slog.DiscardHandler),
			Observer:      obs,
			Retries:       store,
			Now:           func() time.Time { return now },
		},
	)
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	waitRuns(t, obs, 1)
	waitIdle(t, d)
	if err := store.RecordRetry(t.Context(), "acme/app", "github", "sync", true, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	d.RetryDue(t.Context())
	if clones, _, _, _ := m.snapshot(); len(clones) != 0 {
		t.Fatal("automatic retry bypassed review mode")
	}
	if err := store.RecordRetry(
		t.Context(),
		"acme/app",
		"github",
		"sync",
		false,
		context.DeadlineExceeded,
	); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	d.RetryDue(t.Context())
	waitRuns(t, obs, 2)
	shutdown(t, d)
	_, pushes, _, _ := m.snapshot()
	if len(pushes) != 1 || pushes[0].URL != "https://github.com/acme/app.git" {
		t.Fatalf("pushes=%v", pushes)
	}
	if len(store.Retries()) != 0 {
		t.Fatal("successful retry not cleared")
	}
}
