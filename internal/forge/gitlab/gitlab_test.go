package gitlab_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/gitlab"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

const (
	testToken = "s3cr3t-token"
	projPath  = "/api/v4/projects/o%2Fr"
)

type call struct {
	Method string
	// URI is the escaped request target, so path-escaping bugs are visible.
	URI  string
	Body string
}

type fake struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []call
	routes map[string]http.HandlerFunc
}

// newFake serves routes keyed by "METHOD /escaped/path"; unknown routes answer
// 404 so a wrong request path fails the test instead of hanging it.
func newFake(t *testing.T, routes map[string]http.HandlerFunc) *fake {
	t.Helper()

	f := &fake{routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		f.mu.Lock()
		f.calls = append(f.calls, call{r.Method, r.URL.RequestURI(), string(body)})
		f.mu.Unlock()

		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}

		if got := r.Header.Get("Private-Token"); got != "" {
			t.Errorf("Private-Token sent: %q", got)
		}

		h, ok := f.routes[r.Method+" "+r.URL.EscapedPath()]
		if !ok {
			http.Error(w, `{"message":"no route"}`, http.StatusNotFound)

			return
		}

		h(w, r)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fake) client(t *testing.T) *gitlab.Client {
	t.Helper()

	// Address.APIURL carries /api/v4; the library must not append it twice.
	c, err := gitlab.New(httpclient.New(5*time.Second), f.srv.URL+"/api/v4", config.Secret(testToken))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return c
}

func (f *fake) recorded(method, path string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []call

	for _, c := range f.calls {
		if c.Method == method && strings.SplitN(c.URI, "?", 2)[0] == path {
			out = append(out, c)
		}
	}

	return out
}

func jsonReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}
}

func noContent() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
}

func addr() forge.Address {
	return forge.Address{APIURL: "unused", Namespace: "o", Name: "r"}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()

	m := map[string]any{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	return m
}

func TestNew(t *testing.T) {
	t.Parallel()

	for _, u := range []string{"https://gitlab.com/api/v4", "https://gitlab.com/api/v4/", "https://gl.example.com"} {
		if _, err := gitlab.New(httpclient.New(time.Second), u, config.Secret(testToken)); err != nil {
			t.Errorf("New(%q): %v", u, err)
		}
	}
}

func TestGetMapsAccessLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want forge.Repo
	}{
		{
			name: "private with restricted features",
			body: `{"description":"desc","default_branch":"trunk","visibility":"private",
				"topics":["go","mirror"],"issues_access_level":"private",
				"wiki_access_level":"disabled","merge_requests_access_level":"enabled"}`,
			want: forge.Repo{
				Description: "desc", Topics: []string{"go", "mirror"}, DefaultBranch: "trunk",
				Private: true, HasIssues: true, HasWiki: false, HasPullRequests: true,
			},
		},
		{
			name: "public with disabled features",
			body: `{"visibility":"public","issues_access_level":"disabled",
				"wiki_access_level":"enabled","merge_requests_access_level":"disabled"}`,
			want: forge.Repo{HasWiki: true},
		},
		{
			name: "internal counts as private",
			body: `{"visibility":"internal"}`,
			want: forge.Repo{Private: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, map[string]http.HandlerFunc{"GET " + projPath: jsonReply(tt.body)})
			c := f.client(t)

			got, err := c.Get(t.Context(), addr())
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}

			if caps := c.Capabilities(); caps.Website || !caps.PullRequests {
				t.Errorf("Capabilities = %+v", caps)
			}
		})
	}
}

func TestGetNotFound(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)

	_, err := f.client(t).Get(t.Context(), addr())
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateResolvesNamespace(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v4/namespaces/group%2Fsub": jsonReply(
			`{"id":42,"kind":"group","path":"sub","full_path":"group/sub"}`,
		),
		"POST /api/v4/projects": jsonReply(`{}`),
	})

	a := forge.Address{Namespace: "group/sub", Name: "r"}

	err := f.client(t).Create(t.Context(), a, forge.Repo{
		Description: "d", Topics: []string{"go"}, DefaultBranch: "main", Private: true,
		HasIssues: true, HasWiki: false, HasPullRequests: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	calls := f.recorded("POST", "/api/v4/projects")
	if len(calls) != 1 {
		t.Fatalf("create calls = %d", len(calls))
	}

	body := decode(t, calls[0].Body)

	want := map[string]any{
		"namespace_id": float64(42), "name": "r", "path": "r", "visibility": "private",
		"description": "d", "topics": []any{"go"},
		"issues_access_level": "enabled", "wiki_access_level": "disabled",
		"merge_requests_access_level": "enabled",
	}
	if fmt.Sprint(body) != fmt.Sprint(want) {
		t.Errorf("body = %v, want %v", body, want)
	}

	if _, ok := body["default_branch"]; ok {
		t.Errorf("default branch sent on create: %v", body)
	}
}

func TestCreateOwnUser(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v4/namespaces/o": jsonReply(`{"id":7,"kind":"user","path":"o"}`),
		"GET /api/v4/user":         jsonReply(`{"username":"O"}`),
		"POST /api/v4/projects":    jsonReply(`{}`),
	})

	if err := f.client(t).Create(t.Context(), addr(), forge.Repo{}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	calls := f.recorded("POST", "/api/v4/projects")
	if len(calls) != 1 {
		t.Fatalf("create calls = %d", len(calls))
	}

	if body := decode(t, calls[0].Body); body["namespace_id"] != float64(7) || body["visibility"] != "public" {
		t.Errorf("body = %v", body)
	}
}

func TestCreateRefusesForeignUser(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v4/namespaces/o": jsonReply(`{"id":7,"kind":"user","path":"o"}`),
		"GET /api/v4/user":         jsonReply(`{"username":"someone-else"}`),
	})

	err := f.client(t).Create(t.Context(), addr(), forge.Repo{})
	if err == nil {
		t.Fatal("Create succeeded for a foreign user")
	}

	const want = "cannot create repository in namespace o: not an organisation and not the token's user"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}

	if n := len(f.recorded("POST", "/api/v4/projects")); n != 0 {
		t.Errorf("repository created anyway (%d calls)", n)
	}
}

func TestUpdateOnlySetFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings forge.Settings
		wantBody map[string]any
	}{
		{name: "nothing set"},
		{
			name:     "website is never sent",
			settings: forge.Settings{Website: new("https://w")},
		},
		{
			name: "all set",
			settings: forge.Settings{
				Description: new("d"), Website: new("https://w"), DefaultBranch: new("main"),
				Topics: &[]string{"go"}, Private: new(true), HasIssues: new(false),
				HasWiki: new(true), HasPullRequests: new(false),
			},
			wantBody: map[string]any{
				"description": "d", "default_branch": "main", "topics": []any{"go"},
				"visibility": "private", "issues_access_level": "disabled",
				"wiki_access_level": "enabled", "merge_requests_access_level": "disabled",
			},
		},
		{
			name:     "empty description is still sent",
			settings: forge.Settings{Description: new("")},
			wantBody: map[string]any{"description": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, map[string]http.HandlerFunc{"PUT " + projPath: jsonReply(`{}`)})

			if err := f.client(t).Update(t.Context(), addr(), tt.settings); err != nil {
				t.Fatalf("Update: %v", err)
			}

			calls := f.recorded("PUT", projPath)
			if tt.wantBody == nil {
				if len(calls) != 0 {
					t.Fatalf("unexpected edit: %+v", calls)
				}

				return
			}

			if len(calls) != 1 {
				t.Fatalf("edit calls = %d", len(calls))
			}

			if got := decode(t, calls[0].Body); fmt.Sprint(got) != fmt.Sprint(tt.wantBody) {
				t.Errorf("body = %v, want %v", got, tt.wantBody)
			}
		})
	}
}

func TestLabelsPaged(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET " + projPath + "/labels": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if r.URL.Query().Get("per_page") != "100" {
				t.Errorf("per_page = %q", r.URL.Query().Get("per_page"))
			}

			if r.URL.Query().Get("page") == "2" {
				w.Header().Set("X-Next-Page", "")
				fmt.Fprint(w, `[{"name":"b","color":"#00FF00","description":"second"}]`)

				return
			}

			w.Header().Set("X-Next-Page", "2")
			fmt.Fprint(w, `[{"name":"a","color":"#FF0000","description":"first"}]`)
		},
	})

	got, err := f.client(t).Labels(t.Context(), addr())
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}

	want := []forge.Label{
		{Name: "a", Color: "ff0000", Description: "first"},
		{Name: "b", Color: "00ff00", Description: "second"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLabelColorHasHash(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"POST " + projPath + "/labels":                        jsonReply(`{}`),
		"PUT " + projPath + "/labels/bug":                     jsonReply(`{}`),
		"PUT " + projPath + "/labels/a%2Fb":                   jsonReply(`{}`),
		"DELETE " + projPath + "/labels/good%20first%20issue": noContent(),
	})
	c := f.client(t)
	ctx := t.Context()

	if err := c.CreateLabel(ctx, addr(), forge.Label{Name: "bug", Color: "D73A4A", Description: "x"}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	create := f.recorded("POST", projPath+"/labels")
	if len(create) != 1 {
		t.Fatalf("create calls = %d", len(create))
	}

	if b := decode(t, create[0].Body); b["name"] != "bug" || b["color"] != "#d73a4a" || b["description"] != "x" {
		t.Errorf("create body = %v", b)
	}

	if err := c.UpdateLabel(ctx, addr(), forge.Label{Name: "bug", Color: "#AABBCC", Description: "y"}); err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}

	upd := f.recorded("PUT", projPath+"/labels/bug")
	if len(upd) != 1 {
		t.Fatalf("update calls = %d", len(upd))
	}

	if b := decode(t, upd[0].Body); b["color"] != "#aabbcc" || b["description"] != "y" {
		t.Errorf("update body = %v", b)
	}

	// A slash or space in a name must stay inside one path segment.
	if err := c.UpdateLabel(ctx, addr(), forge.Label{Name: "a/b", Color: "000000"}); err != nil {
		t.Fatalf("UpdateLabel a/b: %v", err)
	}

	if err := c.DeleteLabel(ctx, addr(), "good first issue"); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	if n := len(f.recorded("DELETE", projPath+"/labels/good%20first%20issue")); n != 1 {
		t.Errorf("delete calls = %d", n)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{"DELETE " + projPath: noContent()})

	if err := f.client(t).Delete(t.Context(), addr()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := len(f.recorded("DELETE", projPath)); n != 1 {
		t.Errorf("delete calls = %d", n)
	}
}

func TestErrorDoesNotLeakToken(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET " + projPath: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"boom"}`)
		},
	})

	_, err := f.client(t).Get(t.Context(), addr())
	if err == nil {
		t.Fatal("expected error")
	}

	for _, s := range []string{err.Error(), fmt.Sprintf("%+v", err)} {
		if strings.Contains(s, testToken) || strings.Contains(s, "Authorization") {
			t.Errorf("error leaks credentials: %s", s)
		}

		if !strings.Contains(s, "boom") || !strings.Contains(s, "o/r") {
			t.Errorf("error lacks context: %s", s)
		}
	}
}
