//go:build e2e

// Package e2e_test runs the forgemirror binary against a real Forgejo: one
// instance plays both the source (org "src") and a typed target (org "dst").
// It is started by `mise run test:e2e`, which provides the environment read
// in loadEnv; the build tag keeps it out of `mise run check`.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	repoDescription = "forgemirror end-to-end fixture"
	bugColor        = "d73a4a"
	docsColor       = "0075ca"
	ciColor         = "e4e669"
	tagName         = "v0.1.0"

	pushDeadline     = 30 * time.Second
	scheduleDeadline = 10 * time.Second
	deleteDeadline   = 30 * time.Second
	pollInterval     = 500 * time.Millisecond
)

// env is what `mise run test:e2e` hands over.
type env struct {
	forgejoURL  string
	token       string
	user        string
	webhookHost string
	binary      string
}

func loadEnv(t *testing.T) env {
	t.Helper()

	e := env{
		forgejoURL:  strings.TrimRight(os.Getenv("E2E_FORGEJO_URL"), "/"),
		token:       os.Getenv("E2E_FORGEJO_TOKEN"),
		user:        os.Getenv("E2E_FORGEJO_USER"),
		webhookHost: os.Getenv("E2E_WEBHOOK_HOST"),
		binary:      os.Getenv("E2E_FORGEMIRROR_BINARY"),
	}
	if e.forgejoURL == "" || e.token == "" || e.user == "" || e.webhookHost == "" || e.binary == "" {
		t.Fatal("E2E_FORGEJO_URL, E2E_FORGEJO_TOKEN, E2E_FORGEJO_USER, E2E_WEBHOOK_HOST and " +
			"E2E_FORGEMIRROR_BINARY must be set; run `mise run test:e2e`")
	}

	return e
}

// TestEndToEnd is one scenario: each subtest builds on the state the
// previous one left behind, so they run in order and stop at the first
// failure.
func TestEndToEnd(t *testing.T) {
	t.Parallel()

	e := loadEnv(t)
	api := &forgejoAPI{
		baseURL: e.forgejoURL + "/api/v1",
		token:   e.token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	// Random per run, so no secret value lives in the repository.
	secret := rand.Text()

	setupSource(t, api)
	fm := startForgemirror(t, e, secret)

	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "push", run: func(t *testing.T) { testPush(t, e, api, fm, secret) }},
		{name: "schedule", run: func(t *testing.T) { testSchedule(t, api) }},
		{name: "delete", run: func(t *testing.T) { testDelete(t, e, api, fm, secret) }},
	}
	for _, step := range steps {
		if !t.Run(step.name, step.run) {
			t.FailNow()
		}
	}
}

func setupSource(t *testing.T, api *forgejoAPI) {
	t.Helper()

	for _, org := range []string{"src", "dst"} {
		api.mustDo(t, http.MethodPost, "/orgs", map[string]any{"username": org, "visibility": "public"})
	}
	api.mustDo(t, http.MethodPost, "/orgs/src/repos", map[string]any{
		"name":        "app",
		"private":     true,
		"description": repoDescription,
	})
	api.mustDo(t, http.MethodPatch, "/repos/src/app", map[string]any{"has_wiki": false})
	api.mustDo(t, http.MethodPut, "/repos/src/app/topics", map[string]any{"topics": []string{"go"}})
	for name, color := range map[string]string{"bug": bugColor, "docs": docsColor} {
		api.mustDo(t, http.MethodPost, "/repos/src/app/labels", map[string]any{"name": name, "color": "#" + color})
	}
}

func testPush(t *testing.T, e env, api *forgejoAPI, fm *forgemirror, secret string) {
	accepted := fm.acceptedWebhooks(t)
	api.mustDo(t, http.MethodPost, "/repos/src/app/hooks", hookBody(fm.webhookURL(e.webhookHost), secret, "push"))

	sha := pushCommitAndTag(t, e)

	eventually(t, pushDeadline, func() error {
		if got := fm.acceptedWebhooks(t); got <= accepted {
			// A scheduled run would mirror the push as well; this keeps the
			// webhook path itself under test.
			return errors.New("forgemirror has not accepted the push webhook")
		}

		return checkMirror(t.Context(), api, sha)
	})
}

func checkMirror(ctx context.Context, api *forgejoAPI, sha string) error {
	var r struct {
		Private     bool   `json:"private"`
		HasIssues   bool   `json:"has_issues"`
		HasWiki     bool   `json:"has_wiki"`
		Description string `json:"description"`
	}
	if err := api.get(ctx, "/repos/dst/app", &r); err != nil {
		return err
	}
	if !r.Private || r.HasIssues || r.HasWiki || r.Description != repoDescription {
		return fmt.Errorf(
			"dst/app settings = %+v, want private, no issues, no wiki, description %q",
			r,
			repoDescription,
		)
	}

	var branch struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := api.get(ctx, "/repos/dst/app/branches/main", &branch); err != nil {
		return err
	}
	var tag struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := api.get(ctx, "/repos/dst/app/tags/"+tagName, &tag); err != nil {
		return err
	}
	if branch.Commit.ID != sha || tag.Commit.SHA != sha {
		return fmt.Errorf("dst/app main = %s, %s = %s, want both %s", branch.Commit.ID, tagName, tag.Commit.SHA, sha)
	}

	var topics struct {
		Topics []string `json:"topics"`
	}
	if err := api.get(ctx, "/repos/dst/app/topics", &topics); err != nil {
		return err
	}
	if !slices.Equal(topics.Topics, []string{"go"}) {
		return fmt.Errorf("dst/app topics = %v, want [go]", topics.Topics)
	}

	return checkLabels(ctx, api, map[string]string{"bug": bugColor, "docs": docsColor})
}

func testSchedule(t *testing.T, api *forgejoAPI) {
	// No push and no webhook: only a scheduled run can carry these changes.
	api.mustDo(t, http.MethodPost, "/repos/src/app/labels", map[string]any{"name": "ci", "color": "#" + ciColor})
	labels, err := api.labels(t.Context(), "src", "app")
	if err != nil {
		t.Fatal(err)
	}
	api.mustDo(t, http.MethodDelete, "/repos/src/app/labels/"+strconv.FormatInt(labels["docs"].ID, 10), nil)

	eventually(t, scheduleDeadline, func() error {
		return checkLabels(t.Context(), api, map[string]string{"bug": bugColor, "ci": ciColor})
	})
}

func testDelete(t *testing.T, e env, api *forgejoAPI, fm *forgemirror, secret string) {
	// Forgejo offers repository (created/deleted) events only on org and
	// system hooks; a repository's own hooks are deleted along with it.
	api.mustDo(t, http.MethodPost, "/orgs/src/hooks", hookBody(fm.webhookURL(e.webhookHost), secret, "repository"))
	api.mustDo(t, http.MethodDelete, "/repos/src/app", nil)

	eventually(t, deleteDeadline, func() error {
		status, err := api.do(t.Context(), http.MethodGet, "/repos/dst/app", nil, nil)
		if status == http.StatusNotFound {
			return nil
		}
		if err != nil {
			return err
		}

		return errors.New("dst/app still exists")
	})
}

// checkLabels compares the target's labels with want (name to color) exactly,
// so a label that should have been pruned fails the check.
func checkLabels(ctx context.Context, api *forgejoAPI, want map[string]string) error {
	got, err := api.labels(ctx, "dst", "app")
	if err != nil {
		return err
	}
	gotColors := make(map[string]string, len(got))
	for name, l := range got {
		gotColors[name] = strings.ToLower(strings.TrimPrefix(l.Color, "#"))
	}
	if len(gotColors) != len(want) {
		return fmt.Errorf("dst/app labels = %v, want %v", gotColors, want)
	}
	for name, color := range want {
		if gotColors[name] != color {
			return fmt.Errorf("dst/app labels = %v, want %v", gotColors, want)
		}
	}

	return nil
}

func hookBody(url, secret, event string) map[string]any {
	return map[string]any{
		"type":   "forgejo",
		"active": true,
		"events": []string{event},
		"config": map[string]string{"url": url, "content_type": "json", "secret": secret},
	}
}

// pushCommitAndTag pushes one commit on main and a tag to src/app and
// returns the commit id.
func pushCommitAndTag(t *testing.T, e env) string {
	t.Helper()

	dir := t.TempDir()
	remote := e.forgejoURL + "/src/app.git"
	basic := base64.StdEncoding.EncodeToString([]byte(e.user + ":" + e.token))
	// The credentials travel in GIT_CONFIG_* variables, not in the remote URL
	// or -c arguments, so they never show up in process lists or git output.
	gitEnv := append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http."+remote+".extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.invalid",
		"GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.invalid",
	)
	git := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}

		return strings.TrimSpace(string(out))
	}

	git("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "--quiet", "--message", "initial commit")
	git("tag", tagName)
	git("push", "--quiet", remote, "main", tagName)

	return git("rev-parse", "HEAD")
}

// eventually retries check until it passes or the deadline expires; the last
// error explains what was still missing.
func eventually(t *testing.T, deadline time.Duration, check func() error) {
	t.Helper()

	start := time.Now()
	for {
		err := check()
		if err == nil {
			t.Logf("converged after %s", time.Since(start).Round(100*time.Millisecond))

			return
		}
		if time.Since(start) > deadline {
			t.Fatalf("not converged within %s: %v", deadline, err)
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-time.After(pollInterval):
		}
	}
}

// forgemirror is the binary under test, running as a subprocess.
type forgemirror struct {
	port int
	logs *syncBuffer
}

func startForgemirror(t *testing.T, e env, secret string) *forgemirror {
	t.Helper()

	dir := t.TempDir()
	port := freePort(t)
	tokenFile := filepath.Join(dir, "target-token")
	if err := os.WriteFile(tokenFile, []byte(e.token), 0o600); err != nil {
		t.Fatal(err)
	}

	// Listening on all interfaces: Forgejo runs in a container and reaches
	// forgemirror through the Docker host gateway, not through loopback.
	config := fmt.Sprintf(`server:
  listen_address: "0.0.0.0:%d"
  shutdown_timeout: 5s
log:
  level: debug
  format: text
source:
  base_url: %q
  username: %q
sync:
  schedule: 2s
filter:
  exclude_private: true
  allow_private: "src/app"
targets:
  - name: dst
    url: "%s/dst/{{ .Name }}.git"
    username: %q
    token_file: %q
    type: forgejo
    create: true
    delete: true
    prune_labels: true
    settings:
      has_issues: false
`, port, e.forgejoURL+"/", e.user, e.forgejoURL, e.user, tokenFile)
	configFile := filepath.Join(dir, "forgemirror.yaml")
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	fm := &forgemirror{port: port, logs: &syncBuffer{}}
	cmd := exec.CommandContext(t.Context(), e.binary, "--config", configFile)
	// Source token and webhook secret come from the environment and the target
	// token from token_file, so none of them is in the config file or in the
	// process arguments.
	cmd.Env = append(os.Environ(), "FORGEMIRROR_SOURCE__TOKEN="+e.token, "FORGEMIRROR_WEBHOOK__SECRET="+secret)
	cmd.Stdout = fm.logs
	cmd.Stderr = fm.logs
	// An interrupt lets forgemirror drain its runs, like a real shutdown.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("forgemirror output:\n%s", fm.logs.String())
		}
	})

	eventually(t, 10*time.Second, func() error {
		status, err := plainGet(t.Context(), fm.url("127.0.0.1")+"/readyz", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("readyz status %d", status)
		}

		return nil
	})

	return fm
}

func (f *forgemirror) url(host string) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(f.port))
}

func (f *forgemirror) webhookURL(host string) string {
	return f.url(host) + "/webhook"
}

var acceptedMetric = regexp.MustCompile(
	`(?m)^forge_mirror_webhook_requests_total\{[^}]*outcome="accepted"[^}]*\} (\d+)`,
)

// acceptedWebhooks reads the accepted-webhook counter from /metrics.
func (f *forgemirror) acceptedWebhooks(t *testing.T) int {
	t.Helper()

	var body bytes.Buffer
	if _, err := plainGet(t.Context(), f.url("127.0.0.1")+"/metrics", &body); err != nil {
		t.Fatal(err)
	}
	m := acceptedMetric.FindSubmatch(body.Bytes())
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}

	return n
}

func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address %v", l.Addr())
	}

	return addr.Port
}

func plainGet(ctx context.Context, url string, body io.Writer) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if body != nil {
		if _, err = io.Copy(body, resp.Body); err != nil {
			return 0, err
		}
	}

	return resp.StatusCode, nil
}

// syncBuffer collects the subprocess output, which os/exec copies from its
// own goroutines.
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

// forgejoAPI is a minimal client for the Forgejo REST API. Plain net/http
// keeps the test independent of the SDK the product code uses.
type forgejoAPI struct {
	baseURL string
	token   string
	client  *http.Client
}

type label struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (a *forgejoAPI) labels(ctx context.Context, owner, name string) (map[string]label, error) {
	var list []label
	if err := a.get(ctx, "/repos/"+owner+"/"+name+"/labels?limit=50", &list); err != nil {
		return nil, err
	}
	byName := make(map[string]label, len(list))
	for _, l := range list {
		byName[l.Name] = l
	}

	return byName, nil
}

func (a *forgejoAPI) get(ctx context.Context, path string, out any) error {
	_, err := a.do(ctx, http.MethodGet, path, nil, out)

	return err
}

func (a *forgejoAPI) mustDo(t *testing.T, method, path string, in any) {
	t.Helper()

	if _, err := a.do(t.Context(), method, path, in, nil); err != nil {
		t.Fatal(err)
	}
}

// do sends one request. Errors name method, path, status and the response
// body, never the token.
func (a *forgejoAPI) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "token "+a.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return resp.StatusCode, fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err = json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}

	return resp.StatusCode, nil
}
