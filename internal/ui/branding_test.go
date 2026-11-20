package ui_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
)

func TestForgeDisplayIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, alias, typ, url, label, icon string }{
		{"GitHub", "github", "github", "https://github.com/acme/{{ .Name }}.git", "GitHub", "github"},
		{"GitLab", "gitlab", "gitlab", "https://gitlab.com/acme/{{ .Name }}.git", "GitLab", "gitlab"},
		{
			"Codeberg via Forgejo",
			"codeberg",
			"forgejo",
			"https://codeberg.org/acme/{{ .Name }}.git",
			"Codeberg",
			"codeberg",
		},
		{"public GitHub git only", "github", "", "https://github.com/acme/{{ .Name }}.git", "GitHub", "github"},
		{
			"public Codeberg git only",
			"codeberg",
			"",
			"https://codeberg.org/acme/{{ .Name }}.git",
			"Codeberg",
			"codeberg",
		},
		{"self hosted GitLab", "gitlab", "gitlab", "https://git.example/acme/{{ .Name }}.git", "GitLab", "gitlab"},
		{
			"custom GitHub alias",
			"public-backup",
			"github",
			"https://github.example/acme/{{ .Name }}.git",
			"public-backup",
			"github",
		},
		{
			"custom Codeberg alias",
			"community",
			"forgejo",
			"https://codeberg.org/acme/{{ .Name }}.git",
			"community",
			"codeberg",
		},
		{"unknown git target", "backup", "", "https://git.example/acme/{{ .Name }}.git", "backup", ""},
		{
			"lookalike host is not GitHub",
			"github",
			"",
			"https://github.com.evil.example/acme/{{ .Name }}.git",
			"github",
			"",
		},
		{
			"private Forgejo is not Codeberg",
			"private",
			"forgejo",
			"https://codeberg.example/acme/{{ .Name }}.git",
			"private",
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := config.Load(config.Options{Environ: func() []string { return nil }, Overrides: map[string]any{
				"webhook.secret":  "test-secret",
				"ui.enabled":      true,
				"source.base_url": "https://git.example/",
				"source.token":    "test-source",
				"targets": []map[string]any{
					{"name": tc.alias, "type": tc.typ, "url": tc.url, "token": "test-token"},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			store := status.New(status.Options{})
			recordPlan(t, store, plan.Repo{Name: "acme/app", At: testNow(), Targets: []plan.Target{{Name: tc.alias}}})
			handler, err := ui.Handler(
				ui.Options{Config: cfg, Store: store, Logger: slog.New(slog.DiscardHandler), Actions: &fakeActions{}},
			)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
			body := rec.Body.String()
			badge := regexp.MustCompile(`(?s)<span[^>]*data-forge="` + regexp.QuoteMeta(tc.alias) + `"[^>]*>.*?</span>`).
				FindString(body)
			assertForgeIdentity(t, badge, tc.label, tc.icon)
			if !strings.Contains(body, `<option value="`+tc.alias+`">`+tc.label+`</option>`) {
				t.Error("native target selector lost identity or display name")
			}
		})
	}
}

func assertForgeIdentity(t *testing.T, badge, label, icon string) {
	t.Helper()
	if !strings.Contains(badge, label+"</span>") {
		t.Errorf("missing display label %q: %s", label, badge)
	}
	if icon != "" && !strings.Contains(badge, `src="static/icons/`+icon+`.svg"`) {
		t.Errorf("missing official icon %q: %s", icon, badge)
	}
	if icon == "" && strings.Contains(badge, `<img`) {
		t.Errorf("unknown forge given misleading icon: %s", badge)
	}
}

func TestForgeNamesAndIconsInHistory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	recordPlan(
		t,
		f.store,
		plan.Repo{
			Name:    "acme/app",
			At:      testNow(),
			Apply:   true,
			Targets: []plan.Target{{Name: "github"}, {Name: "gitlab"}, {Name: "codeberg"}},
		},
	)
	rec := f.do(t, http.MethodGet, "/history?repo=acme/app")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	body := rec.Body.String()
	for _, brand := range []struct{ name, label string }{{"github", "GitHub"}, {"gitlab", "GitLab"}, {"codeberg", "Codeberg"}} {
		found := false
		for _, heading := range regexp.MustCompile(`(?s)<h3>.*?</h3>`).FindAllString(body, -1) {
			if strings.Contains(heading, brand.label) &&
				strings.Contains(heading, `src="static/icons/`+brand.name+`.svg"`) {
				found = true
			}
		}
		if !found {
			t.Errorf("history missing branded heading for %s", brand.label)
		}
	}
}

func TestOfficialForgeIconsAreServedLocally(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, name := range []string{"github", "gitlab", "codeberg", "codeberg-white"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := f.do(t, http.MethodGet, "/static/icons/"+name+".svg")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "image/svg+xml") ||
				!strings.Contains(rec.Body.String(), "<svg") {
				t.Errorf("icon response: status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
			}
		})
	}
}
