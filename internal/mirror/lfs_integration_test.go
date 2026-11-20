package mirror_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/mirror"
)

func TestLFSWithRealCLI(t *testing.T) {
	t.Parallel()
	content := "historical LFS content\n"
	oid := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	var uploaded, downloaded atomic.Int32
	storage := newLFSStorage(t, content, &uploaded, &downloaded)
	defer storage.Close()
	var missing atomic.Bool
	batch := newLFSBatch(t, oid, storage.URL, &missing)
	defer batch.Close()
	work := lfsSourceFixture(t, oid, len(content))
	testRealLFSTransfer(t, work, batch.URL, &missing, &uploaded, &downloaded)
}

func newLFSStorage(t *testing.T, content string, uploaded, downloaded *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("forge credentials reached object storage")
		}
		switch r.Method {
		case http.MethodGet:
			downloaded.Add(1)
			_, _ = io.WriteString(w, content)
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != content {
				t.Error("incorrect uploaded content")
			}
			uploaded.Add(1)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func newLFSBatch(t *testing.T, oid, storageURL string, missing *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/objects/batch") {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Operation string `json:"operation"`
			Objects   []struct {
				OID  string `json:"oid"`
				Size int    `json:"size"`
			} `json:"objects"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "bot" || (token != "source-token" && token != "target-token") {
			t.Error("missing scoped LFS auth")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		objects := make([]map[string]any, 0, len(req.Objects))
		for _, o := range req.Objects {
			if o.OID != oid {
				t.Errorf("hidden-ref object requested: %s", o.OID)
			}
			result := map[string]any{"oid": o.OID, "size": o.Size}
			if missing.Load() {
				result["error"] = map[string]any{"code": 404, "message": "missing"}
			} else {
				result["actions"] = map[string]any{req.Operation: map[string]any{"href": storageURL + "/object"}}
			}
			objects = append(objects, result)
		}
		w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "objects": objects})
	}))
}

func lfsSourceFixture(t *testing.T, oid string, size int) string {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	gitCmd(t, root, "init", "--quiet", "--initial-branch=main", work)
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:" + oid + fmt.Sprintf(
		"\nsize %d\n",
		size,
	)
	if err := os.WriteFile(filepath.Join(work, "large.bin"), []byte(pointer), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(work, ".lfsconfig"),
		[]byte("[lfs]\nurl = https://untrusted.invalid/evil\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, work, "add", ".")
	gitCmd(t, work, "commit", "--quiet", "-m", "LFS pointer")
	gitCmd(t, work, "rm", "--quiet", "large.bin")
	gitCmd(t, work, "commit", "--quiet", "-m", "delete current file")
	gitCmd(t, work, "checkout", "--quiet", "--orphan", "hidden")
	hidden := "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat("a", 64) + "\nsize 99\n"
	if err := os.WriteFile(filepath.Join(work, "hidden.bin"), []byte(hidden), 0o600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, work, "add", ".")
	gitCmd(t, work, "commit", "--quiet", "-m", "hidden pointer")
	gitCmd(t, work, "update-ref", "refs/pull/1/head", "HEAD")
	gitCmd(t, work, "checkout", "--quiet", "main")
	gitCmd(t, work, "branch", "-D", "hidden")
	return work
}

func testRealLFSTransfer(
	t *testing.T,
	work, batchURL string,
	missing *atomic.Bool,
	uploaded, downloaded *atomic.Int32,
) {
	t.Helper()
	workdir := t.TempDir()
	g := mirror.NewGit(mirror.GitOptions{Binary: "git", WorkDir: workdir, Timeout: time.Minute})
	ws, err := g.Clone(t.Context(), mirror.Remote{URL: work})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := ws.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	src := mirror.Remote{URL: batchURL + "/source.git", Username: "bot", Token: "source-token"}
	if err = ws.FetchLFS(t.Context(), src); err != nil {
		t.Fatal(err)
	}
	if err = ws.PushLFS(
		t.Context(),
		mirror.Remote{URL: batchURL + "/target.git", Username: "bot", Token: "target-token"},
	); err != nil {
		t.Fatal(err)
	}
	if downloaded.Load() != 1 || uploaded.Load() != 1 {
		t.Fatalf("downloads=%d uploads=%d", downloaded.Load(), uploaded.Load())
	}
	// A fresh workspace must fail closed when the historical object is missing.
	missing.Store(true)
	bad, err := g.Clone(t.Context(), mirror.Remote{URL: work})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bad.Close() }()
	if err = bad.FetchLFS(t.Context(), src); err == nil {
		t.Fatal("missing historical object accepted")
	}
	assertNoCredentialsOnDisk(t, workdir)
}

func assertNoCredentialsOnDisk(t *testing.T, workdir string) {
	t.Helper()
	if err := filepath.WalkDir(workdir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "source-token") || strings.Contains(string(data), "target-token") {
			t.Errorf("credential persisted in %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
