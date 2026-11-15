package mirror_test

import (
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/mirror"
)

// fakeGit writes a shell script standing in for git. It appends its
// arguments and GIT_* environment to the returned log file, creates the
// clone target directory, then runs body.
func fakeGit(t *testing.T, body string) (binary, logFile string) { //nolint:nonamedreturns // documents the pair
	t.Helper()

	dir := t.TempDir()
	logFile = filepath.Join(dir, "git.log")
	binary = filepath.Join(dir, "git")
	script := `#!/bin/sh
{ echo "ARGS $*"; env | grep '^GIT_' | sort; } >>"` + logFile + `"
if [ "$1" = clone ]; then for last; do :; done; mkdir -p "$last"; fi
` + body + "\n"
	writeExecutable(t, binary, script)

	return binary, logFile
}

// writeExecutable creates an executable script at path without ever holding
// a writable descriptor for it in this process. Parallel tests fork git
// processes; a child forked while the script is open for writing inherits
// that descriptor until it execs, and exec'ing the script then fails with
// "text file busy" on Linux (golang/go#22315). Writing a plain source file
// and letting a short-lived cp create the executable keeps the destination's
// descriptor out of every forked child.
func writeExecutable(t *testing.T, path, content string) {
	t.Helper()

	src := path + ".src"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "cp", src, path).CombinedOutput(); err != nil {
		t.Fatalf("cp %s %s: %v: %s", src, path, err, out)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func newGit(binary, workDir string, timeout time.Duration, environ ...string) *mirror.Git {
	return mirror.NewGit(mirror.GitOptions{
		Binary:    binary,
		WorkDir:   workDir,
		Timeout:   timeout,
		WaitDelay: 100 * time.Millisecond,
		Environ:   func() []string { return append([]string{"PATH=" + os.Getenv("PATH")}, environ...) },
	})
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("work dir %s not cleaned up: %v", dir, entries)
	}
}

func TestCloneAndPushCommandsAndCredentials(t *testing.T) {
	t.Parallel()

	binary, logFile := fakeGit(t, "exit 0")
	workDir := t.TempDir()
	g := newGit(binary, workDir, time.Minute, "GIT_DIR=/evil", "HOME=/home/test")

	src := mirror.Remote{URL: "https://forgejo.example/acme/app.git", Username: "oauth2", Token: "src-token"}
	dst := mirror.Remote{URL: "https://github.com/acme/app.git", Username: "bot", Token: "dst-token"}

	ws, err := g.Clone(t.Context(), src)
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if pushErr := ws.Push(t.Context(), dst); pushErr != nil {
		t.Fatalf("Push() error = %v", pushErr)
	}
	if closeErr := ws.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if closeErr := ws.Close(); closeErr != nil {
		t.Errorf("second Close() error = %v, want nil", closeErr)
	}
	assertEmptyDir(t, workDir)

	log := readLog(t, logFile)
	header := func(user, token string) string {
		return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
	}

	for _, want := range []string{
		"ARGS clone --mirror --quiet -- https://forgejo.example/acme/app.git ",
		"ARGS push --prune --quiet https://github.com/acme/app.git +refs/heads/*:refs/heads/* +refs/tags/*:refs/tags/*",
		"GIT_CONFIG_KEY_0=http.https://forgejo.example/acme/app.git.extraHeader",
		"GIT_CONFIG_VALUE_0=" + header("oauth2", "src-token"),
		"GIT_CONFIG_KEY_0=http.https://github.com/acme/app.git.extraHeader",
		"GIT_CONFIG_VALUE_0=" + header("bot", "dst-token"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("git log missing %q\n%s", want, log)
		}
	}

	if strings.Contains(log, "GIT_DIR=/evil") {
		t.Error("inherited GIT_DIR was passed to git")
	}
	for line := range strings.SplitSeq(log, "\n") {
		if strings.HasPrefix(line, "ARGS") && strings.Contains(line, "token") {
			t.Errorf("credentials in git arguments: %s", line)
		}
	}
}

func TestAnonymousRemoteSendsNoCredentials(t *testing.T) {
	t.Parallel()

	binary, logFile := fakeGit(t, "exit 0")
	g := newGit(binary, t.TempDir(), time.Minute)

	ws, err := g.Clone(t.Context(), mirror.Remote{URL: "https://forgejo.example/public.git"})
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	if log := readLog(t, logFile); strings.Contains(log, "GIT_CONFIG_COUNT") {
		t.Errorf("anonymous clone got credential config:\n%s", log)
	}
}

func TestCloneFailureRedactsAndCleansUp(t *testing.T) {
	t.Parallel()

	// A misbehaving git that echoes the credentials it received.
	binary, _ := fakeGit(t, `echo "fatal: auth failed for $GIT_CONFIG_VALUE_0 token=src-token" >&2; exit 128`)
	workDir := t.TempDir()
	g := newGit(binary, workDir, time.Minute)

	_, err := g.Clone(t.Context(), mirror.Remote{
		URL:      "https://user:pw@forgejo.example/acme/app.git",
		Username: "oauth2",
		Token:    "src-token",
	})
	if err == nil {
		t.Fatal("Clone() error = nil, want error")
	}

	msg := err.Error()
	if strings.Contains(msg, "src-token") || strings.Contains(msg, "pw@") ||
		strings.Contains(msg, base64.StdEncoding.EncodeToString([]byte("oauth2:src-token"))) {
		t.Errorf("error leaks credentials: %s", msg)
	}
	for _, want := range []string{"cloning https://user:xxxxx@forgejo.example/acme/app.git", "fatal: auth failed", "[REDACTED]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 128 {
		t.Errorf("error does not wrap the exit status 128: %v", err)
	}
	assertEmptyDir(t, workDir)
}

func TestPushFailure(t *testing.T) {
	t.Parallel()

	binary, _ := fakeGit(
		t,
		`if [ "$1" = push ]; then echo "! [remote rejected] main (protected branch)" >&2; exit 1; fi`,
	)
	workDir := t.TempDir()
	g := newGit(binary, workDir, time.Minute)

	ws, err := g.Clone(t.Context(), mirror.Remote{URL: "https://forgejo.example/a.git"})
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	err = ws.Push(t.Context(), mirror.Remote{URL: "https://github.com/a.git", Token: "dst-token"})
	if err == nil || !strings.Contains(err.Error(), "pushing to https://github.com/a.git") ||
		!strings.Contains(err.Error(), "protected branch") {
		t.Errorf("Push() error = %v, want wrapped rejection", err)
	}

	if closeErr := ws.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	assertEmptyDir(t, workDir)
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	binary, _ := fakeGit(t, "sleep 30")
	workDir := t.TempDir()
	g := newGit(binary, workDir, 200*time.Millisecond)

	start := time.Now()
	_, err := g.Clone(t.Context(), mirror.Remote{URL: "https://forgejo.example/slow.git"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Clone() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Clone() returned after %v, want shortly after the timeout", elapsed)
	}
	assertEmptyDir(t, workDir)
}

func TestCancelledContext(t *testing.T) {
	t.Parallel()

	binary, _ := fakeGit(t, "sleep 30")
	g := newGit(binary, t.TempDir(), time.Minute)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := g.Clone(ctx, mirror.Remote{URL: "https://forgejo.example/a.git"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Clone() error = %v, want context.Canceled", err)
	}
}

func TestWorkDirError(t *testing.T) {
	t.Parallel()

	g := newGit("git", filepath.Join(t.TempDir(), "missing", "dir"), time.Minute)
	if _, err := g.Clone(t.Context(), mirror.Remote{URL: "https://forgejo.example/a.git"}); err == nil {
		t.Error("Clone() with missing work dir: error = nil, want error")
	}
}

func TestRefsCommandAndParsing(t *testing.T) {
	t.Parallel()

	binary, logFile := fakeGit(
		t,
		`printf 'aaa\trefs/heads/main\nbbb\trefs/tags/v1\nccc\trefs/tags/v1^{}\n'; echo "warning" >&2`,
	)
	g := newGit(binary, t.TempDir(), time.Minute)
	r := mirror.Remote{URL: "https://github.com/acme/app.git", Username: "bot", Token: "dst-token"}

	got, err := g.Refs(t.Context(), r)
	if err != nil {
		t.Fatalf("Refs() error = %v", err)
	}
	want := map[string]string{"refs/heads/main": "aaa", "refs/tags/v1": "bbb"}
	if !maps.Equal(got, want) {
		t.Errorf("Refs() = %v, want %v", got, want)
	}

	log := readLog(t, logFile)
	if !strings.Contains(log, "ARGS ls-remote --heads --tags -- "+r.URL) {
		t.Errorf("unexpected git arguments:\n%s", log)
	}
	if !strings.Contains(log, "GIT_CONFIG_KEY_0=http."+r.URL+".extraHeader") {
		t.Errorf("credential header not scoped to URL:\n%s", log)
	}
}

func TestRefsCredentialNotInArgs(t *testing.T) {
	t.Parallel()

	binary, logFile := fakeGit(t, "exit 0")
	g := newGit(binary, t.TempDir(), time.Minute)

	got, err := g.Refs(t.Context(), mirror.Remote{URL: "https://github.com/a.git", Username: "bot", Token: "dst-token"})
	if err != nil {
		t.Fatalf("Refs() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Refs() = %v, want empty", got)
	}
	for line := range strings.Lines(readLog(t, logFile)) {
		if strings.HasPrefix(line, "ARGS") && strings.Contains(line, "dst-token") {
			t.Errorf("credentials in git arguments: %s", line)
		}
	}
}

func TestRefsFailureRedacts(t *testing.T) {
	t.Parallel()

	binary, _ := fakeGit(t, `echo "fatal: denied dst-token" >&2; exit 128`)
	g := newGit(binary, t.TempDir(), time.Minute)

	_, err := g.Refs(t.Context(), mirror.Remote{URL: "https://github.com/a.git", Token: "dst-token"})
	if err == nil {
		t.Fatal("Refs() error = nil")
	}
	if strings.Contains(err.Error(), "dst-token") {
		t.Errorf("token leaked: %v", err)
	}
}
