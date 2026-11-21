package ui_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

func newQuietFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithQueued(t, nil, nil, func() map[string]bool { return nil })
}

// The trigger that started running work lights its branch and is named in
// the ForgeMirror line, so operators see why something runs.
func TestPipelineShowsTheTrigger(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		runs   map[string]string // repository → recorded trigger
		fired  []string
		engine string
	}{
		{name: "push", runs: map[string]string{"acme/app": "webhook"}, fired: []string{"push"}, engine: "syncing acme/app · <b>by push</b>"},
		{name: "deletion is a push", runs: map[string]string{"acme/app": "source deletion"}, fired: []string{"push"}, engine: "<b>by push</b>"},
		{name: "schedule", runs: map[string]string{"acme/app": "schedule"}, fired: []string{"schedule"}, engine: "<b>by schedule</b>"},
		{name: "manual sync", runs: map[string]string{"acme/app": "manual sync"}, fired: []string{"manual"}, engine: "<b>by manual</b>"},
		{name: "refresh is manual", runs: map[string]string{"acme/app": "refresh"}, fired: []string{"manual"}, engine: "<b>by manual</b>"},
		{name: "retry", runs: map[string]string{"acme/app": "retry"}, fired: []string{"retry"}, engine: "<b>by retry</b>"},
		{
			name:   "several at once",
			runs:   map[string]string{"acme/app": "webhook", "acme/lib": "schedule", "acme/web": "schedule"},
			fired:  []string{"push", "schedule"},
			engine: "3 running · <b>1 by push, 2 by schedule</b>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newQuietFixture(t)
			for repo, trigger := range tc.runs {
				f.store.RunStarted(t.Context(), repo, trigger, true)
			}
			body := f.do(t, http.MethodGet, "/fragments/overview").Body.String()
			for _, branch := range []string{"push", "schedule", "manual", "retry"} {
				lit := strings.Contains(body, `<li class="pl-fired" data-trigger="`+branch+`">`)
				if want := slices.Contains(tc.fired, branch); lit != want {
					t.Errorf("branch %q lit = %v, want %v:\n%s", branch, lit, want, body)
				}
			}
			if !strings.Contains(body, tc.engine) {
				t.Errorf("engine line lacks %q:\n%s", tc.engine, body)
			}
		})
	}
}

func TestPipelineTargetStates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, s *status.Store)
		want  []string
	}{
		{
			name: "held for every repository",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				if err := s.Pause(t.Context(), status.Pause{Target: "github", Reason: "Maintenance"}); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{`<li class="pl-held" data-target="github">`, "held: Maintenance"},
		},
		{
			name: "failing",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				s.RunStarted(t.Context(), "acme/app", "webhook", true)
				s.TargetFinished(t.Context(), "acme/app", "github", time.Second, errors.New("denied"))
				s.RunFinished(t.Context(), "acme/app")
			},
			want: []string{`<li class="pl-failed" data-target="github">`, `<span class="pl-note">failed: acme/app</span>`},
		},
		{
			name: "pushing, then pushed",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				s.RunStarted(t.Context(), "acme/app", "webhook", true)
				s.TargetFinished(t.Context(), "acme/app", "github", time.Second, nil)
			},
			want: []string{
				`<li class="pl-done" data-target="github">`, `<span class="visually-hidden">: pushed,</span>`,
				`<span class="pl-note">pushed acme/app</span>`,
				`<li class="pl-busy" data-target="backup">`, `<span class="visually-hidden">: pushing,</span>`,
				`<span class="pl-note">pushing acme/app</span>`,
			},
		},
		{
			name: "long lists are shortened",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				for _, repo := range []string{"acme/a", "acme/b", "acme/c", "acme/d"} {
					s.RunStarted(t.Context(), repo, "schedule", true)
				}
			},
			want: []string{`<span class="pl-note">pushing acme/a, acme/b &#43;2 more</span>`},
		},
		{
			name: "idle with the last push",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				s.RunStarted(t.Context(), "acme/app", "webhook", true)
				s.TargetFinished(t.Context(), "acme/app", "github", time.Second, nil)
				s.RunFinished(t.Context(), "acme/app")
			},
			want: []string{`<span class="pl-note">1 repository · last push `, ` ago</span>`},
		},
		{
			name: "compared, never pushed",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				recordPlan(t, s, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{{Name: "github"}}})
			},
			want: []string{`<span class="pl-note">1 repository · never pushed</span>`},
		},
		{
			name: "held until",
			setup: func(t *testing.T, s *status.Store) {
				t.Helper()
				p := status.Pause{Target: "github", Reason: "Audit", Until: testNow().Add(6 * time.Hour)}
				if err := s.Pause(t.Context(), p); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"held: Audit · until Nov 15 18:00 UTC"},
		},
		{
			name:  "unconfigured types collapse",
			setup: func(*testing.T, *status.Store) {},
			want:  []string{"+3 available", "GitLab, Codeberg, Forgejo", `<li class="pl-idle" data-target="github">`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newQuietFixture(t)
			tc.setup(t, f.store)
			body := f.do(t, http.MethodGet, "/fragments/overview").Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("overview lacks %q:\n%s", want, body)
				}
			}
		})
	}
}

func TestPipelineIdleSchedule(t *testing.T) {
	t.Parallel()
	f := newQuietFixture(t)
	f.store.ScheduledRunFinished(t.Context(), 1, nil)
	body := f.do(t, http.MethodGet, "/fragments/overview").Body.String()
	for _, want := range []string{
		"<small>idle · next sweep", `<span class="pl-source">every 1h0m0s</span>`,
		`<p class="pl-engine">`, `<ul class="pl-triggers" role="list" aria-label="Triggers">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("idle overview lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "pl-fired") || strings.Contains(body, "pl-live") {
		t.Errorf("idle overview shows activity:\n%s", body)
	}
}
