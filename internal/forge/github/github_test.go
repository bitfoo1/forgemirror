package github_test

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
	"go.bitfoo.de/forgemirror/internal/forge/github"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

const testToken = "s3cr3t-token"

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

func (f *fake) client(t *testing.T) *github.Client {
	t.Helper()

	// A non-GitHub URL exercises the enterprise path; the library appends
	// /api/v3/ itself.
	c, err := github.New(httpclient.New(5*time.Second), f.srv.URL, config.Secret(testToken))
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

func TestNewAPIURLs(t *testing.T) {
	t.Parallel()

	for _, u := range []string{"https://api.github.com", "https://api.github.com/", "https://ghe.example.com"} {
		if _, err := github.New(httpclient.New(time.Second), u, config.Secret(testToken)); err != nil {
			t.Errorf("New(%q): %v", u, err)
		}
	}

	if _, err := github.New(httpclient.New(time.Second), "https://api.github.com", ""); err == nil {
		t.Error("empty token accepted")
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v3/repos/o/r": jsonReply(`{"name":"r","description":"desc","homepage":"https://x.example",
			"private":true,"default_branch":"trunk","has_issues":true,"has_wiki":false,
			"topics":["go","mirror"]}`),
	})

	want := forge.Repo{
		Description: "desc", Website: "https://x.example", Topics: []string{"go", "mirror"},
		DefaultBranch: "trunk", Private: true, HasIssues: true,
	}

	c := f.client(t)

	got, err := c.Get(t.Context(), addr())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	if caps := c.Capabilities(); !caps.Website || caps.PullRequests {
		t.Errorf("Capabilities = %+v", caps)
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

func TestCreateOrgVsUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		routes   map[string]http.HandlerFunc
		wantPath string
	}{
		{
			name: "organisation",
			routes: map[string]http.HandlerFunc{
				"GET /api/v3/users/o":       jsonReply(`{"login":"o","type":"Organization"}`),
				"POST /api/v3/orgs/o/repos": jsonReply(`{}`),
			},
			wantPath: "/api/v3/orgs/o/repos",
		},
		{
			name: "own user",
			routes: map[string]http.HandlerFunc{
				"GET /api/v3/users/o":     jsonReply(`{"login":"o","type":"User"}`),
				"GET /api/v3/user":        jsonReply(`{"login":"O","type":"User"}`),
				"POST /api/v3/user/repos": jsonReply(`{}`),
			},
			wantPath: "/api/v3/user/repos",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, tt.routes)

			err := f.client(t).Create(t.Context(), addr(), forge.Repo{
				Description: "d", Private: true, DefaultBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			calls := f.recorded("POST", tt.wantPath)
			if len(calls) != 1 {
				t.Fatalf("POST %s calls = %d", tt.wantPath, len(calls))
			}

			body := decode(t, calls[0].Body)
			if body["name"] != "r" || body["description"] != "d" || body["private"] != true {
				t.Errorf("body = %v", body)
			}

			if _, ok := body["default_branch"]; ok {
				t.Errorf("default branch sent on create: %v", body)
			}
		})
	}
}

func TestCreateRefusesForeignUser(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v3/users/o": jsonReply(`{"login":"o","type":"User"}`),
		"GET /api/v3/user":    jsonReply(`{"login":"someone-else","type":"User"}`),
	})

	err := f.client(t).Create(t.Context(), addr(), forge.Repo{})
	if err == nil {
		t.Fatal("Create succeeded for a foreign user")
	}

	const want = "cannot create repository in namespace o: not an organisation and not the token's user"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want %q", err, want)
	}

	if n := len(f.recorded("POST", "/api/v3/user/repos")); n != 0 {
		t.Errorf("repository created anyway (%d calls)", n)
	}
}

func TestUpdateOnlySetFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings forge.Settings
		wantKeys []string
		wantEdit bool
	}{
		{name: "nothing set"},
		{
			name: "all set, pull requests never sent",
			settings: forge.Settings{
				Description: new("d"), Website: new("https://w"), DefaultBranch: new("main"),
				Private: new(true), HasIssues: new(false), HasWiki: new(true), HasPullRequests: new(false),
			},
			wantKeys: []string{"default_branch", "description", "has_issues", "has_wiki", "homepage", "private"},
			wantEdit: true,
		},
		{
			name:     "empty website is still sent",
			settings: forge.Settings{Website: new("")},
			wantKeys: []string{"homepage"},
			wantEdit: true,
		},
		{
			name:     "only pull requests means no edit",
			settings: forge.Settings{HasPullRequests: new(true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, map[string]http.HandlerFunc{"PATCH /api/v3/repos/o/r": jsonReply(`{}`)})

			if err := f.client(t).Update(t.Context(), addr(), tt.settings); err != nil {
				t.Fatalf("Update: %v", err)
			}

			calls := f.recorded("PATCH", "/api/v3/repos/o/r")
			if !tt.wantEdit {
				if len(calls) != 0 {
					t.Fatalf("unexpected edit: %+v", calls)
				}

				return
			}

			if len(calls) != 1 {
				t.Fatalf("edit calls = %d", len(calls))
			}

			var keys []string
			for k := range decode(t, calls[0].Body) {
				keys = append(keys, k)
			}

			slices.Sort(keys)

			if !slices.Equal(keys, tt.wantKeys) {
				t.Errorf("keys = %v, want %v", keys, tt.wantKeys)
			}
		})
	}
}

func TestTopicsReplace(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"PUT /api/v3/repos/o/r/topics": jsonReply(`{"names":["go"]}`),
	})

	err := f.client(t).Update(t.Context(), addr(), forge.Settings{Topics: &[]string{"go", "mirror"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	calls := f.recorded("PUT", "/api/v3/repos/o/r/topics")
	if len(calls) != 1 {
		t.Fatalf("topic calls = %d", len(calls))
	}

	if got := calls[0].Body; !strings.Contains(got, `"names":["go","mirror"]`) {
		t.Errorf("body = %s", got)
	}

	if n := len(f.recorded("PATCH", "/api/v3/repos/o/r")); n != 0 {
		t.Errorf("edit sent without repository fields: %d", n)
	}
}

func TestLabelsPaged(t *testing.T) {
	t.Parallel()

	var srvURL string

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v3/repos/o/r/labels": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if r.URL.Query().Get("per_page") != "100" {
				t.Errorf("per_page = %q", r.URL.Query().Get("per_page"))
			}

			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"name":"b","color":"00FF00","description":"second"}]`)

				return
			}

			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v3/repos/o/r/labels?page=2&per_page=100>; rel="next"`, srvURL))
			fmt.Fprint(w, `[{"name":"a","color":"FF0000","description":"first"}]`)
		},
	})
	srvURL = f.srv.URL

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

func TestLabelMutations(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"POST /api/v3/repos/o/r/labels":                        jsonReply(`{}`),
		"PATCH /api/v3/repos/o/r/labels/bug":                   jsonReply(`{}`),
		"DELETE /api/v3/repos/o/r/labels/bug":                  noContent(),
		"DELETE /api/v3/repos/o/r/labels/good%20first%20issue": noContent(),
		"PATCH /api/v3/repos/o/r/labels/a%2Fb":                 jsonReply(`{}`),
	})
	c := f.client(t)
	ctx := t.Context()

	if err := c.CreateLabel(ctx, addr(), forge.Label{Name: "bug", Color: "#D73A4A", Description: "x"}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	create := f.recorded("POST", "/api/v3/repos/o/r/labels")
	if len(create) != 1 {
		t.Fatalf("create calls = %d", len(create))
	}

	if b := decode(t, create[0].Body); b["name"] != "bug" || b["color"] != "d73a4a" || b["description"] != "x" {
		t.Errorf("create body = %v", b)
	}

	if err := c.UpdateLabel(ctx, addr(), forge.Label{Name: "bug", Color: "AABBCC", Description: "y"}); err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}

	upd := f.recorded("PATCH", "/api/v3/repos/o/r/labels/bug")
	if len(upd) != 1 {
		t.Fatalf("update calls = %d", len(upd))
	}

	if b := decode(t, upd[0].Body); b["new_name"] != "bug" || b["color"] != "aabbcc" || b["description"] != "y" {
		t.Errorf("update body = %v", b)
	}

	if err := c.DeleteLabel(ctx, addr(), "bug"); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	// A slash in a name must not turn into a path separator.
	if err := c.UpdateLabel(ctx, addr(), forge.Label{Name: "a/b", Color: "000000"}); err != nil {
		t.Fatalf("UpdateLabel a/b: %v", err)
	}
}

func TestLabelNameEscaped(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"DELETE /api/v3/repos/o/r/labels/good%20first%20issue": noContent(),
	})

	if err := f.client(t).DeleteLabel(t.Context(), addr(), "good first issue"); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	if n := len(f.recorded("DELETE", "/api/v3/repos/o/r/labels/good%20first%20issue")); n != 1 {
		t.Errorf("escaped delete calls = %d", n)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"DELETE /api/v3/repos/o/r": noContent(),
	})

	if err := f.client(t).Delete(t.Context(), addr()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := len(f.recorded("DELETE", "/api/v3/repos/o/r")); n != 1 {
		t.Errorf("delete calls = %d", n)
	}
}

func TestErrorDoesNotLeakToken(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v3/repos/o/r": func(w http.ResponseWriter, _ *http.Request) {
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
