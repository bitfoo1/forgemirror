package forgejo_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/forgejo"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

const releasesJSON = `[
  {"id": 7, "tag_name": "v1.0.0", "name": "First", "body": "notes", "prerelease": true,
   "created_at": "2026-01-02T03:04:05Z",
   "assets": [{"id": 9, "name": "app.tar.gz", "size": 42, "browser_download_url": "DL/app.tar.gz"}]},
  {"id": 8, "tag_name": "v2.0.0", "name": "", "draft": true, "created_at": "2026-02-02T03:04:05Z"}
]`

func testAddr() forge.Address { return forge.Address{Namespace: "acme", Name: "app"} }

func TestSourceReleases(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app/releases": listReply(releasesJSON, 2),
	})
	src := f.client(t, "").Source().(forge.ReleaseSource)

	got, err := src.Releases(t.Context(), "acme", "app")
	if err != nil {
		t.Fatalf("Releases() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Releases() = %d releases, want 2", len(got))
	}
	first := got[0]
	if first.ID != 7 || first.Tag != "v1.0.0" || first.Name != "First" || first.Body != "notes" ||
		!first.Prerelease || first.Draft || !first.CreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("release = %+v", first)
	}
	if want := (forge.Asset{ID: 9, Name: "app.tar.gz", Size: 42, URL: "DL/app.tar.gz"}); len(first.Assets) != 1 ||
		first.Assets[0] != want {
		t.Errorf("assets = %+v, want [%+v]", first.Assets, want)
	}
	if got[1].Name != "v2.0.0" || !got[1].Draft {
		t.Errorf("untitled draft = %+v, want name from tag and Draft", got[1])
	}
}

func TestSourceOpenAsset(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /acme/app/releases/download/v1.0.0/app.tar.gz": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "binary")
		},
	})
	src := f.client(t, "").Source().(forge.ReleaseSource)

	body, err := src.OpenAsset(t.Context(), forge.Asset{
		Name: "app.tar.gz", URL: f.srv.URL + "/acme/app/releases/download/v1.0.0/app.tar.gz",
	})
	if err != nil {
		t.Fatalf("OpenAsset() error = %v", err)
	}
	defer body.Close()
	if got, _ := io.ReadAll(body); string(got) != "binary" {
		t.Errorf("content = %q, want binary", got)
	}

	if _, err = src.OpenAsset(t.Context(), forge.Asset{Name: "x", URL: "https://evil.example/x"}); err == nil {
		t.Error("OpenAsset() of a foreign URL: error = nil, want refusal")
	}
	if _, err = src.OpenAsset(t.Context(), forge.Asset{Name: "x", URL: f.srv.URL + "/missing"}); err == nil {
		t.Error("OpenAsset() of a missing file: error = nil, want error")
	}
}

func TestSourcePackages(t *testing.T) {
	t.Parallel()

	const packages = `[
	  {"type": "container", "name": "app", "version": "1.0.0", "repository": {"full_name": "acme/app"}},
	  {"type": "container", "name": "app", "version": "sha256:abc", "repository": {"full_name": "acme/app"}},
	  {"type": "container", "name": "app", "version": "sha256-abc.sig", "repository": {"full_name": "ACME/app"}},
	  {"type": "container", "name": "charts/app", "version": "0.1.0", "repository": {"full_name": "acme/app"}},
	  {"type": "generic", "name": "blob", "version": "1", "repository": {"full_name": "acme/app"}},
	  {"type": "container", "name": "other", "version": "1", "repository": {"full_name": "acme/other"}},
	  {"type": "container", "name": "unlinked", "version": "1"}
	]`
	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/packages/acme": listReply(packages, 7),
	})
	src := f.client(t, "").Source().(forge.PackageSource)

	got, err := src.Packages(t.Context(), "acme", "app")
	if err != nil {
		t.Fatalf("Packages() error = %v", err)
	}
	want := []forge.Package{
		{Name: "app", Type: "container", Tags: []string{"1.0.0", "sha256-abc.sig"}},
		{Name: "charts/app", Type: "container", Tags: []string{"0.1.0"}},
		{Name: "blob", Type: "generic", Tags: []string{"1"}},
	}
	if len(got) != len(want) {
		t.Fatalf("Packages() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Type != want[i].Type ||
			strings.Join(got[i].Tags, ",") != strings.Join(want[i].Tags, ",") {
			t.Errorf("package %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSourceListingErrors(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)
	src := f.client(t, "").Source()

	if _, err := src.(forge.ReleaseSource).Releases(t.Context(), "acme", "app"); err == nil {
		t.Error("Releases() error = nil, want error")
	}
	if _, err := src.(forge.PackageSource).Packages(t.Context(), "acme", "app"); err == nil {
		t.Error("Packages() error = nil, want error")
	}
}

func TestReleaseTargetWrites(t *testing.T) {
	t.Parallel()

	var uploaded, uploadedName string
	f := newFake(t, map[string]http.HandlerFunc{
		"POST /api/v1/repos/acme/app/releases": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id": 11, "tag_name": "v1.0.0", "name": "First"}`)
		},
		"PATCH /api/v1/repos/acme/app/releases/7":           jsonReply(`{"id": 7}`),
		"DELETE /api/v1/repos/acme/app/releases/7":          func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"DELETE /api/v1/repos/acme/app/releases/7/assets/9": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"POST /api/v1/repos/acme/app/releases/7/assets": func(w http.ResponseWriter, r *http.Request) {
			uploadedName = r.URL.Query().Get("name")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id": 12}`)
		},
	})
	c := f.client(t, "")
	ctx := t.Context()
	current := forge.Release{ID: 7, Tag: "v1.0.0"}

	created, err := c.CreateRelease(ctx, testAddr(), forge.Release{Tag: "v1.0.0", Name: "First", Prerelease: true})
	if err != nil || created.ID != 11 {
		t.Fatalf("CreateRelease() = %+v, %v; want ID 11", created, err)
	}
	body := f.recorded(http.MethodPost, "/api/v1/repos/acme/app/releases")[0].Body
	if !strings.Contains(body, `"tag_name":"v1.0.0"`) || !strings.Contains(body, `"prerelease":true`) ||
		strings.Contains(body, `"draft":true`) {
		t.Errorf("create body = %s", body)
	}

	if err = c.UpdateRelease(ctx, testAddr(), current, forge.Release{Name: "New", Body: "b"}); err != nil {
		t.Fatalf("UpdateRelease() error = %v", err)
	}
	if body = f.recorded(http.MethodPatch, "/api/v1/repos/acme/app/releases/7")[0].Body; !strings.Contains(
		body,
		`"name":"New"`,
	) ||
		!strings.Contains(body, `"prerelease":false`) {
		t.Errorf("update body = %s", body)
	}

	if err = c.UploadAsset(ctx, testAddr(), current, "app.tar.gz", 6, strings.NewReader("binary")); err != nil {
		t.Fatalf("UploadAsset() error = %v", err)
	}
	uploaded = f.recorded(http.MethodPost, "/api/v1/repos/acme/app/releases/7/assets")[0].Body
	if uploadedName != "app.tar.gz" || !strings.Contains(uploaded, `filename="app.tar.gz"`) ||
		!strings.Contains(uploaded, "binary") || !strings.Contains(uploaded, `name="attachment"`) {
		t.Errorf("upload name = %q, body = %q", uploadedName, uploaded)
	}

	if err = c.DeleteAsset(ctx, testAddr(), current, forge.Asset{ID: 9, Name: "old"}); err != nil {
		t.Fatalf("DeleteAsset() error = %v", err)
	}
	if err = c.DeleteRelease(ctx, testAddr(), current); err != nil {
		t.Fatalf("DeleteRelease() error = %v", err)
	}
	if !c.ReleaseCapabilities().Prerelease {
		t.Error("ReleaseCapabilities().Prerelease = false, want true")
	}
}

func TestUploadAssetFailure(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"POST /api/v1/repos/acme/app/releases/7/assets": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"quota exceeded"}`, http.StatusRequestEntityTooLarge)
		},
	})

	err := f.client(t, "").
		UploadAsset(t.Context(), testAddr(), forge.Release{ID: 7, Tag: "v1"}, "a", 1, strings.NewReader("x"))
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("UploadAsset() error = %v, want status message without token", err)
	}
}

func TestLazyReleaseForwarding(t *testing.T) {
	t.Parallel()

	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id": 1}`)
	}
	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app/releases":               listReply(releasesJSON, 2),
		"POST /api/v1/repos/acme/app/releases":              ok,
		"PATCH /api/v1/repos/acme/app/releases/7":           ok,
		"DELETE /api/v1/repos/acme/app/releases/7":          ok,
		"DELETE /api/v1/repos/acme/app/releases/7/assets/9": ok,
		"POST /api/v1/repos/acme/app/releases/7/assets":     ok,
		"GET /api/v1/packages/acme":                         listReply(`[]`, 0),
		"GET /dl":                                           ok,
	})
	l := forgejo.NewLazy(httpclient.New(5*time.Second), f.srv.URL, config.Secret(testToken))
	ctx := t.Context()
	rel := forge.Release{ID: 7, Tag: "v1.0.0", Name: "x"}

	if got, err := l.Releases(ctx, testAddr()); err != nil || len(got) != 2 {
		t.Errorf("Releases() = %d, %v", len(got), err)
	}
	if _, err := l.CreateRelease(ctx, testAddr(), rel); err != nil {
		t.Errorf("CreateRelease() error = %v", err)
	}
	if err := l.UpdateRelease(ctx, testAddr(), rel, rel); err != nil {
		t.Errorf("UpdateRelease() error = %v", err)
	}
	if err := l.UploadAsset(ctx, testAddr(), rel, "a", 1, strings.NewReader("x")); err != nil {
		t.Errorf("UploadAsset() error = %v", err)
	}
	if err := l.DeleteAsset(ctx, testAddr(), rel, forge.Asset{ID: 9}); err != nil {
		t.Errorf("DeleteAsset() error = %v", err)
	}
	if err := l.DeleteRelease(ctx, testAddr(), rel); err != nil {
		t.Errorf("DeleteRelease() error = %v", err)
	}
	if !l.ReleaseCapabilities().Prerelease {
		t.Error("ReleaseCapabilities().Prerelease = false")
	}

	src := l.Source()
	if got, err := src.(forge.ReleaseSource).Releases(ctx, "acme", "app"); err != nil || len(got) != 2 {
		t.Errorf("source Releases() = %d, %v", len(got), err)
	}
	if _, err := src.(forge.PackageSource).Packages(ctx, "acme", "app"); err != nil {
		t.Errorf("source Packages() error = %v", err)
	}
	body, err := src.(forge.ReleaseSource).OpenAsset(ctx, forge.Asset{URL: f.srv.URL + "/dl"})
	if err != nil {
		t.Fatalf("source OpenAsset() error = %v", err)
	}
	_ = body.Close()
}

func TestLazyReleaseConnectFailure(t *testing.T) {
	t.Parallel()

	l := forgejo.NewLazy(httpclient.New(time.Second), "http://127.0.0.1:1", config.Secret(testToken))
	forgejo.SetFailureBackoff(l, 0)
	ctx := t.Context()
	rel := forge.Release{ID: 1}

	errs := []error{
		func() error { _, err := l.Releases(ctx, testAddr()); return err }(),
		func() error { _, err := l.CreateRelease(ctx, testAddr(), rel); return err }(),
		l.UpdateRelease(ctx, testAddr(), rel, rel),
		l.DeleteRelease(ctx, testAddr(), rel),
		l.UploadAsset(ctx, testAddr(), rel, "a", 1, strings.NewReader("x")),
		l.DeleteAsset(ctx, testAddr(), rel, forge.Asset{}),
		func() error { _, err := l.Source().(forge.ReleaseSource).Releases(ctx, "a", "b"); return err }(),
		func() error { _, err := l.Source().(forge.ReleaseSource).OpenAsset(ctx, forge.Asset{}); return err }(),
		func() error { _, err := l.Source().(forge.PackageSource).Packages(ctx, "a", "b"); return err }(),
	}
	for i, err := range errs {
		if err == nil {
			t.Errorf("call %d: error = nil, want connection error", i)
		}
	}
}
