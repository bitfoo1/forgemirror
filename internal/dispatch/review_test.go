package dispatch_test

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

type modeOptions struct {
	auto   bool
	logs   *syncBuffer
	source forge.Source
}

func newModeDispatcher(
	t *testing.T,
	targetsYAML string,
	m *fakeMirrorer,
	forges map[string]*fakeForge,
	mo modeOptions,
) (*dispatch.Dispatcher, *recordingObserver) {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	if mo.logs != nil {
		logger = slog.New(slog.NewTextHandler(mo.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	targets := make(map[string]forge.Target, len(forges))
	for name, f := range forges {
		targets[name] = f
	}
	obs := &recordingObserver{}

	return dispatch.New(dispatch.Options{
		Mirrorer:      m,
		Source:        config.Source{Username: "oauth2", Token: "src-token"},
		Targets:       loadTargets(t, targetsYAML),
		ForgeSource:   mo.source,
		Forges:        targets,
		MaxConcurrent: 4,
		Auto:          mo.auto,
		Logger:        logger,
		Observer:      obs,
	}), obs
}

// waitRuns waits until n runs have finished; Shutdown alone would drop a
// queued follow-up run.
func waitRuns(t *testing.T, obs *recordingObserver, n int32) {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)
	for obs.done.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("finished runs = %d, want %d", obs.done.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitIdle waits until the dispatcher has deregistered every repository.
// RunFinished fires before the run loop releases the repository, and RetryDue
// skips busy repositories instead of queueing behind them, so waitRuns alone
// does not make a following RetryDue call deterministic.
func waitIdle(t *testing.T, d *dispatch.Dispatcher) {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)
	for len(d.Queued()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("repositories still running: %v", d.Queued())
		}
		time.Sleep(time.Millisecond)
	}
}

func withKind(e webhook.Event, k webhook.EventKind) webhook.Event {
	e.Kind = k

	return e
}

func TestReviewModeOnlyPlans(t *testing.T) {
	t.Parallel()

	for _, kind := range []webhook.EventKind{webhook.EventPush, webhook.EventScheduled, webhook.EventRefresh} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			t.Parallel()

			gh := &fakeForge{missing: true}
			m := &fakeMirrorer{}
			d, obs := newModeDispatcher(t, githubTarget+codebergTarget, m,
				map[string]*fakeForge{"github": gh}, modeOptions{source: sourceRepo()})

			d.Dispatch(t.Context(), withKind(event("app"), kind))
			shutdown(t, d)

			if got := gh.snapshot(); !slices.Equal(got, []string{"Get"}) {
				t.Errorf("github calls = %v, want only the plan's Get", got)
			}
			if clones, pushes, _, _ := m.snapshot(); len(clones)+len(pushes) != 0 {
				t.Errorf("clones = %d, pushes = %d; want none in review mode", len(clones), len(pushes))
			}
			if results, syncs := obs.snapshot(); len(results)+len(syncs) != 0 {
				t.Errorf("results = %+v, syncs = %+v; want none", results, syncs)
			}

			assertCreatePlanOnly(t, obs)
		})
	}
}

func assertCreatePlanOnly(t *testing.T, obs *recordingObserver) {
	t.Helper()

	plans := obs.planSnapshot()
	if len(plans) != 1 || plans[0].Apply || len(plans[0].Targets) != 2 {
		t.Fatalf("plans = %+v, want one plan-only plan for two targets", plans)
	}
	github := plans[0].Targets[0]
	if github.Name != "github" || !github.Create || len(github.Refs) != 1 ||
		len(github.Labels) != 1 || github.Labels[0].Kind() != plan.KindCreate {
		t.Errorf("github plan = %+v, want create with all refs and labels", github)
	}
	if !slices.Equal(obs.applies, []bool{false}) {
		t.Errorf("RunStarted apply = %v, want [false]", obs.applies)
	}
}

func TestSyncAppliesInReviewMode(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{missing: true}
	forges := map[string]*fakeForge{"github": gh}
	m := &fakeMirrorer{pushHook: pushRecorder(forges, nil)}
	d, obs := newModeDispatcher(t, githubTarget, m, forges, modeOptions{source: sourceRepo()})

	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventSync))
	shutdown(t, d)

	if got := gh.snapshot(); !slices.Contains(got, "Create") || !slices.Contains(got, "push") {
		t.Errorf("github calls = %v, want create and push", got)
	}
	if plans := obs.planSnapshot(); len(plans) != 1 || !plans[0].Apply {
		t.Errorf("plans = %+v, want one applied plan", plans)
	}
}

func TestRefreshNeverAppliesInAutoMode(t *testing.T) {
	t.Parallel()

	m := &fakeMirrorer{}
	d, obs := newModeDispatcher(t, codebergTarget, m, nil, modeOptions{auto: true})

	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	shutdown(t, d)

	if clones, pushes, _, _ := m.snapshot(); len(clones)+len(pushes) != 0 {
		t.Errorf("clones = %d, pushes = %d; want none for a refresh", len(clones), len(pushes))
	}
	if plans := obs.planSnapshot(); len(plans) != 1 || plans[0].Changes() != 1 {
		t.Errorf("plans = %+v, want one plan with the ref update", plans)
	}
}

func TestPushOnlyToTargetsWithRefChanges(t *testing.T) {
	t.Parallel()

	src := map[string]string{"refs/heads/main": "new"}
	m := &fakeMirrorer{refsHook: func(_ context.Context, r mirror.Remote) (map[string]string, error) {
		switch {
		case strings.Contains(r.URL, "git.example"):
			return src, nil
		case strings.Contains(r.URL, "github.com"):
			return map[string]string{"refs/heads/main": "new"}, nil
		case strings.Contains(r.URL, "gitlab.com"):
			return nil, fmt.Errorf("repository not found")
		default:
			return map[string]string{"refs/heads/main": "old", "refs/tags/gone": "t"}, nil
		}
	}}
	untyped := `
  - name: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: gh-token
  - name: gitlab
    url: "https://gitlab.com/acme/{{ .Name }}.git"
    token: gl-token
` + codebergTarget
	d, obs := newModeDispatcher(t, untyped, m, nil, modeOptions{auto: true})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	_, pushes, _, _ := m.snapshot()
	var hosts []string
	for _, p := range pushes {
		hosts = append(hosts, strings.Split(p.URL, "/")[2])
	}
	slices.Sort(hosts)
	if !slices.Equal(hosts, []string{"codeberg.org", "gitlab.com"}) {
		t.Errorf("pushed to %v, want codeberg (changed) and gitlab (refs unknown)", hosts)
	}

	p := obs.planSnapshot()[0]
	byName := map[string]plan.Target{}
	for _, tg := range p.Targets {
		byName[tg.Name] = tg
	}
	if byName["github"].Changes() != 0 {
		t.Errorf("github plan = %+v, want in sync", byName["github"])
	}
	if !byName["gitlab"].RefsUnknown || len(byName["gitlab"].Warnings) != 1 {
		t.Errorf("gitlab plan = %+v, want unknown refs with a warning", byName["gitlab"])
	}
	want := []plan.RefChange{{Ref: "refs/heads/main", Old: "old", New: "new"}, {Ref: "refs/tags/gone", Old: "t"}}
	if got := byName["codeberg"].Refs; !slices.Equal(got, want) {
		t.Errorf("codeberg refs = %+v, want %+v", got, want)
	}
}

func TestSyncOfDeletedSourceDeletes(t *testing.T) {
	t.Parallel()

	src := sourceRepo()
	src.err = fmt.Errorf("get: %w", forge.ErrNotFound)
	gh := &fakeForge{}
	gl := &fakeForge{}
	m := &fakeMirrorer{}
	d, obs := newModeDispatcher(t, githubTarget+"    delete: true\n"+gitlabTarget, m,
		map[string]*fakeForge{"github": gh, "gitlab": gl}, modeOptions{source: src})

	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventSync))
	waitRuns(t, obs, 2)
	shutdown(t, d)

	plans := obs.planSnapshot()
	if len(plans) != 2 || !plans[0].SourceDeleted || len(plans[0].Targets) != 1 || !plans[0].Targets[0].Delete {
		t.Fatalf("plans = %+v, want a deletion of github only", plans)
	}
	if got := gh.snapshot(); !slices.Equal(got, []string{"Get", "Get", "Delete"}) {
		t.Errorf("github calls = %v, want two plans and one Delete", got)
	}
	if got := gl.snapshot(); len(got) != 0 {
		t.Errorf("gitlab calls = %v, want none (delete: false)", got)
	}
	if clones, _, _, _ := m.snapshot(); len(clones) != 0 {
		t.Errorf("clones = %d, want none", len(clones))
	}
	if _, ok := d.Known("acme/app"); ok {
		t.Error("deleted repository still known")
	}
}

func TestDeletionOfMissingTargetIsNoChange(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{missing: true}
	d, obs := newModeDispatcher(t, githubTarget+"    delete: true\n", &fakeMirrorer{},
		map[string]*fakeForge{"github": gh}, modeOptions{auto: true, source: sourceRepo()})

	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventDelete))
	shutdown(t, d)

	if got := gh.snapshot(); !slices.Equal(got, []string{"Get"}) {
		t.Errorf("github calls = %v, want Get only", got)
	}
	if p := obs.planSnapshot()[0]; p.Changes() != 0 || !p.SourceDeleted {
		t.Errorf("plan = %+v, want source deleted without changes", p)
	}
}

func TestQueuedSyncSurvivesPlanOnlyEvent(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var once sync.Once
	m := &fakeMirrorer{refsHook: func(_ context.Context, r mirror.Remote) (map[string]string, error) {
		once.Do(func() {
			started <- struct{}{}
			<-release
		})

		return map[string]string{"refs/heads/main": r.URL}, nil
	}}
	d, obs := newModeDispatcher(t, codebergTarget, m, nil, modeOptions{})

	ctx := t.Context()
	d.Dispatch(ctx, event("app")) // plan only: review mode
	<-started
	d.Dispatch(ctx, withKind(event("app"), webhook.EventSync))
	d.Dispatch(ctx, event("app")) // a later push must not cancel the sync
	close(release)
	waitRuns(t, obs, 2)
	shutdown(t, d)

	if !slices.Equal(obs.applies, []bool{false, true}) {
		t.Errorf("runs applied = %v, want [false true]", obs.applies)
	}
	if _, pushes, _, _ := m.snapshot(); len(pushes) != 1 {
		t.Errorf("pushes = %d, want 1", len(pushes))
	}
}

func TestKnownRepositories(t *testing.T) {
	t.Parallel()

	d, _ := newModeDispatcher(t, codebergTarget, &fakeMirrorer{}, nil, modeOptions{})
	d.Dispatch(t.Context(), event("b"))
	d.Dispatch(t.Context(), event("a"))
	shutdown(t, d)

	if got := d.KnownNames(); !slices.Equal(got, []string{"acme/a", "acme/b"}) {
		t.Errorf("KnownNames = %v", got)
	}
	r, ok := d.Known("acme/a")
	if !ok || r.CloneURL != "https://git.example/acme/a.git" {
		t.Errorf("Known(acme/a) = %+v, %v", r, ok)
	}
	if _, found := d.Known("acme/unknown"); found {
		t.Error("Known(acme/unknown) = true")
	}
}

func TestChangeLog(t *testing.T) {
	t.Parallel()

	gh := &fakeForge{
		repo:   forge.Repo{Description: "old", DefaultBranch: "main", Topics: []string{"go"}, HasIssues: true},
		labels: []forge.Label{{Name: "bug", Color: "00ff00"}, {Name: "stale", Color: "000000"}},
	}
	forges := map[string]*fakeForge{"github": gh}
	m := &fakeMirrorer{pushHook: pushRecorder(forges, nil)}
	logs := &syncBuffer{}
	d, _ := newModeDispatcher(t, githubTarget+"    prune_labels: true\n", m, forges,
		modeOptions{auto: true, logs: logs, source: sourceRepo()})

	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	out := logs.String()
	for _, want := range []string{
		`level=INFO msg="plan: out of sync" repository=acme/app`,
		`level=DEBUG msg="plan: setting" repository=acme/app ref=refs/heads/main delivery="" target=github field=description old=old new="` + markedDescription + `"`,
		`level=INFO msg="setting changed" repository=acme/app ref=refs/heads/main delivery="" target=github field=description old=old new="` + markedDescription + `"`,
		`level=INFO msg="label updated" repository=acme/app ref=refs/heads/main delivery="" target=github label=bug old.color=00ff00 old.description="" new.color=ff0000 new.description=""`,
		`level=INFO msg="label deleted" repository=acme/app ref=refs/heads/main delivery="" target=github label=stale`,
		`level=INFO msg="refs pushed" repository=acme/app ref=refs/heads/main delivery="" target=github created=0 updated=1 forced=0 deleted=0`,
		`level=INFO msg="ref pushed" repository=acme/app ref=refs/heads/main delivery="" target=github ref=refs/heads/main change=update summary=a..b`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "gh-token") || strings.Contains(out, "src-token") {
		t.Error("a token reached the log")
	}
}

func TestChangeLogCapsRefsAtInfo(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	d := dispatch.New(dispatch.Options{
		Mirrorer:      &manyRefsMirrorer{fakeMirrorer: &fakeMirrorer{}, refs: 25},
		Source:        config.Source{Token: "src-token"},
		Targets:       loadTargets(t, codebergTarget),
		MaxConcurrent: 1,
		Auto:          true,
		Logger:        slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)

	out := logs.String()
	if n := strings.Count(out, `level=INFO msg="ref pushed"`); n != 20 {
		t.Errorf("info ref lines = %d, want 20", n)
	}
	if n := strings.Count(out, `level=DEBUG msg="ref pushed"`); n != 5 {
		t.Errorf("debug ref lines = %d, want 5", n)
	}
	if !strings.Contains(out, "created=25") {
		t.Errorf("summary lacks created=25:\n%s", out)
	}
}

// manyRefsMirrorer reports a push that created many refs.
type manyRefsMirrorer struct {
	*fakeMirrorer

	refs int
}

func (m *manyRefsMirrorer) Clone(ctx context.Context, src mirror.Remote) (mirror.Workspace, error) {
	if _, err := m.fakeMirrorer.Clone(ctx, src); err != nil {
		return nil, err
	}

	return manyRefsWorkspace{refs: m.refs}, nil
}

type manyRefsWorkspace struct{ refs int }

func (w manyRefsWorkspace) Push(context.Context, mirror.Remote) ([]mirror.RefUpdate, error) {
	updates := make([]mirror.RefUpdate, w.refs)
	for i := range updates {
		updates[i] = mirror.RefUpdate{
			Ref:     fmt.Sprintf("refs/tags/v%d", i),
			Kind:    mirror.RefCreated,
			Summary: "[new tag]",
		}
	}

	return updates, nil
}

func (manyRefsWorkspace) FetchLFS(context.Context, mirror.Remote) error { return nil }
func (manyRefsWorkspace) PushLFS(context.Context, mirror.Remote) error  { return nil }
func (manyRefsWorkspace) Close() error                                  { return nil }
