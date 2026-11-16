package gitlab_test

import (
	"net/http"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge"
)

func releaseAddr() forge.Address {
	return forge.Address{APIURL: "unused", Namespace: "o", Name: "r"}
}

const (
	pkgsPath  = projPath + "/packages"
	filesPath = projPath + "/packages/3/package_files"
)

func TestReleases(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET " + projPath + "/releases": jsonReply(`[{"tag_name": "v1.0.0", "name": "One", "description": "b",
		  "created_at": "2026-01-01T00:00:00Z",
		  "assets": {"links": [{"id": 8, "name": "a.tgz"}, {"id": 9, "name": "elsewhere"}]}}]`),
		"GET " + pkgsPath: jsonReply(`[{"id": 3, "name": "release-assets", "version": "v1.0.0"},
		  {"id": 4, "name": "other", "version": "v1.0.0"}]`),
		"GET " + filesPath: jsonReply(`[{"id": 20, "file_name": "a.tgz", "size": 1},
		  {"id": 21, "file_name": "a.tgz", "size": 3}]`),
	})

	got, err := f.client(t).Releases(t.Context(), releaseAddr())
	if err != nil {
		t.Fatalf("Releases() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Releases() = %+v, want 1", got)
	}
	r := got[0]
	if r.Tag != "v1.0.0" || r.Name != "One" || r.Body != "b" || r.CreatedAt.Year() != 2026 {
		t.Errorf("release = %+v", r)
	}
	want := []forge.Asset{{ID: 8, Name: "a.tgz", Size: 3}, {ID: 9, Name: "elsewhere"}}
	if len(r.Assets) != 2 || r.Assets[0] != want[0] || r.Assets[1] != want[1] {
		t.Errorf("assets = %+v, want %+v (newest file wins)", r.Assets, want)
	}
	if q := f.recorded(http.MethodGet, pkgsPath)[0].URI; !strings.Contains(q, "package_name=release-assets") ||
		!strings.Contains(q, "package_type=generic") {
		t.Errorf("package query = %s", q)
	}
}

func TestReleasesEmptySkipsPackages(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{"GET " + projPath + "/releases": jsonReply(`[]`)})

	got, err := f.client(t).Releases(t.Context(), releaseAddr())
	if err != nil || len(got) != 0 {
		t.Fatalf("Releases() = %+v, %v", got, err)
	}
	if n := len(f.recorded(http.MethodGet, pkgsPath)); n != 0 {
		t.Errorf("package listing requested %d times, want 0", n)
	}
}

func TestReleaseWrites(t *testing.T) {
	t.Parallel()

	files := jsonReply(`[{"id": 20, "file_name": "a.tgz", "size": 3}]`)
	f := newFake(t, map[string]http.HandlerFunc{
		"POST " + projPath + "/releases":              jsonReply(`{"tag_name": "v1.0.0", "name": "One"}`),
		"PUT " + projPath + "/releases/v1%2E0%2E0":    jsonReply(`{"tag_name": "v1.0.0"}`),
		"DELETE " + projPath + "/releases/v1%2E0%2E0": jsonReply(`{"tag_name": "v1.0.0"}`),
		"PUT " + projPath + "/packages/generic/release-assets/v1%2E0%2E0/a%2Etgz": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST " + projPath + "/releases/v1%2E0%2E0/assets/links":     jsonReply(`{"id": 8}`),
		"DELETE " + projPath + "/releases/v1%2E0%2E0/assets/links/8": jsonReply(`{"id": 8}`),
		"GET " + pkgsPath:  jsonReply(`[{"id": 3, "name": "release-assets", "version": "v1.0.0"}]`),
		"GET " + filesPath: files,
		"DELETE " + projPath + "/packages/3/package_files/20": noContent(),
		"DELETE " + projPath + "/packages/3":                  noContent(),
	})
	c := f.client(t)
	ctx := t.Context()
	rel := forge.Release{Tag: "v1.0.0", Name: "One", Body: "b"}

	created, err := c.CreateRelease(ctx, releaseAddr(), rel)
	if err != nil || created.Tag != "v1.0.0" {
		t.Fatalf("CreateRelease() = %+v, %v", created, err)
	}
	if body := f.recorded(http.MethodPost, projPath+"/releases")[0].Body; !strings.Contains(
		body,
		`"tag_name":"v1.0.0"`,
	) ||
		!strings.Contains(body, `"description":"b"`) {
		t.Errorf("create body = %s", body)
	}

	if err = c.UpdateRelease(ctx, releaseAddr(), rel, forge.Release{Name: "Two", Body: ""}); err != nil {
		t.Fatalf("UpdateRelease() error = %v", err)
	}
	if body := f.recorded(http.MethodPut, projPath+"/releases/v1%2E0%2E0")[0].Body; !strings.Contains(
		body,
		`"name":"Two"`,
	) ||
		!strings.Contains(body, `"description":""`) {
		t.Errorf("update body = %s", body)
	}

	if err = c.UploadAsset(ctx, releaseAddr(), rel, "a.tgz", 6, strings.NewReader("binary")); err != nil {
		t.Fatalf("UploadAsset() error = %v", err)
	}
	if up := f.recorded(http.MethodPut, projPath+"/packages/generic/release-assets/v1%2E0%2E0/a%2Etgz"); len(up) != 1 ||
		up[0].Body != "binary" {
		t.Errorf("upload = %+v", up)
	}
	link := f.recorded(http.MethodPost, projPath+"/releases/v1%2E0%2E0/assets/links")[0].Body
	if !strings.Contains(link, `"name":"a.tgz"`) || !strings.Contains(link, `"link_type":"package"`) ||
		!strings.Contains(link, f.srv.URL+"/api/v4/projects/o%2Fr/packages/generic/release-assets/v1%2E0%2E0/a%2Etgz") {
		t.Errorf("link body = %s", link)
	}

	if err = c.DeleteAsset(ctx, releaseAddr(), rel, forge.Asset{ID: 8, Name: "a.tgz"}); err != nil {
		t.Fatalf("DeleteAsset() error = %v", err)
	}
	if n := len(f.recorded(http.MethodDelete, projPath+"/packages/3/package_files/20")); n != 1 {
		t.Errorf("package file deleted %d times, want 1", n)
	}

	if err = c.DeleteRelease(ctx, releaseAddr(), rel); err != nil {
		t.Fatalf("DeleteRelease() error = %v", err)
	}
	if n := len(f.recorded(http.MethodDelete, projPath+"/packages/3")); n != 1 {
		t.Errorf("asset package version deleted %d times, want 1", n)
	}
	if c.ReleaseCapabilities().Prerelease {
		t.Error("ReleaseCapabilities().Prerelease = true, want false")
	}
}

func TestReleaseErrors(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"PUT " + projPath + "/packages/generic/release-assets/v1/a": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"too large"}`, http.StatusRequestEntityTooLarge)
		},
	})
	c := f.client(t)
	ctx := t.Context()
	rel := forge.Release{Tag: "v1"}

	if _, err := c.Releases(ctx, releaseAddr()); err == nil {
		t.Error("Releases() error = nil")
	}
	if _, err := c.CreateRelease(ctx, releaseAddr(), rel); err == nil {
		t.Error("CreateRelease() error = nil")
	}
	if err := c.DeleteRelease(ctx, releaseAddr(), rel); err == nil {
		t.Error("DeleteRelease() error = nil")
	}
	if err := c.DeleteAsset(ctx, releaseAddr(), rel, forge.Asset{ID: 1}); err == nil {
		t.Error("DeleteAsset() error = nil")
	}
	err := c.UploadAsset(ctx, releaseAddr(), rel, "a", 1, strings.NewReader("x"))
	if err == nil || !strings.Contains(err.Error(), "too large") || strings.Contains(err.Error(), testToken) {
		t.Errorf("UploadAsset() error = %v, want status message without token", err)
	}
}
