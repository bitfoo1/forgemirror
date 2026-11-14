package mirror_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/mirror"
)

// gitCmd runs the real git with a hermetic configuration.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func refs(t *testing.T, bareRepo string) []string {
	t.Helper()

	out := gitCmd(t, bareRepo, "for-each-ref", "--format=%(refname)")
	if out == "" {
		return nil
	}

	return strings.Split(out, "\n")
}

// TestMirrorWithRealGit proves the ref semantics against real repositories:
// branches and tags are copied, refs deleted at the source are pruned at the
// target, and refs/pull/* (which GitHub rejects) are left out.
func TestMirrorWithRealGit(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	root := t.TempDir()
	work := filepath.Join(root, "work")
	source := filepath.Join(root, "source.git")
	target := filepath.Join(root, "target.git")

	gitCmd(t, root, "init", "--quiet", "--bare", source)
	gitCmd(t, root, "init", "--quiet", "--bare", target)
	gitCmd(t, root, "init", "--quiet", "--initial-branch=main", work)
	gitCmd(t, work, "commit", "--quiet", "--allow-empty", "-m", "initial")
	gitCmd(t, work, "tag", "v1.0.0")
	gitCmd(t, work, "branch", "feature")
	gitCmd(t, work, "push", "--quiet", source, "main", "feature", "v1.0.0", "HEAD:refs/pull/1/head")

	// The target has a stale branch that no longer exists at the source.
	gitCmd(t, work, "push", "--quiet", target, "HEAD:refs/heads/stale")

	g := mirror.NewGit(mirror.GitOptions{Binary: "git", WorkDir: t.TempDir(), Timeout: time.Minute})
	ws, err := g.Clone(t.Context(), mirror.Remote{URL: source})
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	if pushErr := ws.Push(t.Context(), mirror.Remote{URL: target}); pushErr != nil {
		t.Fatalf("Push() error = %v", pushErr)
	}

	got := refs(t, target)
	want := []string{"refs/heads/feature", "refs/heads/main", "refs/tags/v1.0.0"}
	if !slices.Equal(got, want) {
		t.Errorf("target refs = %v, want %v", got, want)
	}
}

// TestCredentialHeaderScopeWithRealGit proves that git picks up the header
// from the GIT_CONFIG_* environment for the remote's request URLs, and only
// for those: a request to another host must not carry the credentials.
func TestCredentialHeaderScopeWithRealGit(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	remote := mirror.Remote{URL: "https://github.com/acme/app.git", Username: "bot", Token: "secret-token"}
	env := mirror.NewGit(mirror.GitOptions{Binary: "git", Timeout: time.Minute}).Env(remote)

	urlmatch := func(requestURL string) (string, error) {
		cmd := exec.CommandContext(t.Context(), "git", "config", "--get-urlmatch", "http.extraheader", requestURL)
		cmd.Env = env
		out, err := cmd.Output()

		return strings.TrimSpace(string(out)), err
	}

	got, err := urlmatch("https://github.com/acme/app.git/info/refs")
	if err != nil {
		t.Fatalf("git config --get-urlmatch: %v", err)
	}
	if !strings.HasPrefix(got, "Authorization: Basic ") {
		t.Errorf("header for the remote = %q, want Basic auth", got)
	}

	for _, other := range []string{
		"https://evil.example/acme/app.git/info/refs",
		"https://github.com/acme/other.git/info/refs",
	} {
		if leaked, matchErr := urlmatch(other); matchErr == nil || leaked != "" {
			t.Errorf("header leaked to %s: %q", other, leaked)
		}
	}
}
