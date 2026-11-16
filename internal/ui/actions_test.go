package ui_test

import (
	"context"
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
	mu         sync.Mutex
	calls      []string
	refreshErr error
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

func (a *fakeActions) RefreshAll(context.Context) (int, error) {
	a.record("refresh all")

	return 3, a.refreshErr
}

func (a *fakeActions) SyncAll(context.Context) int {
	a.record("sync all")

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
			name: "sync one", path: "/actions/sync", repo: "acme/app", headers: htmx,
			wantCode: http.StatusOK, wantCall: "sync acme/app", wantNotice: "Sync started for acme/app.",
		},
		{
			name: "refresh one", path: "/actions/refresh", repo: "acme/app", headers: htmx,
			wantCode: http.StatusOK, wantCall: "refresh acme/app", wantNotice: "Refresh started for acme/app.",
		},
		{
			name: "sync all", path: "/actions/sync", headers: htmx,
			wantCode: http.StatusOK, wantCall: "sync all", wantNotice: "Sync started for 1 repository.",
		},
		{
			name: "refresh all", path: "/actions/refresh", headers: htmx,
			wantCode: http.StatusOK, wantCall: "refresh all", wantNotice: "Refresh started for 3 repositories.",
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
	if notice != "" && !strings.Contains(rec.Body.String(), `<p class="notice" role="status">`+notice) {
		t.Errorf("body lacks notice %q:\n%s", notice, rec.Body.String())
	}
}

func TestActionErrorIsShown(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, nil, &fakeActions{refreshErr: errors.New("listing source repositories: forgejo down")})
	rec := f.post(t, "/actions/refresh", url.Values{}, map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "forgejo down") {
		t.Errorf("status = %d, body lacks the error:\n%s", rec.Code, rec.Body.String())
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
			rec := f.post(t, "/actions/sync", url.Values{"repo": {"acme/app"}}, headers)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if got := actions.recorded(); len(got) != 0 {
				t.Errorf("calls = %v, want none", got)
			}
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
		Warnings: []string{`topic "Go Lang" is not supported by the target forge`},
	}}})
	recordPlan(t, f.store, plan.Repo{Name: "acme/clean", At: testNow(), Targets: []plan.Target{{Name: "github"}}})
	recordPlan(t, f.store, plan.Repo{Name: "acme/broken", At: testNow(), Targets: []plan.Target{{
		Name: "github", Error: "target \"github\": reading repository: 502",
	}}})

	body := f.do(t, http.MethodGet, "/fragments/repos").Body.String()
	for _, want := range []string{
		"out of sync · 14 changes",
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
		{name: "applied changes", changes: true, want: "synced · 1 change"},
		{name: "applied nothing", want: "in sync"},
		{
			name: "push failed while applying", changes: true,
			fail: func(s *status.Store, ctx context.Context) {
				s.TargetFinished(ctx, "acme/app", "github", time.Second, errors.New("rejected"))
			},
			want: `pill-fail">failed`,
		},
		{
			name: "operation failed while applying", changes: true,
			fail: func(s *status.Store, ctx context.Context) {
				s.SyncFinished(ctx, "acme/app", "github", "labels", errors.New("forbidden"))
			},
			want: `pill-fail">failed`,
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
