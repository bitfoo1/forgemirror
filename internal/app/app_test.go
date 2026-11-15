package app_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/app"
	"go.bitfoo.de/forgemirror/internal/forge/forgejo"
)

const waitTimeout = 10 * time.Second

const testConfig = `
webhook:
  secret: s
log:
  format: text
targets:
  - name: example
    url: "https://example.com/{{ .FullName }}.git"
    token: x
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "forgemirror.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// syncBuffer is a [bytes.Buffer] safe for the concurrent writes of a running
// server's logger and the test's reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
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

type output struct {
	stdout, stderr string
}

// run executes the command with the given arguments and captures its output.
func run(t *testing.T, args ...string) (output, error) {
	t.Helper()

	var out, errOut syncBuffer
	cmd := app.NewCommand()
	cmd.Writer = &out
	cmd.ErrWriter = &errOut

	err := cmd.Run(t.Context(), append([]string{"forgemirror"}, args...))

	return output{stdout: out.String(), stderr: errOut.String()}, err
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	out, err := run(t, "--version")
	if err != nil {
		t.Fatalf("Run(--version) error = %v", err)
	}
	if !strings.Contains(out.stdout, "forgemirror version") {
		t.Errorf("--version output = %q, want it to contain %q", out.stdout, "forgemirror version")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     string
		wantErr    string
		wantStdout string
	}{
		{name: "valid", config: testConfig, wantStdout: "configuration is valid (1 targets)"},
		{name: "invalid", config: "webhook:\n  secret: s\n", wantErr: "targets"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, "--config", writeConfig(t, tc.config), "validate")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Run(validate) error = %v, want it to contain %q", err, tc.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Run(validate) error = %v", err)
			}
			if !strings.Contains(out.stdout, tc.wantStdout) {
				t.Errorf("stdout = %q, want %q", out.stdout, tc.wantStdout)
			}
		})
	}
}

func TestServeConfigError(t *testing.T) {
	t.Parallel()

	_, err := run(t, "--config", filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Run() error = nil, want missing config file error")
	}
}

func TestServeListenError(t *testing.T) {
	t.Parallel()

	_, err := run(t, "--config", writeConfig(t, testConfig), "--listen-address", "256.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "listening on") {
		t.Errorf("Run() error = %v, want listen error", err)
	}
}

func waitForLog(t *testing.T, logs *syncBuffer, pattern *regexp.Regexp) []string {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if m := pattern.FindStringSubmatch(logs.String()); m != nil {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log never matched %s:\n%s", pattern, logs.String())

	return nil
}

func scrapeMetrics(ctx context.Context, t *testing.T, addr string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/metrics", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// assertMetrics polls because the "mirror finished" log line is written
// before the run's deferred bookkeeping (active gauge) has completed.
func assertMetrics(ctx context.Context, t *testing.T, addr string, want ...string) {
	t.Helper()

	var body string
	for deadline := time.Now().Add(waitTimeout); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		body = scrapeMetrics(ctx, t, addr)
		if containsAll(body, want) {
			return
		}
	}
	t.Fatalf("metrics never contained all of %q:\n%s", want, body)
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}

	return true
}

// TestEndToEnd runs the real command against a fake git binary: a signed
// push event must lead to a clone and a push, and cancellation must shut the
// service down cleanly.
func TestEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitLog := filepath.Join(dir, "git.log")
	fakeGit := filepath.Join(dir, "git")
	script := `#!/bin/sh
echo "$*" >>"` + gitLog + `"
if [ "$1" = clone ]; then for last; do :; done; mkdir -p "$last"; fi
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := writeConfig(t, testConfig+`
server:
  listen_address: "127.0.0.1:0"
  shutdown_timeout: 5s
mirror:
  git_binary: `+fakeGit+`
  work_dir: `+dir+`
`)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var logs syncBuffer
	cmd := app.NewCommand()
	cmd.ErrWriter = &logs
	done := make(chan error, 1)
	go func() { done <- cmd.Run(ctx, []string{"forgemirror", "--config", cfg}) }()

	addr := waitForLog(t, &logs, regexp.MustCompile(`msg="http server listening" address=(\S+)`))[1]

	body := `{"ref":"refs/heads/main","repository":{"name":"app","full_name":"acme/app",` +
		`"owner":{"login":"acme"},"clone_url":"https://git.example/acme/app.git"}}`
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write([]byte(body))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/webhook", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forgejo-Event", "push")
	req.Header.Set("X-Forgejo-Signature", hex.EncodeToString(mac.Sum(nil)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /webhook: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /webhook status = %d, want 202", resp.StatusCode)
	}

	waitForLog(t, &logs, regexp.MustCompile(`msg="mirror finished"`))
	assertMetrics(ctx, t, addr,
		`forge_mirror_operations_total{status="success",target="example"} 1`,
		`forge_mirror_webhook_requests_total{outcome="accepted"} 1`,
		`forge_mirror_active_operations 0`,
	)
	cancel()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run() error = %v, want clean shutdown", runErr)
		}
	case <-time.After(waitTimeout):
		t.Fatal("service did not shut down")
	}

	gitCalls, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"clone --mirror --quiet -- https://git.example/acme/app.git",
		"push --prune --quiet https://example.com/acme/app.git",
	} {
		if !strings.Contains(string(gitCalls), want) {
			t.Errorf("git calls missing %q:\n%s", want, gitCalls)
		}
	}
	if !strings.Contains(logs.String(), "forgemirror stopped") {
		t.Errorf("logs missing shutdown message:\n%s", logs.String())
	}
}

func TestFlagOverridesLogging(t *testing.T) {
	t.Parallel()

	// An occupied port makes serve fail right after logging is configured;
	// at level error the startup info line must not appear.
	out, _ := run(t, "-c", writeConfig(t, testConfig), "--log-format", "json", "--log-level", "error",
		"--listen-address", "256.0.0.1:0")
	if strings.Contains(out.stderr, "forgemirror starting") {
		t.Errorf("log output = %q, want no info lines at level error", out.stderr)
	}
}

// forgeFake serves the Forgejo source API and a GitHub Enterprise target API
// from one server and records the requests.
type forgeFake struct {
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []string
	created bool
	// down makes every request fail, like an unreachable Forgejo.
	down atomic.Bool
}

func newForgeFake(t *testing.T) *forgeFake {
	t.Helper()

	routes := map[string]string{
		"GET /api/v1/version":               `{"version":"15.0.0+gitea-1.22.0"}`,
		"GET /api/v1/repos/acme/app":        `{"name":"app","description":"the app","default_branch":"main"}`,
		"GET /api/v1/repos/acme/app/topics": `{"topics":[]}`,
		"GET /api/v1/repos/acme/app/labels": `[]`,
		"GET /api/v1/repos/search": `{"ok":true,"data":[{"name":"app","full_name":"acme/app",` +
			`"owner":{"login":"acme"},"clone_url":"https://git.example/acme/app.git"}]}`,
		"GET /api/v3/users/acme":            `{"login":"acme","type":"Organization"}`,
		"POST /api/v3/orgs/acme/repos":      `{}`,
		"PATCH /api/v3/repos/acme/app":      `{}`,
		"GET /api/v3/repos/acme/app/labels": `[]`,
	}
	f := &forgeFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, `{"message":"unavailable"}`, http.StatusServiceUnavailable)

			return
		}
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.calls = append(f.calls, key)
		f.mu.Unlock()

		body, ok := routes[key]
		f.mu.Lock()
		switch key {
		case "POST /api/v3/orgs/acme/repos":
			f.created = true
		case "GET /api/v3/repos/acme/app":
			// The target repository exists only once it was created.
			body, ok = `{"name":"app","description":"the app"}`, f.created
		}
		f.mu.Unlock()
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *forgeFake) called(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Contains(f.calls, key)
}

// fakeGitBinary writes a git stand-in that logs its arguments and creates
// the clone directory.
func fakeGitBinary(t *testing.T, dir string) (binary, logFile string) { //nolint:nonamedreturns // documents the pair
	t.Helper()

	logFile = filepath.Join(dir, "git.log")
	binary = filepath.Join(dir, "git")
	script := `#!/bin/sh
echo "$*" >>"` + logFile + `"
if [ "$1" = clone ]; then for last; do :; done; mkdir -p "$last"; fi
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return binary, logFile
}

func postPush(ctx context.Context, t *testing.T, addr, body string) {
	t.Helper()

	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write([]byte(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/webhook", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forgejo-Event", "push")
	req.Header.Set("X-Forgejo-Signature", hex.EncodeToString(mac.Sum(nil)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /webhook: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /webhook status = %d, want 202", resp.StatusCode)
	}
}

func typedConfig(t *testing.T, f *forgeFake, fakeGit, dir string) string {
	t.Helper()

	return writeConfig(t, `
webhook:
  secret: s
log:
  format: text
source:
  base_url: `+f.srv.URL+`
  token: src-token
targets:
  - name: gh
    type: github
    url: "`+f.srv.URL+`/acme/{{ .Name }}.git"
    token: gh-token
    create: true
server:
  listen_address: "127.0.0.1:0"
  shutdown_timeout: 5s
mirror:
  git_binary: `+fakeGit+`
  work_dir: `+dir+`
`)
}

// service is a running forgemirror command.
type service struct {
	addr   string
	logs   *syncBuffer
	cancel context.CancelFunc
	done   chan error
}

func startService(ctx context.Context, t *testing.T, cfg string) *service {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	s := &service{logs: &syncBuffer{}, cancel: cancel, done: make(chan error, 1)}
	cmd := app.NewCommand()
	cmd.ErrWriter = s.logs
	go func() { s.done <- cmd.Run(ctx, []string{"forgemirror", "--config", cfg}) }()

	s.addr = waitForLog(t, s.logs, regexp.MustCompile(`msg="http server listening" address=(\S+)`))[1]

	return s
}

func (s *service) stop(t *testing.T) {
	t.Helper()

	s.cancel()
	select {
	case runErr := <-s.done:
		if runErr != nil {
			t.Fatalf("Run() error = %v, want clean shutdown", runErr)
		}
	case <-time.After(waitTimeout):
		t.Fatal("service did not shut down")
	}
}

func pushBody(f *forgeFake) string {
	return `{"ref":"refs/heads/main","repository":{"name":"app","full_name":"acme/app",` +
		`"owner":{"login":"acme"},"clone_url":"` + f.srv.URL + `/acme/app.git"}}`
}

// TestEndToEndTypedTarget: a push for a repository missing on a typed target
// creates it through the API, pushes, then syncs the settings.
func TestEndToEndTypedTarget(t *testing.T) {
	t.Parallel()

	f := newForgeFake(t)
	dir := t.TempDir()
	fakeGit, gitLog := fakeGitBinary(t, dir)
	ctx := t.Context()

	svc := startService(ctx, t, typedConfig(t, f, fakeGit, dir))
	postPush(ctx, t, svc.addr, pushBody(f))

	waitForLog(t, svc.logs, regexp.MustCompile(`msg="mirror finished"`))
	assertMetrics(ctx, t, svc.addr,
		`forgemirror_sync_operations_total{operation="create",status="success",target="gh"} 1`,
		`forgemirror_sync_operations_total{operation="settings",status="success",target="gh"} 1`,
		`forge_mirror_operations_total{status="success",target="gh"} 1`,
	)
	svc.stop(t)

	for _, key := range []string{
		"POST /api/v3/orgs/acme/repos", "PATCH /api/v3/repos/acme/app",
	} {
		if !f.called(key) {
			t.Errorf("API call %q missing", key)
		}
	}
	gitCalls, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	if want := "push --prune --quiet " + f.srv.URL + "/acme/app.git"; !strings.Contains(string(gitCalls), want) {
		t.Errorf("git calls missing %q:\n%s", want, gitCalls)
	}
	if logs := svc.logs.String(); strings.Contains(logs, "gh-token") || strings.Contains(logs, "src-token") {
		t.Errorf("logs contain a token:\n%s", logs)
	}
}

// TestStartsWhileForgejoUnreachable: startup must not depend on Forgejo
// (Kubernetes would crash-loop); the first run fails cleanly and a run after
// the failure backoff connects once Forgejo is back.
func TestStartsWhileForgejoUnreachable(t *testing.T) {
	t.Parallel()

	f := newForgeFake(t)
	f.down.Store(true)
	dir := t.TempDir()
	fakeGit, _ := fakeGitBinary(t, dir)
	ctx := t.Context()

	svc := startService(ctx, t, typedConfig(t, f, fakeGit, dir))
	postPush(ctx, t, svc.addr, pushBody(f))
	waitForLog(t, svc.logs, regexp.MustCompile(`msg="mirror finished with errors".*reading source repository`))
	assertMetrics(ctx, t, svc.addr, `forge_mirror_operations_total{status="failure",target="gh"} 1`)

	f.down.Store(false)
	// Within the backoff window the cached connect error is returned without
	// a new probe, so the recovery is only visible after it.
	time.Sleep(forgejo.DefaultFailureBackoff)
	postPush(ctx, t, svc.addr, pushBody(f))
	waitForLog(t, svc.logs, regexp.MustCompile(`msg="mirror finished"`))
	assertMetrics(ctx, t, svc.addr,
		`forgemirror_sync_operations_total{operation="create",status="success",target="gh"} 1`,
		`forge_mirror_operations_total{status="success",target="gh"} 1`,
	)
	svc.stop(t)
}

// TestScheduledSync: with only a schedule (no webhook secret, push events
// off) the service lists the source repositories at startup and mirrors
// them, and stops cleanly while the scheduler waits for the next tick.
func TestScheduledSync(t *testing.T) {
	t.Parallel()

	f := newForgeFake(t)
	dir := t.TempDir()
	fakeGit, gitLog := fakeGitBinary(t, dir)
	// The source has a branch the target lacks; with equal (empty) refs the
	// scheduled run would rightly skip clone and push.
	script := `#!/bin/sh
echo "$*" >>"` + gitLog + `"
case "$1 $*" in
ls-remote*git.example*) printf 'abc123\trefs/heads/main\n' ;;
clone*) for last; do :; done; mkdir -p "$last" ;;
esac
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	svc := startService(ctx, t, writeConfig(t, `
log:
  format: text
source:
  base_url: `+f.srv.URL+`
  token: src-token
sync:
  on_push: false
  schedule: 1h
targets:
  - name: example
    url: "https://example.com/{{ .FullName }}.git"
    token: x
server:
  listen_address: "127.0.0.1:0"
  shutdown_timeout: 5s
mirror:
  git_binary: `+fakeGit+`
  work_dir: `+dir+`
`))

	waitForLog(t, svc.logs, regexp.MustCompile(`msg="mirror finished" repository=acme/app.*delivery=schedule-`))
	assertMetrics(ctx, t, svc.addr,
		`forgemirror_scheduled_runs_total{status="success"} 1`,
		`forge_mirror_operations_total{status="success",target="example"} 1`,
	)
	svc.stop(t)

	if !f.called("GET /api/v1/repos/search") {
		t.Error("source repositories were not listed")
	}
	gitCalls, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	if want := "push --prune --quiet https://example.com/acme/app.git"; !strings.Contains(string(gitCalls), want) {
		t.Errorf("git calls missing %q:\n%s", want, gitCalls)
	}
}
