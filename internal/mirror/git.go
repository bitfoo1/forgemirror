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
	"runtime"
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
	lfsRefs   string
}

// Push implements [Workspace].
func (w *gitWorkspace) Push(ctx context.Context, dst Remote) ([]RefUpdate, error) {
	// --porcelain reports every ref on stdout for the change log; --quiet
	// would suppress exactly those lines. Progress is not shown either way,
	// because stderr is not a terminal.
	args := append([]string{"push", "--prune", "--porcelain", dst.URL}, pushRefspecs()...)
	out, err := w.git.output(ctx, w.dir, dst, args...)
	if err != nil {
		return nil, fmt.Errorf("pushing to %s: %w", redactURL(dst.URL), err)
	}

	return parsePorcelain(out), nil
}

// porcelainFields is the number of tab-separated fields of a ref line.
const porcelainFields = 3

// parsePorcelain reads the ref lines of `git push --porcelain`
// ("<flag>\t<from>:<to>\t<summary>"). Up-to-date refs ("=") and the "To"
// and "Done" lines are skipped; rejected refs fail the push before this.
func parsePorcelain(out string) []RefUpdate {
	kinds := map[byte]string{'*': RefCreated, ' ': RefUpdated, '+': RefForced, '-': RefDeleted}

	var updates []RefUpdate
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		fields := strings.SplitN(line, "\t", porcelainFields)
		if len(fields) != porcelainFields || len(fields[0]) != 1 {
			continue
		}
		kind, ok := kinds[fields[0][0]]
		if !ok {
			continue
		}
		_, to, _ := strings.Cut(fields[1], ":")
		updates = append(updates, RefUpdate{Ref: to, Kind: kind, Summary: fields[2]})
	}

	return updates
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

// Refs implements [Mirrorer].
func (g *Git) Refs(ctx context.Context, r Remote) (map[string]string, error) {
	// Only heads and tags: refs/pull/* are never mirrored, so they must not
	// make an otherwise identical target look out of sync.
	out, err := g.output(ctx, "", r, "ls-remote", "--heads", "--tags", "--", r.URL)
	if err != nil {
		return nil, fmt.Errorf("listing refs of %s: %w", redactURL(r.URL), err)
	}

	refs := make(map[string]string)
	for line := range strings.Lines(out) {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		// Peeled entries (^{}) repeat an annotated tag's target commit; the tag
		// object id alone already identifies the tag.
		if !ok || strings.HasSuffix(name, "^{}") {
			continue
		}
		refs[name] = id
	}

	return refs, nil
}

func (g *Git) run(ctx context.Context, dir string, remote Remote, args ...string) error {
	_, err := g.output(ctx, dir, remote, args...)

	return err
}

// output runs git and returns stdout. Stderr is kept apart so that warnings
// cannot corrupt parsed output; it only appears in errors.
func (g *Git) output(ctx context.Context, dir string, remote Remote, args ...string) (string, error) {
	return g.outputInput(ctx, dir, remote, "", nil, args...)
}

func (g *Git) outputInput(
	ctx context.Context,
	dir string,
	remote Remote,
	input string,
	settings [][2]string,
	args ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, g.opts.Timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	// The binary is operator configuration (mirror.git_binary) and the
	// arguments are fixed flags plus validated URLs; no shell is involved.
	cmd := exec.CommandContext(ctx, g.opts.Binary, args...) //nolint:gosec // see above
	cmd.Dir = dir
	cmd.Env = g.env(remote)
	if len(settings) != 0 {
		count := 0
		if remote.Token != "" {
			count = 1
		}
		for i, kv := range cmd.Env {
			if strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") {
				cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
				break
			}
		}
		for _, setting := range settings {
			cmd.Env = append(
				cmd.Env,
				fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", count, setting[0]),
				fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", count, setting[1]),
			)
			count++
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", count))
	}
	cmd.Stdin = strings.NewReader(input)
	// Distinct buffers: exec copies each stream in its own goroutine, so a
	// shared buffer would race.
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = g.opts.WaitDelay

	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}

	output := redact(strings.TrimSpace(stderr.String()+stdout.String()), remote)
	if ctxErr := ctx.Err(); ctxErr != nil {
		// Both errors stay inspectable: errors.Is(err, context.DeadlineExceeded)
		// as well as *exec.ExitError.
		return "", fmt.Errorf("%w: %w: %s", ctxErr, err, output)
	}

	return "", fmt.Errorf("%w: %s", err, output)
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
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "LFS_") {
			if currentPath, pathOK := strings.CutPrefix(kv, "PATH="); pathOK {
				for _, entry := range inherited {
					if data, ok := strings.CutPrefix(entry, "KO_DATA_PATH="); ok {
						kv = "PATH=" + filepath.Join(
							data,
							"lfs",
							runtime.GOARCH,
						) + string(
							os.PathListSeparator,
						) + currentPath
						break
					}
				}
			}
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
