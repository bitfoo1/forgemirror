// Browser-test fixture: real templates/HTMX, deterministic in-process jobs.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
	"go.bitfoo.de/forgemirror/internal/webhook"
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
	store   *status.Store
	jobs    *jobControl
	targets []config.Target
}

func (a actions) Refresh(_ context.Context, name string) error { a.start(name, false); return nil }
func (a actions) RefreshAll(ctx context.Context) (int, error) {
	repos := a.store.Snapshot().Repos
	for _, r := range repos {
		_ = a.Refresh(ctx, r.Name)
	}
	return len(repos), nil
}
func (a actions) Approve(context.Context, string, string, string) error { return nil }
func (a actions) Pause(ctx context.Context, name, target, reason string, until time.Time) error {
	return a.store.Pause(ctx, status.Pause{Repo: name, Target: target, Reason: reason, Until: until})
}
func (a actions) Resume(ctx context.Context, name, target string) error {
	return a.controller().Resume(ctx, name, target)
}
func (a actions) PauseScope(ctx context.Context, scope hold.Scope, reason string, until time.Time) error {
	return a.controller().PauseScope(ctx, scope, reason, until)
}
func (a actions) ResumeHold(ctx context.Context, id string) error {
	return a.controller().ResumeHold(ctx, id)
}
func (a actions) controller() *control.Controller {
	return control.New(control.Options{Store: a.store, Targets: a.targets, Dispatcher: a, Logger: slog.New(slog.DiscardHandler)})
}
func (a actions) KnownNames() []string {
	var names []string
	for _, r := range a.store.Snapshot().Repos {
		names = append(names, r.Name)
	}
	return names
}
func (a actions) Known(name string) (repo.Repository, bool) {
	for _, known := range a.KnownNames() {
		if known == name {
			owner, short, _ := strings.Cut(name, "/")
			return repo.Repository{FullName: name, Owner: owner, Name: short, CloneURL: "https://git.example/" + name + ".git"}, true
		}
	}
	return repo.Repository{}, false
}
func (a actions) Dispatch(_ context.Context, e webhook.Event) {
	selected := e.Targets
	if selected == nil && e.Target != "" {
		selected = []string{e.Target}
	}
	a.startScoped(e.Repository.FullName, true, selected)
}
func (a actions) RepairLFS(ctx context.Context, name string) error { return a.Sync(ctx, name) }
func (a actions) SyncAll(ctx context.Context) int {
	names := ui.Pending(a.store.Snapshot())
	for _, name := range names {
		_ = a.Sync(ctx, name)
	}
	return len(names)
}
func (a actions) Sync(_ context.Context, name string) error { a.start(name, true); return nil }

func (a actions) start(name string, apply bool) { a.startScoped(name, apply, nil) }
func (a actions) startScoped(name string, apply bool, selected []string) {
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
			for i, target := range current.Targets {
				chosen := selected == nil || slices.Contains(selected, target.Name)
				if _, paused := a.store.Paused(name, target.Name); chosen && !paused {
					a.store.TargetFinished(ctx, name, target.Name, time.Millisecond, nil)
					current.Targets[i] = plan.Target{Name: target.Name}
				}
			}
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
		{Name: "gitlab"},
	}}
}

func main() {
	cfg, err := config.Load(config.Options{Environ: func() []string { return nil }, Overrides: map[string]any{
		"webhook.secret": "test-secret", "ui.enabled": true, "sync.auto": false,
		"source.base_url": "https://git.example/", "source.token": "test-source",
		"targets": []map[string]any{
			{"name": "github", "url": "https://github.com/{{ .FullName }}.git", "token": "test-token"},
			{"name": "codeberg", "url": "https://codeberg.org/{{ .FullName }}.git", "token": "test-token"},
			{"name": "gitlab", "url": "https://gitlab.com/{{ .FullName }}.git", "token": "test-token"},
		},
	}})
	if err != nil {
		panic(err)
	}
	store := status.New(status.Options{})
	initial := initialFor("acme/app", false)
	if os.Getenv("FORGEMIRROR_UI_PREVIEW") == "true" {
		initial.Targets[2].Refs = []plan.RefChange{{Ref: "refs/tags/obsolete-release", Old: "4444444"}}
	}
	store.PlanFinished(context.Background(), initial)
	if os.Getenv("FORGEMIRROR_UI_PREVIEW") == "true" {
		if err = store.Pause(context.Background(), status.Pause{Target: "codeberg", Reason: "Maintenance"}); err != nil {
			panic(err)
		}
	}
	jobs := &jobControl{active: map[string]*heldJob{}}
	h, err := ui.Handler(ui.Options{Config: cfg, Store: store, Logs: logging.NewRing(10), Logger: slog.New(slog.DiscardHandler), Actions: actions{store: store, jobs: jobs, targets: cfg.Targets}, Queued: jobs.snapshot})
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
	mux.HandleFunc("POST /test/reset-controls", func(w http.ResponseWriter, r *http.Request) {
		for _, p := range store.Pauses() {
			if _, resumeErr := store.ResumeHold(r.Context(), p.ID); resumeErr != nil {
				http.Error(w, resumeErr.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", h)
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("FORGEMIRROR_UI_TEST_PORT"), Handler: mux, ReadHeaderTimeout: time.Second}
	if err = server.ListenAndServe(); err != nil {
		panic(err)
	}
}
