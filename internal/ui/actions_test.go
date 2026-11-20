package ui_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
)

func reviewMode() []string { return []string{"FORGEMIRROR_SYNC__AUTO=false"} }

type fakeActions struct {
	mu           sync.Mutex
	calls        []string
	refreshErr   error
	refreshCount *int
	syncCount    *int
}

func (a *fakeActions) record(call string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, call)
}

func (a *fakeActions) recorded() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.calls)
}

func (a *fakeActions) Refresh(_ context.Context, name string) error {
	if name != "acme/app" {
		return fmt.Errorf("%w: %s", control.ErrUnknownRepository, name)
	}
	a.record("refresh " + name)

	return nil
}

func (a *fakeActions) Sync(_ context.Context, name string) error {
	if name != "acme/app" {
		return fmt.Errorf("%w: %s", control.ErrUnknownRepository, name)
	}
	a.record("sync " + name)

	return nil
}

func (a *fakeActions) Approve(_ context.Context, _, target, _ string) error {
	a.record("approve " + target)
	return nil
}

func (a *fakeActions) Pause(_ context.Context, _, target, _ string, _ time.Time) error {
	a.record("pause " + target)
	return nil
}

func (a *fakeActions) Resume(_ context.Context, _, target string) error {
	a.record("resume " + target)
	return nil
}

func (a *fakeActions) RepairLFS(_ context.Context, name string) error {
	if name != "acme/app" {
		return control.ErrUnknownRepository
	}
	a.record("repair " + name)
	return nil
}

func (a *fakeActions) RefreshAll(context.Context) (int, error) {
	a.record("refresh all")

	if a.refreshCount != nil {
		return *a.refreshCount, a.refreshErr
	}
	return 3, a.refreshErr
}

func (a *fakeActions) SyncAll(context.Context) int {
	a.record("sync all")

	if a.syncCount != nil {
		return *a.syncCount
	}
	return 1
}

func (f *fixture) post(
	t *testing.T,
	path string,
	form url.Values,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

func TestActions(t *testing.T) {
	t.Parallel()

	htmx := map[string]string{"HX-Request": "true", "Sec-Fetch-Site": "same-origin"}
	tests := []struct {
		name       string
		path       string
		repo       string
		headers    map[string]string
		wantCode   int
		wantCall   string
		wantNotice string
	}{
		{
			name: "repair LFS", path: "/actions/repair-lfs", repo: "acme/app", headers: htmx,
			wantCode: http.StatusOK, wantCall: "repair acme/app", wantNotice: "LFS repair started for acme/app.",
		},
		{
			name: "unknown repair", path: "/actions/repair-lfs", repo: "evil/repo", headers: htmx,
			wantCode: http.StatusNotFound,
		},
		{
			name: "repair without repository", path: "/actions/repair-lfs", headers: htmx,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "sync one", path: "/actions/sync", repo: "acme/app", headers: htmx,
			wantCode: http.StatusOK, wantCall: "sync acme/app", wantNotice: "Sync started for acme/app.",
		},
		{
			name: "refresh one", path: "/actions/refresh", repo: "acme/app", headers: htmx,
			wantCode: http.StatusOK, wantCall: "refresh acme/app", wantNotice: "Refresh started for acme/app.",
		},
		{
			name:       "sync all",
			path:       "/actions/sync",
			headers:    htmx,
			wantCode:   http.StatusOK,
			wantCall:   "sync all",
			wantNotice: "Sync started for all pending repositories (1 queued).",
		},
		{
			name:       "refresh all",
			path:       "/actions/refresh",
			headers:    htmx,
			wantCode:   http.StatusOK,
			wantCall:   "refresh all",
			wantNotice: "Refresh started for all repositories (3 queued).",
		},
		{
			name: "plain form is redirected back", path: "/actions/sync", repo: "acme/app",
			wantCode: http.StatusSeeOther, wantCall: "sync acme/app",
		},
		{
			name:     "unknown repository",
			path:     "/actions/sync",
			repo:     "evil/repo",
			headers:  htmx,
			wantCode: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actions := &fakeActions{}
			f := newFixtureWith(t, nil, actions)
			form := url.Values{}
			if tc.repo != "" {
				form.Set("repo", tc.repo)
			}

			rec := f.post(t, tc.path, form, tc.headers)
			assertAction(t, rec, actions, tc.wantCode, tc.wantCall, tc.wantNotice)
		})
	}
}

func assertAction(t *testing.T, rec *httptest.ResponseRecorder, actions *fakeActions, code int, call, notice string) {
	t.Helper()

	if rec.Code != code {
		t.Fatalf("status = %d, want %d: %s", rec.Code, code, rec.Body.String())
	}
	var want []string
	if call != "" {
		want = []string{call}
	}
	if got := actions.recorded(); !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if code == http.StatusSeeOther && rec.Header().Get("Location") != "../" {
		t.Errorf("Location = %q, want ../", rec.Header().Get("Location"))
	}
	if notice != "" {
		got := actionNotification(t, rec)
		if got.Message != notice || got.Kind != "success" {
			t.Errorf("notification=%+v want success %q", got, notice)
		}
		if strings.Contains(rec.Body.String(), notice) || strings.Contains(rec.Body.String(), "action-notice") {
			t.Error("action acknowledgement still rendered inline")
		}
	}
}

type actionToast struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

func actionNotification(t *testing.T, rec *httptest.ResponseRecorder) actionToast {
	t.Helper()
	var events map[string]actionToast
	if err := json.Unmarshal([]byte(rec.Header().Get("Hx-Trigger")), &events); err != nil {
		t.Fatalf("invalid or missing notification event: %v", err)
	}
	notice, ok := events["forgemirror:notice"]
	if !ok {
		t.Fatal("action notification event missing")
	}
	return notice
}

func TestNoWorkIsNotAnnouncedAsStarted(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ path, message string }{
		{"/actions/refresh", "No repositories available to refresh."},
		{"/actions/sync", "No repositories need syncing."},
	} {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			f := newFixtureWith(t, nil, &fakeActions{refreshCount: new(0), syncCount: new(0)})
			rec := f.post(t, tt.path, url.Values{}, map[string]string{"Hx-Request": "true"})
			if got := actionNotification(t, rec); got.Message != tt.message || got.Kind != "success" {
				t.Fatalf("notification=%+v", got)
			}
		})
	}
}

func TestActionMessageIsDataNotMarkup(t *testing.T) {
	t.Parallel()
	message := `<img src=x onerror="window.noticeInjection=true">`
	f := newFixtureWith(t, nil, &fakeActions{refreshErr: errors.New(message)})
	rec := f.post(t, "/actions/refresh", url.Values{}, map[string]string{"Hx-Request": "true"})
	if got := actionNotification(t, rec); got.Message != message || got.Kind != "error" {
		t.Fatalf("notification=%+v", got)
	}
	if strings.Contains(rec.Body.String(), message) {
		t.Fatal("message embedded as markup")
	}
}

func TestActionErrorIsShown(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, nil, &fakeActions{refreshErr: errors.New("listing source repositories: forgejo down")})
	rec := f.post(t, "/actions/refresh", url.Values{}, map[string]string{"HX-Request": "true"})
	notice := actionNotification(t, rec)
	if rec.Code != http.StatusOK || !strings.Contains(notice.Message, "forgejo down") || notice.Kind != "error" {
		t.Errorf("status=%d notification=%+v", rec.Code, notice)
	}
}

func TestActionsRejectCrossOrigin(t *testing.T) {
	t.Parallel()

	for name, headers := range map[string]map[string]string{
		"fetch metadata": {"Sec-Fetch-Site": "cross-site"},
		"origin":         {"Origin": "https://evil.example"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actions := &fakeActions{}
			f := newFixtureWith(t, nil, actions)
			for _, path := range []string{"/actions/sync", "/actions/repair-lfs", "/actions/pause", "/actions/approve"} {
				rec := f.post(t, path, url.Values{"repo": {"acme/app"}}, headers)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s status = %d, want 403", path, rec.Code)
				}
			}
			if got := actions.recorded(); len(got) != 0 {
				t.Errorf("calls = %v, want none", got)
			}
		})
	}
}

func TestApprovalAction(t *testing.T) {
	t.Parallel()
	actions := &fakeActions{}
	f := newFixtureWith(t, nil, actions)
	rec := f.post(
		t,
		"/actions/approve",
		url.Values{"repo": {"acme/app"}, "target": {"github"}, "identity": {"current-mapping"}},
		map[string]string{"HX-Request": "true"},
	)
	assertAction(t, rec, actions, http.StatusOK, "approve github", "")
	if rec = f.do(t, http.MethodGet, "/actions/approve"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET approval status=%d", rec.Code)
	}
}

func TestPauseActions(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"pause", "resume"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			actions := &fakeActions{}
			f := newFixtureWith(t, nil, actions)
			rec := f.post(
				t,
				"/actions/pause",
				url.Values{"target": {"github"}, "mode": {mode}},
				map[string]string{"HX-Request": "true"},
			)
			assertAction(t, rec, actions, http.StatusOK, mode+" github", "")
		})
	}
}

func TestActionBodyLimit(t *testing.T) {
	t.Parallel()

	actions := &fakeActions{}
	f := newFixtureWith(t, nil, actions)
	rec := f.post(t, "/actions/sync", url.Values{"repo": {strings.Repeat("a", 8<<10)}}, nil)
	if rec.Code != http.StatusBadRequest || len(actions.recorded()) != 0 {
		t.Errorf("status = %d, calls = %v; want 400 and none", rec.Code, actions.recorded())
	}
}

func TestModeBadge(t *testing.T) {
	t.Parallel()

	auto := newFixtureWith(t, nil, &fakeActions{}).do(t, http.MethodGet, "/").Body.String()
	if !strings.Contains(auto, "Auto sync") || strings.Contains(auto, "Review mode") {
		t.Error("auto mode page lacks the Auto sync badge")
	}
	review := newFixtureWith(t, reviewMode(), &fakeActions{})
	page := review.do(t, http.MethodGet, "/").Body.String()
	for _, want := range []string{"Review mode", `hx-post="actions/refresh"`, `id="theme-toggle"`, `src="static/app.js"`} {
		if !strings.Contains(page, want) {
			t.Errorf("review page lacks %q", want)
		}
	}
	if strings.Contains(newFixture(t).do(t, http.MethodGet, "/").Body.String(), "Refresh all") {
		t.Error("page without actions shows action buttons")
	}
}

// recordPlan stores a finished plan-only (or applied) run for name.
func recordPlan(t *testing.T, s *status.Store, p plan.Repo) {
	t.Helper()

	s.RunStarted(t.Context(), p.Name, p.Apply)
	s.PlanFinished(t.Context(), p)
	s.RunFinished(t.Context(), p.Name)
}

func TestDiffView(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, reviewMode(), &fakeActions{})
	recordPlan(t, f.store, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{{
		Name: "github", Create: true,
		Refs: []plan.RefChange{
			{Ref: "refs/heads/main", Old: "1111111aaaa", New: "2222222bbbb"},
			{Ref: "refs/heads/new", New: "3333333cccc"},
			{Ref: "refs/tags/gone", Old: "4444444dddd"},
		},
		RefsOmitted: 7,
		Settings:    []plan.FieldChange{{Field: "description", Old: "", New: "<b>the app</b>"}},
		Labels: []plan.LabelChange{
			{Name: "bug", New: &forge.Label{Name: "bug", Color: "ff0000"}},
			{Name: "old", Old: &forge.Label{Name: "old", Color: "000000"}},
		},
		Releases: []plan.ReleaseChange{
			{Tag: "v1.0.0", New: &forge.Release{Tag: "v1.0.0"}, AssetsAdded: []string{"a.tgz", "b.tgz"}},
			{
				Tag: "v0.9.0", Old: &forge.Release{}, New: &forge.Release{},
				Fields:      []plan.FieldChange{{Field: "name", Old: "old", New: "new"}},
				AssetsAdded: []string{"c.tgz"}, AssetsReplaced: []string{"d.tgz"}, AssetsRemoved: []string{"e.tgz"},
			},
			{Tag: "v0.1.0", Old: &forge.Release{}},
		},
		Packages: []plan.PackageChange{{
			Package: "app", Image: "ghcr.io/acme/app",
			Tags: []plan.TagChange{{Tag: "1.0.0", New: "sha256:aa"}}, TagsOmitted: 2,
		}},
		Warnings: []string{`topic "Go Lang" is not supported by the target forge`},
	}}})
	recordPlan(t, f.store, plan.Repo{Name: "acme/clean", At: testNow(), Targets: []plan.Target{{Name: "github"}}})
	recordPlan(t, f.store, plan.Repo{Name: "acme/broken", At: testNow(), Targets: []plan.Target{{
		Name: "github", Error: "target \"github\": reading repository: 502",
	}}})

	body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	for _, want := range []string{
		"Current diff · 20 changes",
		"release <b>v1.0.0</b> <span class=\"muted\">2 assets</span>",
		"release <b>v0.9.0</b> name: <span class=\"old\">old</span> → <span class=\"new\">new</span>",
		"assets +c.tgz ~d.tgz −e.tgz",
		"release <b>v0.1.0</b>",
		"package <b>app</b> → <code>ghcr.io/acme/app</code>: 3 tags <code class=\"muted\">1.0.0</code>",
		"create repository",
		"<code>refs/heads/main</code> <code class=\"muted\">1111111</code> → <code>2222222</code>",
		"<code>refs/heads/new</code> <code class=\"muted\">3333333</code>",
		"<code>refs/tags/gone</code>",
		"7 more ref changes",
		"description: <span class=\"old\">(empty)</span> → <span class=\"new\">&lt;b&gt;the app&lt;/b&gt;</span>",
		"label <b>bug</b> <span class=\"swatch\" data-color=\"ff0000\">#ff0000</span>",
		"label <b>old</b>",
		"topic &#34;Go Lang&#34; is not supported",
		`data-open-default`,
		"Sync all (1)",
		"1 out of sync", "1 failed",
		"in sync",
		"reading repository: 502",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("repos fragment lacks %q", want)
		}
	}

	// Attention first: failed, then out of sync, then in sync.
	broken, app, clean := strings.Index(
		body,
		"acme/broken",
	), strings.Index(
		body,
		"acme/app",
	), strings.Index(
		body,
		"acme/clean",
	)
	if broken >= app || app >= clean {
		t.Errorf("order = broken %d, app %d, clean %d; want failed, out of sync, in sync", broken, app, clean)
	}

	if got := ui.Pending(f.store.Snapshot()); !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("Pending = %v, want [acme/app]", got)
	}
}

func TestAppliedPlanStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		changes bool
		fail    func(s *status.Store, ctx context.Context)
		want    string
	}{
		{name: "applied changes await verification", changes: true, want: "Current diff · awaiting verification"},
		{name: "applied nothing awaits verification", want: "Current diff · awaiting verification"},
		{
			name: "push failed while applying", changes: true,
			fail: func(s *status.Store, ctx context.Context) {
				s.TargetFinished(ctx, "acme/app", "github", time.Second, errors.New("rejected"))
			},
			want: `title="GitHub: error`,
		},
		{
			name: "operation failed while applying", changes: true,
			fail: func(s *status.Store, ctx context.Context) {
				s.SyncFinished(ctx, "acme/app", "github", "labels", errors.New("forbidden"))
			},
			want: `title="GitHub: error`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			ctx := t.Context()
			p := plan.Repo{Name: "acme/app", At: testNow(), Apply: true, Targets: []plan.Target{{Name: "github"}}}
			if tc.changes {
				p.Targets[0].Refs = []plan.RefChange{{Ref: "refs/heads/main", Old: "a", New: "b"}}
			}
			f.store.RunStarted(ctx, "acme/app", true)
			f.store.PlanFinished(ctx, p)
			if tc.fail != nil {
				tc.fail(f.store, ctx)
			}
			f.store.RunFinished(ctx, "acme/app")

			body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("body lacks %q:\n%s", tc.want, body)
			}
			if got := ui.Pending(f.store.Snapshot()); len(got) != 0 {
				t.Errorf("Pending = %v, want none for applied plans", got)
			}
		})
	}
}
