package mirror_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/mirror"
)

func TestCheckLFS(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "missing"}[fail], func(t *testing.T) {
			t.Parallel()
			body := "exit 0"
			if fail {
				body = "exit 1"
			}
			binary, _ := fakeGit(t, body)
			if err := newGit(binary, t.TempDir(), time.Minute).CheckLFS(t.Context()); (err != nil) != fail {
				t.Fatalf("dependency check error=%v", err)
			}
		})
	}
}

func TestLFSTransfers(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, refs string
		fail       bool
	}{
		{name: "published refs", refs: "refs/heads/main\nrefs/tags/v1\n"},
		{name: "empty repository"},
		{name: "failed transfer", refs: "refs/heads/main\n", fail: true},
	} {
		t.Run(tt.name, func(t *testing.T) { t.Parallel(); testLFSTransfer(t, tt.refs, tt.fail) })
	}
}

func testLFSTransfer(t *testing.T, refs string, fail bool) {
	t.Helper()
	failure := "exit 0"
	if fail {
		failure = "echo token-and-signed-url >&2; exit 1"
	}
	body := "case \"$*\" in\n *for-each-ref*) printf '" + refs + "';;\n *'lfs fetch'*) " + failure + ";;\nesac\nexit 0"
	binary, logFile := fakeGit(t, body)
	dir := t.TempDir()
	g := newGit(binary, dir, time.Minute, "LFS_STANDALONE_TRANSFER_AGENT=evil", "GIT_TRACE=1")
	ws, err := g.Clone(t.Context(), mirror.Remote{URL: "https://source.example/a/r.git", Token: "source-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := ws.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	err = ws.FetchLFS(t.Context(), mirror.Remote{URL: "https://source.example/a/r.git", Token: "source-token"})
	if (err != nil) != fail {
		t.Fatalf("FetchLFS error=%v", err)
	}
	if fail {
		if strings.Contains(err.Error(), "token-and-signed-url") {
			t.Fatal("raw diagnostics leaked")
		}
		return
	}
	if err = ws.PushLFS(
		t.Context(),
		mirror.Remote{URL: "https://target.example/a/r.git", Token: "target-token"},
	); err != nil {
		t.Fatal(err)
	}
	assertLFSTransferLog(t, readLog(t, logFile), refs != "")
	if refs != "" {
		assertNoLFSLogs(t, dir)
	}
}

func assertLFSTransferLog(t *testing.T, log string, hasRefs bool) {
	t.Helper()
	if !hasRefs {
		if strings.Contains(log, "ARGS lfs fetch") || strings.Contains(log, "ARGS lfs push") {
			t.Fatal("empty repository contacted LFS")
		}
		return
	}
	for _, want := range []string{"lfs fetch --all", "lfs push --all", "lfs.url", "lfs.concurrenttransfers", "lfs.basictransfersonly"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in %s", want, log)
		}
	}
	if strings.Contains(log, "GIT_TRACE=") {
		t.Fatal("trace environment inherited")
	}
}

func assertNoLFSLogs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, entries[0].Name(), "repo.git", "lfs", "logs"))
	if err != nil || info.IsDir() {
		t.Fatalf("diagnostic logs not blocked: %v", err)
	}
}
