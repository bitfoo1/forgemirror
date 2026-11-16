package ui_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
)

// Secrets are distinctive so a substring check cannot match by accident.
const (
	webhookSecret = "webhook-secret-value-4711"
	sourceToken   = "source-token-value-4712"
	targetToken   = "target-token-value-4713"
)

const testConfig = `
webhook:
  secret: ` + webhookSecret + `
source:
  base_url: https://git.example.org/
  token: ` + sourceToken + `
sync:
  schedule: 1h
ui:
  enabled: true
targets:
  - name: github
    type: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: ` + targetToken + `
  - name: backup
    url: "https://git.example.net/acme/{{ .Name }}.git"
    token: ` + targetToken + `
`

func testNow() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }

type fixture struct {
	handler http.Handler
	store   *status.Store
	ring    *logging.Ring
	logger  *slog.Logger
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	return newFixtureWith(t, nil, nil)
}

// newFixtureWith adds environment variables to the test config (e.g.
// sync.auto) and enables the actions when actions is not nil.
func newFixtureWith(t *testing.T, env []string, actions ui.Actions) *fixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), "forgemirror.yaml")
	if err := os.WriteFile(path, []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{File: path, Environ: func() []string { return env }})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	ring := logging.NewRing(100)
	logger := logging.New(io.Discard, config.Log{Level: slog.LevelDebug}, ring)
	store := status.New(status.Options{ScheduleInterval: time.Hour, Now: testNow})

	h, err := ui.Handler(ui.Options{
		Config:  cfg,
		Store:   store,
		Logs:    ring,
		Queued:  func() map[string]bool { return map[string]bool{"acme/busy": true} },
		Actions: actions,
		Version: "v9.9.9",
		Logger:  logger,
		Now:     func() time.Time { return testNow().Add(time.Minute) },
	})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}

	return &fixture{handler: h, store: store, ring: ring, logger: logger}
}

func (f *fixture) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody))

	return rec
}

// populate records a mirrored repository, a failed target, a scheduled sweep
// and log lines, including a secret passed through the logger.
func (f *fixture) populate(t *testing.T) {
	t.Helper()

	ctx := t.Context()
	f.store.RunStarted(ctx, "acme/app", true)
	f.store.SyncFinished(ctx, "acme/app", "github", "create", nil)
	f.store.TargetFinished(ctx, "acme/app", "github", 1500*time.Millisecond, nil)
	f.store.RunFinished(ctx, "acme/app")
	f.store.RunStarted(ctx, "acme/busy", true)
	f.store.TargetFinished(
		ctx,
		"acme/busy",
		"github",
		time.Second,
		errors.New("pushing to https://github.com/acme/busy.git: <denied>"),
	)
	f.store.ScheduledRunFinished(ctx, 2, nil)

	f.logger.Debug("debug line")
	f.logger.Info("mirror finished", slog.String("repository", "acme/app"))
	f.logger.Warn("careful", slog.Any("token", config.Secret(targetToken)))
	f.logger.Error("target failed", slog.String("target", "github"))
}

func TestRoutes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.populate(t)

	tests := []struct {
		path     string
		want     []string
		wantType string
	}{
		{
			path: "/",
			want: []string{
				"<title>ForgeMirror</title>", "v9.9.9", `src="static/htmx.min.js"`,
				`hx-get="fragments/repos"`, "acme/app", "Configuration", "ops_listen_address: :9090",
				"mirror finished", "every 1h0m0s",
			},
			wantType: "text/html; charset=utf-8",
		},
		{
			path: "/fragments/overview",
			want: []string{
				"Uptime",
				"<code>github</code>",
				"<code>backup</code>",
				// Every accepted target type is shown; configured ones are on.
				`<li class="kind kind-on" title="github">GitHub</li>`,
				`<li class="kind" title="not configured">GitLab</li>`,
				`<li class="kind" title="not configured">Forgejo</li>`,
				`<li class="kind kind-on" title="backup">Git</li>`,
				"every 1h0m0s",
				"2 repositories",
				"1 queued",
			},
			wantType: "text/html; charset=utf-8",
		},
		{
			path: "/fragments/repos",
			want: []string{
				"acme/app", "create:", "in 1.5s", "acme/busy", "syncing", "queued",
				// The error text is escaped, never interpreted as markup.
				"&lt;denied&gt;",
			},
			wantType: "text/html; charset=utf-8",
		},
		{path: "/fragments/logs", want: []string{"debug line", "target failed"}, wantType: "text/html; charset=utf-8"},
		{path: "/static/htmx.min.js", want: []string{"htmx"}, wantType: "text/javascript; charset=utf-8"},
		{path: "/static/style.css", want: []string{"--accent"}, wantType: "text/css; charset=utf-8"},
		{path: "/static/app.js", want: []string{"forgemirror-theme"}, wantType: "text/javascript; charset=utf-8"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			rec := f.do(t, http.MethodGet, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
			}
			for _, want := range tc.want {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body lacks %q:\n%s", want, rec.Body.String())
				}
			}
		})
	}
}

func TestScheduleTileSingular(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.store.ScheduledRunFinished(t.Context(), 1, nil)

	body := f.do(t, http.MethodGet, "/fragments/overview").Body.String()
	if !strings.Contains(body, ": 1 repository ·") {
		t.Errorf("schedule tile lacks %q:\n%s", ": 1 repository ·", body)
	}
}

func TestLogLevelFilter(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.populate(t)

	tests := []struct {
		query     string
		want, not []string
	}{
		{query: "", want: []string{"debug line", "target failed"}},
		{
			query: "?level=warn",
			want:  []string{"careful", "target failed"},
			not:   []string{"debug line", "mirror finished"},
		},
		{query: "?level=error", want: []string{"target failed"}, not: []string{"careful"}},
		{query: "?level=bogus", want: []string{"debug line"}},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()

			body := f.do(t, http.MethodGet, "/fragments/logs"+tc.query).Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %q", want)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(body, not) {
					t.Errorf("body contains %q", not)
				}
			}
		})
	}

	page := f.do(t, http.MethodGet, "/?level=warn").Body.String()
	if !strings.Contains(page, `<option value="warn" selected>`) {
		t.Errorf("page does not preselect the requested level")
	}
}

func TestNoSecretInAnyResponse(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.populate(t)

	for _, path := range []string{"/", "/fragments/overview", "/fragments/repos", "/fragments/logs"} {
		body := f.do(t, http.MethodGet, path).Body.String()
		for _, secret := range []string{webhookSecret, sourceToken, targetToken} {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaks secret %q", path, secret)
			}
		}
	}
}

func TestMethods(t *testing.T) {
	t.Parallel()

	f := newFixtureWith(t, nil, &fakeActions{})
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, path := range []string{"/", "/fragments/repos", "/actions/sync", "/anything"} {
			rec := f.do(t, method, path)
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD, POST" {
				t.Errorf("%s %s = %d (Allow %q), want 405", method, path, rec.Code, rec.Header().Get("Allow"))
			}
		}
	}
	for _, path := range []string{"/", "/fragments/repos"} {
		if rec := f.do(t, http.MethodPost, path); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, rec.Code)
		}
	}
	if rec := f.do(t, http.MethodGet, "/actions/sync"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /actions/sync = %d, want 405", rec.Code)
	}
	if rec := newFixture(t).do(t, http.MethodPost, "/actions/sync"); rec.Code != http.StatusNotFound {
		t.Errorf("POST /actions/sync without actions = %d, want 404", rec.Code)
	}
	if rec := f.do(t, http.MethodHead, "/"); rec.Code != http.StatusOK {
		t.Errorf("HEAD / = %d, want 200", rec.Code)
	}
	if rec := f.do(t, http.MethodGet, "/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for _, path := range []string{"/", "/static/style.css", "/nope"} {
		rec := f.do(t, http.MethodGet, path)
		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
			"Cache-Control":          "no-store",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "script-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", path, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe") {
			t.Errorf("%s: CSP %q allows unsafe sources", path, csp)
		}
	}
}

// TestNoInlineScriptOrStyle keeps the page compatible with the CSP.
func TestNoInlineScriptOrStyle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.populate(t)
	body := f.do(t, http.MethodGet, "/").Body.String()

	if m := regexp.MustCompile(`<script(?:\s[^>]*)?>[^<]+</script>|<style|\sstyle="|\son[a-z]+="`).
		FindString(body); m != "" {
		t.Errorf("page contains inline script or style: %q", m)
	}
}

// TestRelativeURLs keeps the UI working behind a proxy that serves it below
// a path prefix.
func TestRelativeURLs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := f.do(t, http.MethodGet, "/").Body.String()

	if m := regexp.MustCompile(`(?:href|src|action|hx-get)="/`).FindString(body); m != "" {
		t.Errorf("page contains an absolute URL: %q", m)
	}
}

func TestEmptyState(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for path, want := range map[string]string{
		"/fragments/repos":    "No repositories yet",
		"/fragments/logs":     "No log records",
		"/fragments/overview": "first sweep running",
	} {
		// The fixture's logger already logs nothing; the ring is empty.
		if body := f.do(t, http.MethodGet, path).Body.String(); !strings.Contains(body, want) {
			t.Errorf("%s lacks %q:\n%s", path, want, body)
		}
	}
}

func TestVendoredHTMX(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("static/htmx.min.js")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	// Keep in sync with static/README.md (htmx.org 2.0.11).
	const want = "d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("htmx.min.js sha256 = %s, want %s (update static/README.md with the file)", got, want)
	}
	if !bytes.Contains(data, []byte(`version:"2.0.11"`)) {
		t.Error("htmx.min.js is not version 2.0.11")
	}
}
