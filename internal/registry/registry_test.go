package registry_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/registry"
)

// newRegistry starts an in-memory registry; 127.0.0.1 makes the library use
// plain HTTP.
func newRegistry(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(ggcrregistry.New())
	t.Cleanup(srv.Close)

	return strings.TrimPrefix(srv.URL, "http://")
}

func push(t *testing.T, ref string, img v1.Image, idx v1.ImageIndex) string {
	t.Helper()

	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	var d v1.Hash
	if idx != nil {
		err = remote.WriteIndex(tag, idx)
		d, _ = idx.Digest()
	} else {
		err = remote.Write(tag, img)
		d, _ = img.Digest()
	}
	if err != nil {
		t.Fatalf("pushing %s: %v", ref, err)
	}

	return d.String()
}

func TestCopyKeepsDigests(t *testing.T) {
	t.Parallel()

	srcHost, dstHost := newRegistry(t), newRegistry(t)
	img, err := random.Image(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := random.Index(128, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	imgDigest := push(t, srcHost+"/acme/app:1.0.0", img, nil)
	idxDigest := push(t, srcHost+"/acme/app:latest", nil, idx)

	c := registry.New()
	ctx := t.Context()
	src := registry.Repository{Host: srcHost, Path: "acme/app", Username: "u", Password: config.Secret("p")}
	dst := registry.Repository{Host: dstHost, Path: "mirror/app"}

	before, err := c.Digests(ctx, dst, []string{"1.0.0", "latest"}, false)
	if err != nil || len(before) != 0 {
		t.Fatalf("Digests() on an empty target = %v, %v; want empty", before, err)
	}

	for _, tag := range []string{"1.0.0", "latest"} {
		if err = c.Copy(ctx, src, dst, tag); err != nil {
			t.Fatalf("Copy(%s) error = %v", tag, err)
		}
	}

	got, err := c.Digests(ctx, dst, []string{"1.0.0", "latest", "missing"}, false)
	if err != nil {
		t.Fatalf("Digests() error = %v", err)
	}
	if got["1.0.0"] != imgDigest || got["latest"] != idxDigest || len(got) != 2 {
		t.Errorf("Digests() = %v, want 1.0.0=%s latest=%s", got, imgDigest, idxDigest)
	}
}

func TestCopyMissingSource(t *testing.T) {
	t.Parallel()

	host := newRegistry(t)
	repo := registry.Repository{Host: host, Path: "acme/app"}

	if err := registry.New().Copy(t.Context(), repo, registry.Repository{Host: host, Path: "x/y"}, "nope"); err == nil {
		t.Fatal("Copy() of a missing tag: error = nil, want error")
	}
}

func TestDigestsDenied(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"DENIED","message":"denied"}]}`))
	}))
	t.Cleanup(srv.Close)
	repo := registry.Repository{Host: strings.TrimPrefix(srv.URL, "http://"), Path: "acme/app"}
	c := registry.New()

	got, err := c.Digests(t.Context(), repo, []string{"1"}, true)
	if err != nil || len(got) != 0 {
		t.Errorf("Digests(deniedIsMissing) = %v, %v; want empty, nil", got, err)
	}
	if _, err = c.Digests(t.Context(), repo, []string{"1"}, false); err == nil {
		t.Error("Digests() on a denial: error = nil, want error")
	}
}

func TestRepositoryString(t *testing.T) {
	t.Parallel()

	r := registry.Repository{Host: "ghcr.io", Path: "acme/app", Password: config.Secret("hidden")}
	if got := r.String(); got != "ghcr.io/acme/app" {
		t.Errorf("String() = %q", got)
	}
	if _, err := registry.New().
		Digests(t.Context(), registry.Repository{Host: "UPPER CASE", Path: "x"}, []string{"t"}, false); err == nil {
		t.Error("Digests() with an invalid reference: error = nil, want error")
	}
}
