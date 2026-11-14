package mirror

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pushRefspecs mirror branches and tags only. `git push --mirror` would also
// push refs/pull/* that Forgejo exposes, which GitHub (among others) rejects
// as hidden refs, failing the entire push.
func pushRefspecs() []string {
	return []string{"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}
}

// defaultWaitDelay bounds how long Wait blocks after the context killed git
// while a grandchild process (credential helper, ssh) still holds the
// output pipes open.
const defaultWaitDelay = 5 * time.Second

// GitOptions configures [Git].
type GitOptions struct {
	// Binary is the git executable; name in PATH or absolute path.
	Binary string
	// WorkDir holds the temporary clones; empty means [os.TempDir].
	WorkDir string
	// Timeout bounds each single git command (one clone or one push).
	Timeout time.Duration
	// WaitDelay overrides the default grace period after a timeout kill.
	WaitDelay time.Duration
	// Environ returns the base environment for git; nil means [os.Environ].
	Environ func() []string
}

// Git implements [Mirrorer] by running the git binary.
type Git struct {
	opts GitOptions
}

var _ Mirrorer = (*Git)(nil)

// NewGit returns a [Git] mirrorer.
func NewGit(opts GitOptions) *Git {
	if opts.WaitDelay == 0 {
		opts.WaitDelay = defaultWaitDelay
	}
	if opts.Environ == nil {
		opts.Environ = os.Environ
	}

	return &Git{opts: opts}
}

// Clone implements [Mirrorer].
func (g *Git) Clone(ctx context.Context, src Remote) (Workspace, error) {
	tmp, err := os.MkdirTemp(g.opts.WorkDir, "forgemirror-*")
	if err != nil {
		return nil, fmt.Errorf("creating work dir: %w", err)
	}
	ws := &gitWorkspace{git: g, tmp: tmp, dir: filepath.Join(tmp, "repo.git")}

	// "--" stops option parsing, so a URL can never be read as a git option.
	if cloneErr := g.run(ctx, tmp, src, "clone", "--mirror", "--quiet", "--", src.URL, ws.dir); cloneErr != nil {
		// Close the workspace here: on error the caller gets no handle to it.
		_ = ws.Close()

		return nil, fmt.Errorf("cloning %s: %w", redactURL(src.URL), cloneErr)
	}

	return ws, nil
}

type gitWorkspace struct {
	git       *Git
	tmp, dir  string
	closeOnce sync.Once
	closeErr  error
}

// Push implements [Workspace].
func (w *gitWorkspace) Push(ctx context.Context, dst Remote) error {
	args := append([]string{"push", "--prune", "--quiet", dst.URL}, pushRefspecs()...)
	if err := w.git.run(ctx, w.dir, dst, args...); err != nil {
		return fmt.Errorf("pushing to %s: %w", redactURL(dst.URL), err)
	}

	return nil
}

// Close implements [Workspace].
func (w *gitWorkspace) Close() error {
	w.closeOnce.Do(func() {
		if err := os.RemoveAll(w.tmp); err != nil {
			w.closeErr = fmt.Errorf("removing work dir: %w", err)
		}
	})

	return w.closeErr
}

func (g *Git) run(ctx context.Context, dir string, remote Remote, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, g.opts.Timeout)
	defer cancel()

	var out bytes.Buffer
	// The binary is operator configuration (mirror.git_binary) and the
	// arguments are fixed flags plus validated URLs; no shell is involved.
	cmd := exec.CommandContext(ctx, g.opts.Binary, args...) //nolint:gosec // see above
	cmd.Dir = dir
	cmd.Env = g.env(remote)
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = g.opts.WaitDelay

	err := cmd.Run()
	if err == nil {
		return nil
	}

	output := redact(strings.TrimSpace(out.String()), remote)
	if ctxErr := ctx.Err(); ctxErr != nil {
		// Both errors stay inspectable: errors.Is(err, context.DeadlineExceeded)
		// as well as *exec.ExitError.
		return fmt.Errorf("%w: %w: %s", ctxErr, err, output)
	}

	return fmt.Errorf("%w: %s", err, output)
}

// env builds git's environment. Credentials travel as an HTTP header scoped
// to the remote's URL via GIT_CONFIG_* variables, which keeps them
//   - off disk: `clone --mirror` would persist a URL with credentials in the
//     clone's config file,
//   - out of argv, which every local user can read in the process list,
//   - away from other hosts, e.g. after a redirect.
func (g *Git) env(remote Remote) []string {
	own := []string{
		"GIT_TERMINAL_PROMPT=0",           // fail instead of waiting for a password forever
		"GIT_CONFIG_NOSYSTEM=1",           // reproducible: ignore host-level git config,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, // including credential helpers
	}
	if remote.Token != "" {
		own = append(own,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+remote.URL+".extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basicAuth(remote),
		)
	}

	inherited := g.opts.Environ()
	env := make([]string, 0, len(inherited)+len(own))
	for _, kv := range inherited {
		// Inherited GIT_* variables (GIT_DIR, GIT_CONFIG_*, …) could redirect
		// or override what forgemirror tells git to do.
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}

	return append(env, own...)
}

func basicAuth(r Remote) string {
	return base64.StdEncoding.EncodeToString([]byte(r.Username + ":" + r.Token.Reveal()))
}

// redact removes the remote's credentials from git output. git does not echo
// extra headers, but output ends up in logs, so this is a second safety net.
func redact(s string, r Remote) string {
	if r.Token == "" {
		return s
	}

	return strings.NewReplacer(
		basicAuth(r), "[REDACTED]",
		r.Token.Reveal(), "[REDACTED]",
	).Replace(s)
}

// redactURL masks credentials a URL might carry anyway (e.g. a clone URL
// from a webhook payload).
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparsable URL>"
	}

	return u.Redacted()
}
