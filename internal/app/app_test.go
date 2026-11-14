package app_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/app"
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
