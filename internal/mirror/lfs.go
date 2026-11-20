package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckLFS fails before accepting work if the runtime dependency is missing.
func (g *Git) CheckLFS(ctx context.Context) error {
	if _, err := g.output(ctx, "", Remote{}, "lfs", "version"); err != nil {
		return errors.New("Git LFS is required; install git-lfs alongside git and ensure it is on PATH")
	}
	return nil
}

// FetchLFS implements Workspace. Refs are passed on stdin to avoid both argv
// limits and accidentally including hidden refs from the bare mirror clone.
func (w *gitWorkspace) FetchLFS(ctx context.Context, src Remote) error {
	refs, err := w.git.output(ctx, w.dir, Remote{}, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/tags/")
	if err != nil {
		return errors.New("could not enumerate published refs for LFS")
	}
	w.lfsRefs = refs
	if strings.TrimSpace(refs) == "" {
		return nil
	}
	storage := filepath.Join(w.dir, "lfs")
	if err = os.MkdirAll(storage, 0o700); err != nil {
		return errors.New("could not prepare LFS storage")
	}
	// git-lfs panic logs dump GIT_CONFIG_VALUE_* (including auth headers).
	// Occupy its log-directory path with a file so it cannot persist diagnostics.
	if err = os.WriteFile(filepath.Join(storage, "logs"), nil, 0o600); err != nil {
		return errors.New("could not disable LFS diagnostic files")
	}
	return w.transferLFS(ctx, src, "fetch")
}

// PushLFS implements Workspace. FetchLFS must have completed first.
func (w *gitWorkspace) PushLFS(ctx context.Context, dst Remote) error {
	if strings.TrimSpace(w.lfsRefs) == "" {
		return nil
	}
	return w.transferLFS(ctx, dst, "push")
}

func (w *gitWorkspace) transferLFS(ctx context.Context, remote Remote, operation string) error {
	endpoint := strings.TrimSuffix(strings.TrimRight(remote.URL, "/"), ".git") + ".git/info/lfs"
	auth := remote
	auth.URL = endpoint
	const disabled = "false"
	settings := [][2]string{
		{"lfs.url", endpoint},
		{"lfs.pushurl", endpoint},
		{"lfs.concurrenttransfers", "1"},
		{"lfs.basictransfersonly", "true"},
		{"lfs.storage", filepath.Join(w.dir, "lfs")},
		{"lfs.transfer.maxretries", "0"},
		{"lfs.skipdownloaderrors", disabled},
		{"lfs.allowincompletepush", disabled},
		{"lfs.locksverify", disabled},
	}
	_, err := w.git.outputInput(ctx, w.dir, auth, w.lfsRefs, settings, "lfs", operation, "--all", remote.URL, "--stdin")
	if err != nil {
		// Diagnostics can include signed storage URLs and server-controlled text;
		// even token redaction cannot make those safe for status, logs or the UI.
		if ctx.Err() != nil {
			return fmt.Errorf("LFS %s cancelled: %w", operation, ctx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("LFS %s timed out: %w", operation, context.DeadlineExceeded)
		}
		return fmt.Errorf(
			"LFS %s failed; check endpoint support, credentials, quota and source object availability",
			operation,
		)
	}
	return nil
}
