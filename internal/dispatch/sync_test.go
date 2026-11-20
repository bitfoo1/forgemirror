package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// gauge records the highest number of forge API calls in flight at once. A
// nil gauge records nothing.
type gauge struct {
	mu      sync.Mutex
	active  int
	maxSeen int
}

// hold occupies one slot briefly, so overlapping calls actually overlap.
func (g *gauge) hold() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.active++
	g.maxSeen = max(g.maxSeen, g.active)
	g.mu.Unlock()

	time.Sleep(time.Millisecond)

	g.mu.Lock()
	g.active--
	g.mu.Unlock()
}

func (g *gauge) max() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.maxSeen
}

// fakeSource is a forge.Source serving one repository.
type fakeSource struct {
	repo   forge.Repo
	labels []forge.Label
	err    error
	calls  atomic.Int32
	gauge  *gauge
}

func (s *fakeSource) Repo(context.Context, string, string) (forge.Repo, error) {
	s.calls.Add(1)
	s.gauge.hold()

	return s.repo, s.err
}

func (s *fakeSource) Labels(context.Context, string, string) ([]forge.Label, error) {
	s.calls.Add(1)
	s.gauge.hold()

	return slices.Clone(s.labels), s.err
}

func (s *fakeSource) Repos(context.Context) ([]repo.Repository, error) {
	s.calls.Add(1)

	return nil, s.err
}

// fakeForge is a forge.Target recording its calls in order. "push" is added
// by the mirrorer's push hook, so the order covers git and API steps.
type fakeForge struct {
	mu        sync.Mutex
	calls     []string
	creates   []forge.Repo
	updates   []forge.Settings
	labelOps  []string
	missing   bool
	repo      forge.Repo
	labels    []forge.Label
	deleteErr error
	// regetErr is returned by Get once the repository was created.
	regetErr error
	created  bool
	caps     forge.Capabilities
	// updateErr is returned by Update.
	updateErr error
	gauge     *gauge
}

func (f *fakeForge) record(call string) {
	if call != "push" {
		f.gauge.hold()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeForge) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func (f *fakeForge) Get(context.Context, forge.Address) (forge.Repo, error) {
	f.record("Get")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return forge.Repo{}, fmt.Errorf("get: %w", forge.ErrNotFound)
	}
	if f.created && f.regetErr != nil {
		return forge.Repo{}, f.regetErr
	}

	return f.repo, nil
}

func (f *fakeForge) Create(_ context.Context, _ forge.Address, r forge.Repo) error {
	f.record("Create")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, r)
	// Like the real adapters, creation applies only a subset of the settings.
	f.missing, f.created = false, true
	f.repo = forge.Repo{Private: r.Private, Description: r.Description}

	return nil
}

func (f *fakeForge) Update(_ context.Context, _ forge.Address, s forge.Settings) error {
	f.record("Update")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, s)

	return f.updateErr
}

func (f *fakeForge) Labels(context.Context, forge.Address) ([]forge.Label, error) {
	f.record("Labels")
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.labels), nil
}

func (f *fakeForge) CreateLabel(_ context.Context, _ forge.Address, l forge.Label) error {
	f.record("CreateLabel")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labelOps = append(f.labelOps, "create "+l.Name)

	return nil
}

func (f *fakeForge) UpdateLabel(_ context.Context, _ forge.Address, l forge.Label) error {
	f.record("UpdateLabel")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labelOps = append(f.labelOps, "update "+l.Name)

	return nil
}

func (f *fakeForge) DeleteLabel(_ context.Context, _ forge.Address, name string) error {
	f.record("DeleteLabel")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labelOps = append(f.labelOps, "delete "+name)

	return nil
}

func (f *fakeForge) Delete(context.Context, forge.Address) error {
	f.record("Delete")

	return f.deleteErr
}

func (f *fakeForge) Capabilities() forge.Capabilities { return f.caps }

const sourceYAML = `
webhook:
  secret: s
source:
  base_url: https://git.example
  username: oauth2
  token: src-token
`

// loadTargets validates targets through the real config package.
func loadTargets(t *testing.T, targetsYAML string) []config.Target {
	t.Helper()

	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(sourceYAML+"targets:\n"+targetsYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{File: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}

	return cfg.Targets
}

const (
	// markedDescription is the description of sourceRepo with the default
	// mirror note, i.e. what an up-to-date target carries.
	markedDescription = "the app · " + forge.DefaultMirrorNote

	githubTarget = `
  - name: github
    type: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: gh-token
    create: true
`
	gitlabTarget = `
  - name: gitlab
    type: gitlab
    url: "https://gitlab.com/acme/{{ .Name }}.git"
    token: gl-token
`
	codebergTarget = `
  - name: codeberg
    url: "https://codeberg.org/{{ .FullName }}.git"
    token: cb-token
`
)

// pushRecorder makes each push visible in the fake forge owning the URL.
func pushRecorder(forges map[string]*fakeForge, fail map[string]error) func(context.Context, mirror.Remote) error {
	return func(_ context.Context, dst mirror.Remote) error {
		for name, f := range forges {
			if strings.Contains(dst.URL, name+".com") {
				f.record("push")
			}
		}
		for host, err := range fail {
			if strings.Contains(dst.URL, host) {
				return err
			}
		}

		return nil
	}
}

func newSyncDispatcher(
	t *testing.T,
	targetsYAML string,
	m *fakeMirrorer,
	src *fakeSource,
	forges map[string]*fakeForge,
) (*dispatch.Dispatcher, *recordingObserver) {
	t.Helper()

	return newSyncDispatcherWithLogger(t, targetsYAML, m, src, forges, slog.New(slog.DiscardHandler))
}

func newSyncDispatcherWithLogger(
	t *testing.T,
	targetsYAML string,
	m *fakeMirrorer,
	src *fakeSource,
	forges map[string]*fakeForge,
	logger *slog.Logger,
) (*dispatch.Dispatcher, *recordingObserver) {
	t.Helper()

	obs := &recordingObserver{}
	targets := make(map[string]forge.Target, len(forges))
	for name, f := range forges {
		targets[name] = f
	}
	opts := dispatch.Options{
		Mirrorer:      m,
		Source:        config.Source{Username: "oauth2", Token: "src-token"},
		Targets:       loadTargets(t, targetsYAML),
		MaxConcurrent: 4,
		Auto:          true,
		Logger:        logger,
		Observer:      obs,
		Forges:        targets,
	}
	if src != nil {
		opts.ForgeSource = src
	}

	return dispatch.New(opts), obs
}

func sourceRepo() *fakeSource {
	return &fakeSource{
		repo: forge.Repo{
			Description: "the app", DefaultBranch: "main", Topics: []string{"Go"},
			HasIssues: true,
		},
		labels: []forge.Label{{Name: "bug", Color: "ff0000"}},
	}
}

func TestRunOrderCreatePushSettingsLabels(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{missing: true}
	forges := map[string]*fakeForge{"github": gh}
	m := &fakeMirrorer{pushHook: pushRecorder(forges, nil)}
	src := sourceRepo()
	src.repo.HasWiki = true
	d, obs := newSyncDispatcher(t, githubTarget, m, src, forges)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	want := []string{"Get", "Create", "push", "Get", "Update", "Labels", "CreateLabel"}
	if got := gh.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if gh.creates[0].DefaultBranch != "" {
		t.Errorf("Create DefaultBranch = %q, want empty", gh.creates[0].DefaultBranch)
	}
	// The fake's Create stores only Private and Description, so everything
	// else must come from the re-read after the push.
	upd := gh.updates[0]
	if upd.DefaultBranch == nil || *upd.DefaultBranch != "main" {
		t.Errorf("Update DefaultBranch = %v, want main", upd.DefaultBranch)
	}
	if upd.HasIssues == nil || !*upd.HasIssues || upd.HasWiki == nil || !*upd.HasWiki {
		t.Errorf("Update HasIssues/HasWiki = %v/%v, want true/true", upd.HasIssues, upd.HasWiki)
	}
	if upd.Description != nil {
		t.Errorf("Update Description = %q, want unchanged (set by Create)", *upd.Description)
	}

	_, syncs := obs.snapshot()
	ops := make([]string, 0, len(syncs))
	for _, s := range syncs {
		if s.err != nil || s.target != "github" {
			t.Errorf("sync %+v, want success for github", s)
		}
		ops = append(ops, s.operation)
	}
	if wantOps := []string{"create", "lfs", "settings", "labels"}; !slices.Equal(ops, wantOps) {
		t.Errorf("sync operations = %v, want %v", ops, wantOps)
	}
}

func TestRunRegetFailureFailsSettings(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{missing: true, regetErr: errors.New("api down")}
	d, obs := newSyncDispatcher(t, githubTarget, &fakeMirrorer{}, sourceRepo(), map[string]*fakeForge{"github": gh})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if slices.Contains(gh.snapshot(), "Update") {
		t.Errorf("calls = %v, want no Update after a failed re-read", gh.snapshot())
	}
	_, syncs := obs.snapshot()
	var settingsErr error
	for _, s := range syncs {
		if s.operation == "settings" {
			settingsErr = s.err
		}
	}
	if settingsErr == nil || !strings.Contains(settingsErr.Error(), "api down") {
		t.Errorf("settings result = %v, want the re-read error", settingsErr)
	}
}

func TestRunCreatesPrivateRepoPrivate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		private     bool
		wantPrivate bool
	}{
		{name: "private source", private: true, wantPrivate: true},
		{name: "public source", private: false, wantPrivate: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := sourceRepo()
			src.repo.Private = tc.private
			gh := &fakeForge{missing: true}
			d, _ := newSyncDispatcher(t, githubTarget, &fakeMirrorer{}, src, map[string]*fakeForge{"github": gh})

			d.Dispatch(t.Context(), event("app"))
			shutdown(t, d)

			if len(gh.creates) != 1 || gh.creates[0].Private != tc.wantPrivate {
				t.Errorf("creates = %+v, want one with Private=%v", gh.creates, tc.wantPrivate)
			}
		})
	}
}

func TestRunMissingRepoWithoutCreate(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{repo: forge.Repo{DefaultBranch: "main"}}
	gl := &fakeForge{missing: true}
	forges := map[string]*fakeForge{"github": gh, "gitlab": gl}
	m := &fakeMirrorer{pushHook: pushRecorder(forges, nil)}
	d, obs := newSyncDispatcher(t, githubTarget+gitlabTarget, m, sourceRepo(), forges)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if got := gl.snapshot(); !slices.Equal(got, []string{"Get"}) {
		t.Errorf("gitlab calls = %v, want only Get", got)
	}
	if got := gh.snapshot(); !slices.Contains(got, "push") || !slices.Contains(got, "Update") {
		t.Errorf("github calls = %v, want push and Update", got)
	}

	results, _ := obs.snapshot()
	byTarget := map[string]error{}
	for _, r := range results {
		byTarget[r.target] = r.err
	}
	err := byTarget["gitlab"]
	if err == nil || !strings.Contains(err.Error(), "gitlab") || !strings.Contains(err.Error(), "create is false") {
		t.Errorf("gitlab error = %v, want target name and %q", err, "create is false")
	}
	if byTarget["github"] != nil {
		t.Errorf("github error = %v, want nil", byTarget["github"])
	}
}

func TestRunPushFailureSkipsSettings(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{repo: forge.Repo{Description: "old"}}
	forges := map[string]*fakeForge{"github": gh}
	m := &fakeMirrorer{pushHook: pushRecorder(forges, map[string]error{"github.com": errors.New("rejected")})}
	d, obs := newSyncDispatcher(t, githubTarget, m, sourceRepo(), forges)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if got := gh.snapshot(); !slices.Equal(got, []string{"Get", "Labels", "push"}) {
		t.Errorf("calls = %v, want only the plan reads and the push", got)
	}
	if _, syncs := obs.snapshot(); len(syncs) != 1 || syncs[0].operation != "lfs" || syncs[0].err != nil {
		t.Errorf("sync results = %+v, want only successful LFS upload", syncs)
	}
}

func TestRunSourceFailureFailsTypedTargetsOnly(t *testing.T) {
	t.Parallel()

	src := &fakeSource{err: errors.New("source down")}
	gh := &fakeForge{}
	m := &fakeMirrorer{}
	d, obs := newSyncDispatcher(t, githubTarget+codebergTarget, m, src, map[string]*fakeForge{"github": gh})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if got := gh.snapshot(); len(got) != 0 {
		t.Errorf("github calls = %v, want none", got)
	}
	_, pushes, _, _ := m.snapshot()
	if len(pushes) != 1 || pushes[0].Token != "cb-token" {
		t.Errorf("pushes = %+v, want only codeberg", pushes)
	}
	results, _ := obs.snapshot()
	for _, r := range results {
		if r.target == "github" && (r.err == nil || !strings.Contains(r.err.Error(), "source down")) {
			t.Errorf("github error = %v, want source error", r.err)
		}
	}
}

func TestRunUntypedTargetUnchanged(t *testing.T) {
	t.Parallel()

	src := sourceRepo()
	m := &fakeMirrorer{}
	d, _ := newSyncDispatcher(t, codebergTarget, m, src, nil)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if n := src.calls.Load(); n != 0 {
		t.Errorf("source API calls = %d, want 0", n)
	}
	if clones, pushes, _, _ := m.snapshot(); len(clones) != 1 || len(pushes) != 1 {
		t.Errorf("clones = %d, pushes = %d; want 1 and 1", len(clones), len(pushes))
	}
}

func TestScheduledRunSkipsWhenInSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		targetRefs map[string]string
		wantClones int
	}{
		{name: "in sync", targetRefs: map[string]string{"refs/heads/main": "abc"}, wantClones: 0},
		{name: "behind", targetRefs: map[string]string{"refs/heads/main": "old"}, wantClones: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gh := &fakeForge{repo: forge.Repo{Description: "old"}}
			m := &fakeMirrorer{refsHook: func(_ context.Context, r mirror.Remote) (map[string]string, error) {
				if r.Token == "src-token" {
					return map[string]string{"refs/heads/main": "abc"}, nil
				}

				return tc.targetRefs, nil
			}}
			d, _ := newSyncDispatcher(t, githubTarget, m, sourceRepo(), map[string]*fakeForge{"github": gh})

			e := event("app")
			e.Kind = webhook.EventScheduled
			d.Dispatch(t.Context(), e)
			shutdown(t, d)

			if clones, _, _, _ := m.snapshot(); len(clones) != tc.wantClones {
				t.Errorf("clones = %d, want %d", len(clones), tc.wantClones)
			}
			if !slices.Contains(gh.snapshot(), "Update") {
				t.Errorf("calls = %v, want settings synced", gh.snapshot())
			}
		})
	}
}

func TestPruneLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		prune string
		want  []string
	}{
		{name: "prune", prune: "true", want: []string{"delete extra"}},
		{name: "keep", prune: "false", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gh := &fakeForge{
				repo: forge.Repo{
					Description:   markedDescription,
					DefaultBranch: "main",
					Topics:        []string{"forgemirror", "go"},
					HasIssues:     true,
				},
				labels: []forge.Label{{Name: "bug", Color: "#FF0000"}, {Name: "extra", Color: "000000"}},
			}
			d, _ := newSyncDispatcher(t, githubTarget+"    prune_labels: "+tc.prune+"\n",
				&fakeMirrorer{}, sourceRepo(), map[string]*fakeForge{"github": gh})

			d.Dispatch(t.Context(), event("app"))
			shutdown(t, d)

			if !slices.Equal(gh.labelOps, tc.want) {
				t.Errorf("label operations = %v, want %v", gh.labelOps, tc.want)
			}
			if slices.Contains(gh.snapshot(), "Update") {
				t.Errorf("calls = %v, want no Update for equal settings", gh.snapshot())
			}
		})
	}
}

func TestDeleteEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		deleteErr error
		wantErr   bool
	}{
		{name: "deleted", deleteErr: nil},
		{name: "already gone", deleteErr: fmt.Errorf("delete: %w", forge.ErrNotFound)},
		{name: "failure", deleteErr: errors.New("forbidden"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gh := &fakeForge{deleteErr: tc.deleteErr}
			gl := &fakeForge{}
			src := sourceRepo()
			m := &fakeMirrorer{}
			d, obs := newSyncDispatcher(t, githubTarget+"    delete: true\n"+gitlabTarget+codebergTarget,
				m, src, map[string]*fakeForge{"github": gh, "gitlab": gl})

			e := event("app")
			e.Kind = webhook.EventDelete
			d.Dispatch(t.Context(), e)
			shutdown(t, d)

			if got := gh.snapshot(); !slices.Equal(got, []string{"Get", "Delete"}) {
				t.Errorf("github calls = %v, want Get (plan) and Delete", got)
			}
			if got := gl.snapshot(); len(got) != 0 {
				t.Errorf("gitlab calls = %v, want none (delete: false)", got)
			}
			if clones, pushes, _, _ := m.snapshot(); len(clones)+len(pushes) != 0 {
				t.Errorf("clones = %d, pushes = %d; want none", len(clones), len(pushes))
			}
			if n := src.calls.Load(); n != 0 {
				t.Errorf("source API calls = %d, want 0", n)
			}
			_, syncs := obs.snapshot()
			if len(syncs) != 1 || syncs[0].target != "github" || syncs[0].operation != "delete" ||
				(syncs[0].err != nil) != tc.wantErr {
				t.Errorf("sync results = %+v, want one delete for github (failed=%v)", syncs, tc.wantErr)
			}
			if got := obs.observedRepos(); !slices.Equal(got, []string{"acme/app"}) {
				t.Errorf("observed repositories = %q, want [acme/app]", got)
			}
		})
	}
}

func TestQueuedDeleteWinsOverPush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		queue []webhook.EventKind
	}{
		{name: "push then delete", queue: []webhook.EventKind{webhook.EventPush, webhook.EventDelete}},
		{name: "delete then push", queue: []webhook.EventKind{webhook.EventDelete, webhook.EventPush}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})
			started := make(chan struct{}, 10)
			m := &fakeMirrorer{cloneHook: func(ctx context.Context, _ mirror.Remote) error {
				started <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			gh := &fakeForge{}
			d, _ := newSyncDispatcher(t, githubTarget+"    delete: true\n", m, sourceRepo(),
				map[string]*fakeForge{"github": gh})

			d.Dispatch(t.Context(), event("app"))
			waitFor(t, started)
			for _, kind := range tc.queue {
				e := event("app")
				e.Kind = kind
				d.Dispatch(t.Context(), e)
			}
			close(release)
			// Shutdown drops queued follow-ups, so wait for the delete first.
			waitUntil(t, func() bool { return slices.Contains(gh.snapshot(), "Delete") })
			shutdown(t, d)

			if clones, _, _, _ := m.snapshot(); len(clones) != 1 {
				t.Errorf("clones = %d, want 1 (follow-up is the delete)", len(clones))
			}
			calls := gh.snapshot()
			if len(calls) == 0 || calls[len(calls)-1] != "Delete" {
				t.Errorf("calls = %v, want the follow-up run to delete", calls)
			}
		})
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()

	for deadline := time.Now().Add(waitTimeout); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out waiting for condition")
}

func TestSweepBoundsForgeAPIConcurrency(t *testing.T) {
	t.Parallel()

	const maxConcurrent = 4 // set by newSyncDispatcher

	g := &gauge{}
	src := sourceRepo()
	src.gauge = g
	forges := map[string]*fakeForge{
		"github": {repo: forge.Repo{Description: "old"}, gauge: g},
		"gitlab": {repo: forge.Repo{Description: "old"}, gauge: g},
	}
	d, _ := newSyncDispatcher(t, githubTarget+gitlabTarget, &fakeMirrorer{}, src, forges)

	for i := range 24 {
		e := event(fmt.Sprintf("app%d", i))
		e.Kind = webhook.EventScheduled
		d.Dispatch(t.Context(), e)
	}
	shutdown(t, d)

	if got := g.max(); got < 1 || got > maxConcurrent {
		t.Errorf("forge API calls in flight at once = %d, want 1..%d", got, maxConcurrent)
	}
}

func TestRunPrivatisesBeforePush(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		private   bool
		override  string
		updateErr error
		wantCalls []string
	}{
		{
			name:      "source turned private",
			private:   true,
			wantCalls: []string{"Get", "Labels", "Update", "push"},
		},
		{
			name:      "override private",
			override:  "    settings:\n      private: true\n",
			wantCalls: []string{"Get", "Labels", "Update", "push"},
		},
		{
			name:      "privatising fails",
			private:   true,
			updateErr: errors.New("org policy"),
			wantCalls: []string{"Get", "Labels", "Update"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := sourceRepo()
			src.repo.Private = tc.private
			gh := &fakeForge{repo: forge.Repo{Description: "the app"}, updateErr: tc.updateErr}
			forges := map[string]*fakeForge{"github": gh}
			m := &fakeMirrorer{pushHook: pushRecorder(forges, nil)}
			d, obs := newSyncDispatcher(t, githubTarget+tc.override, m, src, forges)

			d.Dispatch(t.Context(), event("app"))
			shutdown(t, d)

			calls := gh.snapshot()
			if len(calls) < len(tc.wantCalls) || !slices.Equal(calls[:len(tc.wantCalls)], tc.wantCalls) {
				t.Fatalf("calls = %v, want prefix %v", calls, tc.wantCalls)
			}
			if first := gh.updates[0]; first != (forge.Settings{Private: first.Private}) ||
				first.Private == nil || !*first.Private {
				t.Errorf("first Update = %+v, want only Private=true", first)
			}

			if tc.updateErr != nil {
				assertPrivatiseFailed(t, calls, obs)
			}
		})
	}
}

func assertPrivatiseFailed(t *testing.T, calls []string, obs *recordingObserver) {
	t.Helper()

	if slices.Contains(calls, "push") {
		t.Errorf("calls = %v, want no push after a failed privatisation", calls)
	}
	results, syncs := obs.snapshot()
	if len(results) != 1 || results[0].err == nil || !strings.Contains(results[0].err.Error(), "org policy") {
		t.Errorf("target results = %+v, want one failure with the update error", results)
	}
	if len(syncs) != 1 || syncs[0].operation != "settings" || syncs[0].err == nil {
		t.Errorf("sync results = %+v, want one failed settings operation", syncs)
	}
}

// syncBuffer is an [io.Writer] safe for the concurrent writes of a run.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestUnrepresentableTopicsDroppedWithWarning(t *testing.T) {
	t.Parallel()

	src := sourceRepo()
	src.repo.Topics = []string{"Go", "node.js"}
	gh := &fakeForge{
		repo: forge.Repo{
			Description:   markedDescription,
			DefaultBranch: "main",
			Topics:        []string{"forgemirror", "go"},
			HasIssues:     true,
		},
		caps: forge.Capabilities{ValidTopic: func(topic string) bool { return !strings.Contains(topic, ".") }},
	}
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	d, _ := newSyncDispatcherWithLogger(t, githubTarget, &fakeMirrorer{}, src,
		map[string]*fakeForge{"github": gh}, logger)

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	if slices.Contains(gh.snapshot(), "Update") {
		t.Errorf("calls = %v, want no Update: the only difference cannot be stored", gh.snapshot())
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "node.js") {
		t.Errorf("logs = %q, want a warning naming node.js", out)
	}
}

func TestMirrorMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		settings   string
		wantDesc   string
		wantTopics []string
	}{
		{
			name:       "default",
			wantDesc:   markedDescription,
			wantTopics: []string{"forgemirror", "go"},
		},
		{
			name:       "topics setting keeps the marker topic",
			settings:   "      topics: [infra]\n",
			wantDesc:   markedDescription,
			wantTopics: []string{"forgemirror", "infra"},
		},
		{
			name:       "custom note and topic",
			settings:   "      mirror_note: \"Mirror of {{ .FullName }}\"\n      mirror_topic: mirror\n",
			wantDesc:   "the app · Mirror of acme/app",
			wantTopics: []string{"go", "mirror"},
		},
		{
			name:       "disabled",
			settings:   "      mirror_note: \"\"\n      mirror_topic: \"\"\n",
			wantDesc:   "the app",
			wantTopics: []string{"go"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gh := &fakeForge{missing: true}
			target := githubTarget
			if tc.settings != "" {
				target += "    settings:\n" + tc.settings
			}
			d, _ := newSyncDispatcher(t, target, &fakeMirrorer{}, sourceRepo(), map[string]*fakeForge{"github": gh})

			d.Dispatch(t.Context(), event("app"))
			shutdown(t, d)

			if len(gh.creates) != 1 {
				t.Fatalf("creates = %v, want one", gh.creates)
			}
			got := gh.creates[0]
			if got.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tc.wantDesc)
			}
			if !slices.Equal(got.Topics, tc.wantTopics) {
				t.Errorf("Topics = %v, want %v", got.Topics, tc.wantTopics)
			}
		})
	}
}
