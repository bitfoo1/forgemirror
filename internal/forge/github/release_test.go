package github_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge"
)

func TestReleases(t *testing.T) {
	t.Parallel()

	var f *fake
	f = newFake(t, map[string]http.HandlerFunc{
		"GET /api/v3/repos/o/r/releases": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"id": 2, "tag_name": "v0.1.0", "name": "old", "created_at": "2025-01-01T00:00:00Z"}]`)

				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/repos/o/r/releases?page=2>; rel="next"`, f.srv.URL))
			fmt.Fprint(w, `[{"id": 1, "tag_name": "v1.0.0", "name": "One", "body": "b", "prerelease": true,
			  "upload_url": "https://uploads.example/assets{?name,label}", "created_at": "2026-01-01T00:00:00Z",
			  "assets": [{"id": 5, "name": "a.tgz", "size": 3}]}]`)
		},
	})

	got, err := f.client(t).Releases(t.Context(), addr())
	if err != nil {
		t.Fatalf("Releases() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Releases() = %+v, want 2 releases over 2 pages", got)
	}
	r := got[0]
	if r.ID != 1 || r.Tag != "v1.0.0" || r.Name != "One" || r.Body != "b" || !r.Prerelease ||
		r.UploadURL != "https://uploads.example/assets{?name,label}" || r.CreatedAt.Year() != 2026 {
		t.Errorf("release = %+v", r)
	}
	if len(r.Assets) != 1 || r.Assets[0] != (forge.Asset{ID: 5, Name: "a.tgz", Size: 3}) {
		t.Errorf("assets = %+v", r.Assets)
	}
}

func TestReleaseWrites(t *testing.T) {
	t.Parallel()

	var f *fake
	f = newFake(t, map[string]http.HandlerFunc{
		"POST /api/v3/repos/o/r/releases": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(
				w,
				`{"id": 11, "tag_name": "v1.0.0", "upload_url": "%s/api/uploads/repos/o/r/releases/11/assets{?name,label}"}`,
				f.srv.URL,
			)
		},
		"PATCH /api/v3/repos/o/r/releases/11":            jsonReply(`{"id": 11}`),
		"DELETE /api/v3/repos/o/r/releases/11":           noContent(),
		"DELETE /api/v3/repos/o/r/releases/assets/5":     noContent(),
		"POST /api/uploads/repos/o/r/releases/11/assets": jsonReply(`{"id": 6}`),
	})
	c := f.client(t)
	ctx := t.Context()

	created, err := c.CreateRelease(ctx, addr(), forge.Release{Tag: "v1.0.0", Name: "One", Body: "b"})
	if err != nil || created.ID != 11 || created.UploadURL == "" {
		t.Fatalf("CreateRelease() = %+v, %v", created, err)
	}
	body := f.recorded(http.MethodPost, "/api/v3/repos/o/r/releases")[0].Body
	for _, want := range []string{`"tag_name":"v1.0.0"`, `"name":"One"`, `"make_latest":"legacy"`, `"prerelease":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("create body %s lacks %s", body, want)
		}
	}

	if err = c.UpdateRelease(ctx, addr(), created, forge.Release{Name: "Two", Prerelease: true}); err != nil {
		t.Fatalf("UpdateRelease() error = %v", err)
	}
	if body = f.recorded(http.MethodPatch, "/api/v3/repos/o/r/releases/11")[0].Body; !strings.Contains(
		body,
		`"name":"Two"`,
	) ||
		!strings.Contains(body, `"prerelease":true`) {
		t.Errorf("update body = %s", body)
	}

	if err = c.UploadAsset(ctx, addr(), created, "app.tar.gz", 6, strings.NewReader("binary")); err != nil {
		t.Fatalf("UploadAsset() error = %v", err)
	}
	up := f.recorded(http.MethodPost, "/api/uploads/repos/o/r/releases/11/assets")[0]
	if up.Body != "binary" || !strings.Contains(up.URI, "name=app.tar.gz") {
		t.Errorf("upload = %+v", up)
	}

	if err = c.DeleteAsset(ctx, addr(), created, forge.Asset{ID: 5, Name: "a"}); err != nil {
		t.Fatalf("DeleteAsset() error = %v", err)
	}
	if err = c.DeleteRelease(ctx, addr(), created); err != nil {
		t.Fatalf("DeleteRelease() error = %v", err)
	}
	if !c.ReleaseCapabilities().Prerelease {
		t.Error("ReleaseCapabilities().Prerelease = false, want true")
	}
}

func TestReleaseErrors(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)
	c := f.client(t)
	ctx := t.Context()
	rel := forge.Release{ID: 1, Tag: "v1", UploadURL: f.srv.URL + "/api/uploads/repos/o/r/releases/1/assets"}

	if _, err := c.Releases(ctx, addr()); err == nil {
		t.Error("Releases() error = nil")
	}
	if _, err := c.CreateRelease(ctx, addr(), rel); err == nil {
		t.Error("CreateRelease() error = nil")
	}
	if err := c.UploadAsset(ctx, addr(), rel, "a", 1, strings.NewReader("x")); err == nil {
		t.Error("UploadAsset() error = nil")
	}
	evil := rel
	evil.UploadURL = "https://evil.example/upload"
	if err := c.UploadAsset(ctx, addr(), evil, "a", 1, strings.NewReader("x")); err == nil {
		t.Error("UploadAsset() to a foreign host: error = nil, want refusal")
	}
}
