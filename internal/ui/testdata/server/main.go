// Browser-test fixture: real templates/HTMX, deterministic in-process jobs.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
)

type heldJob struct {
	release, finished chan struct{}
	once              sync.Once
}
type jobControl struct {
	mu     sync.Mutex
	hold   bool
	active map[string]*heldJob
}

func (j *jobControl) snapshot() map[string]bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make(map[string]bool, len(j.active))
	for name := range j.active {
		out[name] = false
	}
	return out
}

func (j *jobControl) complete(name string) {
	j.mu.Lock()
	var jobs []*heldJob
	for repo, job := range j.active {
		if name == "" || repo == name {
			job.once.Do(func() { close(job.release) })
			jobs = append(jobs, job)
		}
	}
	j.mu.Unlock()
	for _, job := range jobs {
		<-job.finished
	}
}

type actions struct {
	store *status.Store
	jobs  *jobControl
}

func (a actions) Refresh(_ context.Context, name string) error { a.start(name, false); return nil }
func (a actions) RefreshAll(ctx context.Context) (int, error) {
	repos := a.store.Snapshot().Repos
	for _, r := range repos {
		_ = a.Refresh(ctx, r.Name)
	}
	return len(repos), nil
}
func (a actions) Approve(context.Context, string, string, string) error          { return nil }
func (a actions) Pause(context.Context, string, string, string, time.Time) error { return nil }
func (a actions) Resume(context.Context, string, string) error                   { return nil }
func (a actions) RepairLFS(ctx context.Context, name string) error               { return a.Sync(ctx, name) }
func (a actions) SyncAll(ctx context.Context) int {
	names := ui.Pending(a.store.Snapshot())
	for _, name := range names {
		_ = a.Sync(ctx, name)
	}
	return len(names)
}
func (a actions) Sync(_ context.Context, name string) error { a.start(name, true); return nil }

func (a actions) start(name string, apply bool) {
	a.jobs.mu.Lock()
	if _, busy := a.jobs.active[name]; busy {
		a.jobs.mu.Unlock()
		return
	}
	job := &heldJob{release: make(chan struct{}), finished: make(chan struct{})}
	hold := a.jobs.hold
	a.jobs.active[name] = job
	a.jobs.mu.Unlock()
	ctx := context.Background()
	a.store.RunStarted(ctx, name, apply)
	if apply {
		a.store.PlanFinished(ctx, initialFor(name, true))
	}
	go func() {
		defer close(job.finished)
		if hold {
			<-job.release
		} else {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-job.release:
			}
			timer.Stop()
		}
		current := initialFor(name, false)
		if apply {
			a.store.TargetFinished(ctx, name, "github", time.Millisecond, nil)
			current.Targets = []plan.Target{{Name: "github"}, {Name: "codeberg"}}
		}
		a.store.PlanFinished(ctx, current)
		a.store.RunFinished(ctx, name)
		a.jobs.mu.Lock()
		delete(a.jobs.active, name)
		a.jobs.mu.Unlock()
	}()
}

func initialFor(name string, apply bool) plan.Repo {
	return plan.Repo{Name: name, At: time.Now(), Apply: apply, Targets: []plan.Target{
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
	store.PlanFinished(context.Background(), initialFor("acme/app", false))
	jobs := &jobControl{active: map[string]*heldJob{}}
	h, err := ui.Handler(ui.Options{Config: cfg, Store: store, Logs: logging.NewRing(10), Logger: slog.New(slog.DiscardHandler), Actions: actions{store: store, jobs: jobs}, Queued: jobs.snapshot})
	if err != nil {
		panic(err)
	}
	// Test controls are outside the production handler and cannot be shipped routes.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /test/hold", func(w http.ResponseWriter, r *http.Request) {
		jobs.mu.Lock()
		jobs.hold = r.URL.Query().Get("enabled") != "false"
		jobs.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /test/complete", func(w http.ResponseWriter, r *http.Request) {
		jobs.complete(r.URL.Query().Get("repo"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /test/seed", func(w http.ResponseWriter, r *http.Request) {
		store.PlanFinished(r.Context(), initialFor(r.URL.Query().Get("repo"), false))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", h)
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("FORGEMIRROR_UI_TEST_PORT"), Handler: mux, ReadHeaderTimeout: time.Second}
	if err = server.ListenAndServe(); err != nil {
		panic(err)
	}
}
