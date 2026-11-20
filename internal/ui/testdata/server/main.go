// Browser-test fixture: real templates/HTMX, deterministic in-process syncs.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
)

type actions struct{ store *status.Store }

func (a actions) Refresh(context.Context, string) error   { return nil }
func (a actions) RefreshAll(context.Context) (int, error) { return 1, nil }
func (a actions) Pause(context.Context,string,string,string,time.Time) error { return nil }
func (a actions) Resume(context.Context,string,string) error { return nil }
func (a actions) RepairLFS(ctx context.Context, name string) error { return a.Sync(ctx, name) }
func (a actions) SyncAll(ctx context.Context) int         { _ = a.Sync(ctx, "acme/app"); return 1 }
func (a actions) Sync(_ context.Context, name string) error {
	ctx := context.Background()
	a.store.RunStarted(ctx, name, true)
	a.store.PlanFinished(ctx, initial(true))
	go func() {
		time.Sleep(100 * time.Millisecond)
		a.store.TargetFinished(ctx, name, "github", time.Millisecond, nil)
		a.store.PlanFinished(ctx, plan.Repo{Name: name, At: time.Now(), Targets: []plan.Target{{Name: "github"}, {Name: "codeberg"}}})
		a.store.RunFinished(ctx, name)
	}()
	return nil
}
func initial(apply bool) plan.Repo {
	return plan.Repo{Name: "acme/app", At: time.Now(), Apply: apply, Targets: []plan.Target{
		{Name: "github", Refs: []plan.RefChange{{Ref: "refs/heads/main", Old: "1111111", New: "2222222"}}},
		{Name: "codeberg", Refs: []plan.RefChange{{Ref: "refs/heads/main", Old: "1111111", New: "2222222"}}},
	}}
}
func main() {
	cfg, err := config.Load(config.Options{Environ: func() []string { return nil }, Overrides: map[string]any{
		"webhook.secret": "test-secret", "ui.enabled": true, "sync.auto": false,
		"source.base_url": "https://git.example/", "source.token": "test-source",
		"targets": []map[string]any{{"name": "github", "url": "https://github.com/{{ .FullName }}.git", "token": "test-token"}},
	}})
	if err != nil {
		panic(err)
	}
	store := status.New(status.Options{})
	store.PlanFinished(context.Background(), initial(false))
	ring := logging.NewRing(10)
	logger := slog.New(slog.DiscardHandler)
	h, err := ui.Handler(ui.Options{Config: cfg, Store: store, Logs: ring, Logger: logger, Actions: actions{store: store}})
	if err != nil {
		panic(err)
	}
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("FORGEMIRROR_UI_TEST_PORT"), Handler: h, ReadHeaderTimeout: time.Second}
	if err = server.ListenAndServe(); err != nil {
		panic(err)
	}
}
