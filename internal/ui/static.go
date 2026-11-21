package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

const (
	// immutable lets browsers keep a versioned asset for a year without
	// revalidating: a new build changes the content hash and with it the URL.
	immutable = "public, max-age=31536000, immutable"
	// revalidate covers unversioned requests (icons built in JavaScript, URLs
	// from an older build); the ETag makes the check a cheap 304.
	revalidate = "no-cache"
	// hashBytes of the SHA-256 identify a file version; collisions only cost
	// a stale cache entry, so a short prefix is enough.
	hashBytes = 8
)

// staticAssets serves the embedded files with content-hash validators. The
// HTML itself stays "no-store", so it always names the current asset URLs.
type staticAssets struct {
	files  http.Handler
	hashes map[string]string // path below static/ → content hash
}

func newStaticAssets(fsys fs.FS) (*staticAssets, error) {
	hashes := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		sum := sha256.Sum256(data)
		hashes[path] = hex.EncodeToString(sum[:hashBytes])

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hashing static files: %w", err)
	}

	return &staticAssets{files: http.FileServerFS(fsys), hashes: hashes}, nil
}

// url is the versioned, page-relative URL of a static file. An unknown path
// is an error, so a typo in a template fails rendering instead of 404ing.
func (s *staticAssets) url(path string) (string, error) {
	hash, ok := s.hashes[path]
	if !ok {
		return "", fmt.Errorf("unknown static asset %q", path)
	}

	return "static/" + path + "?v=" + hash, nil
}

// ServeHTTP expects the "/static/" prefix to be stripped already.
func (s *staticAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if hash, ok := s.hashes[strings.TrimPrefix(r.URL.Path, "/")]; ok {
		w.Header().Set("ETag", `"`+hash+`"`)
		if r.URL.Query().Get("v") == hash {
			w.Header().Set("Cache-Control", immutable)
		} else {
			w.Header().Set("Cache-Control", revalidate)
		}
	}
	s.files.ServeHTTP(w, r)
}
