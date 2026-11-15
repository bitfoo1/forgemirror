package forgejo_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/forgejo"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

const testToken = "s3cr3t-token"

type call struct {
	Method string
	Path   string
	Query  string
	Body   string
}

type fake struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []call
	routes map[string]http.HandlerFunc
}

// newFake serves routes keyed by "METHOD /path"; unknown routes answer 404 so
// a wrong request path fails the test instead of hanging it.
func newFake(t *testing.T, routes map[string]http.HandlerFunc) *fake {
	t.Helper()

	f := &fake{t: t, routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		f.mu.Lock()
		f.calls = append(f.calls, call{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
		f.mu.Unlock()

		if got := r.Header.Get("Authorization"); r.URL.Path != "/api/v1/version" && got != "token "+testToken {
			t.Errorf("Authorization = %q", got)
		}

		if r.URL.Path == "/api/v1/version" {
			fmt.Fprint(w, `{"version":"15.0.0+gitea-1.22.0"}`)

			return
		}

		h, ok := f.routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, `{"message":"no route"}`, http.StatusNotFound)

			return
		}

		h(w, r)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fake) client(t *testing.T, base string) *forgejo.Client {
	t.Helper()

	c, err := forgejo.New(t.Context(), httpclient.New(5*time.Second), f.srv.URL+base, config.Secret(testToken))
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
		if c.Method == method && c.Path == path {
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

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func addr() forge.Address {
	return forge.Address{APIURL: "unused", Namespace: "acme", Name: "app"}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()

	m := map[string]any{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	return m
}

func TestNewAcceptsRootAndAPIURL(t *testing.T) {
	t.Parallel()

	for _, base := range []string{"", "/", "/api/v1", "/api/v1/"} {
		t.Run("base"+base, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, map[string]http.HandlerFunc{
				"GET /api/v1/repos/acme/app":        jsonReply(`{"name":"app","description":"d"}`),
				"GET /api/v1/repos/acme/app/topics": jsonReply(`{"topics":[]}`),
			})

			r, err := f.client(t, base).Get(t.Context(), addr())
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			if r.Description != "d" {
				t.Errorf("Description = %q", r.Description)
			}
		})
	}
}

func TestRepo(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app": jsonReply(`{"name":"app","description":"desc","website":"https://x.example",
			"private":true,"default_branch":"trunk","has_issues":true,"has_wiki":false,"has_pull_requests":true}`),
		"GET /api/v1/repos/acme/app/topics": jsonReply(`{"topics":["go","mirror"]}`),
	})

	want := forge.Repo{
		Description: "desc", Website: "https://x.example", Topics: []string{"go", "mirror"},
		DefaultBranch: "trunk", Private: true, HasIssues: true, HasWiki: false, HasPullRequests: true,
	}

	c := f.client(t, "")

	for name, get := range map[string]func() (forge.Repo, error){
		"source": func() (forge.Repo, error) { return c.Source().Repo(t.Context(), "acme", "app") },
		"target": func() (forge.Repo, error) { return c.Get(t.Context(), addr()) },
	} {
		got, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}

	if got := c.Capabilities(); !got.Website || !got.PullRequests {
		t.Errorf("Capabilities = %+v", got)
	}
}

func TestLabelsPaged(t *testing.T) {
	t.Parallel()

	page := func(from, n int) string {
		items := make([]string, 0, n)
		for i := from; i < from+n; i++ {
			items = append(
				items,
				fmt.Sprintf(`{"id":%d,"name":"l%d","color":"#AABB%02d","description":"d%d"}`, i, i, i%100, i),
			)
		}

		return "[" + strings.Join(items, ",") + "]"
	}

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app/labels": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, page(0, 50))

				return
			}

			fmt.Fprint(w, page(50, 3))
		},
	})

	c := f.client(t, "")

	for name, get := range map[string]func() ([]forge.Label, error){
		"source": func() ([]forge.Label, error) { return c.Source().Labels(t.Context(), "acme", "app") },
		"target": func() ([]forge.Label, error) { return c.Labels(t.Context(), addr()) },
	} {
		got, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if len(got) != 53 {
			t.Fatalf("%s: len = %d, want 53", name, len(got))
		}

		if got[0].Color != "aabb00" || got[0].Description != "d0" {
			t.Errorf("%s: first label = %+v", name, got[0])
		}
	}
}

func TestRepos(t *testing.T) {
	t.Parallel()

	item := func(i int) string {
		return fmt.Sprintf(`{"name":"r%d","full_name":"acme/r%d","owner":{"login":"acme"},
			"clone_url":"https://git.example/acme/r%d.git","private":%t,"fork":%t}`, i, i, i, i%2 == 0, i == 1)
	}

	var first []string
	for i := range 50 {
		first = append(first, item(i))
	}

	routes := map[string]http.HandlerFunc{
		"GET /api/v1/repos/search": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(first, ","))

				return
			}

			fmt.Fprintf(w, `{"data":[%s,%s]}`, item(50), item(51))
		},
	}
	// The SDK's Repository model has no topics field, so each repo costs one
	// extra topics request.
	for i := range 52 {
		routes[fmt.Sprintf("GET /api/v1/repos/acme/r%d/topics", i)] = jsonReply(fmt.Sprintf(`{"topics":["t%d"]}`, i))
	}

	f := newFake(t, routes)

	got, err := f.client(t, "").Source().Repos(t.Context())
	if err != nil {
		t.Fatalf("Repos: %v", err)
	}

	if len(got) != 52 {
		t.Fatalf("len = %d, want 52", len(got))
	}

	r := got[1]
	if r.Owner != "acme" || r.Name != "r1" || r.FullName != "acme/r1" ||
		r.CloneURL != "https://git.example/acme/r1.git" || r.Private || !r.Fork ||
		len(r.Topics) != 1 || r.Topics[0] != "t1" {
		t.Errorf("repo 1 = %+v", r)
	}

	if !got[0].Private {
		t.Errorf("repo 0 should be private: %+v", got[0])
	}
}

func TestGetNotFound(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"not here"}`, http.StatusNotFound)
		},
	})

	c := f.client(t, "")

	if _, err := c.Get(t.Context(), addr()); !errorsIs(err, forge.ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound", err)
	}

	if _, err := c.Source().Repo(t.Context(), "acme", "app"); !errorsIs(err, forge.ErrNotFound) {
		t.Errorf("Repo err = %v, want ErrNotFound", err)
	}
}

func TestCreateOrgVsUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ns       string
		orgRoute http.HandlerFunc
		wantPath string
	}{
		{"organisation", "acme", jsonReply(`{"username":"acme"}`), "/api/v1/org/acme/repos"},
		{"user", "bob", status(http.StatusNotFound), "/api/v1/user/repos"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, map[string]http.HandlerFunc{
				"GET /api/v1/orgs/" + tt.ns: tt.orgRoute,
				"POST " + tt.wantPath:       jsonReply(`{"name":"app"}`),
			})

			a := forge.Address{Namespace: tt.ns, Name: "app"}

			err := f.client(t, "").Create(t.Context(), a, forge.Repo{
				Description: "d", Private: true, DefaultBranch: "main",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			posts := f.recorded("POST", tt.wantPath)
			if len(posts) != 1 {
				t.Fatalf("posts = %d", len(posts))
			}

			body := decode(t, posts[0].Body)
			if body["private"] != true || body["name"] != "app" || body["description"] != "d" {
				t.Errorf("body = %v", body)
			}

			// The SDK always serialises default_branch; empty means "unset" to
			// Forgejo, whereas a value would be rejected before the first push.
			if v := body["default_branch"]; v != nil && v != "" {
				t.Errorf("default_branch = %v", v)
			}
		})
	}
}

func TestUpdateOnlySetFields(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/repos/acme/app":      jsonReply(`{"name":"app"}`),
		"PUT /api/v1/repos/acme/app/topics": status(http.StatusNoContent),
	})

	c := f.client(t, "")
	no := false

	if err := c.Update(t.Context(), addr(), forge.Settings{HasIssues: &no}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	patches := f.recorded("PATCH", "/api/v1/repos/acme/app")
	if len(patches) != 1 {
		t.Fatalf("patches = %d", len(patches))
	}

	if got := decode(t, patches[0].Body); len(got) != 1 || got["has_issues"] != false {
		t.Errorf("patch body = %v", got)
	}

	if n := len(f.recorded("PUT", "/api/v1/repos/acme/app/topics")); n != 0 {
		t.Errorf("topics PUT = %d, want 0", n)
	}

	topics := []string{"a", "b"}
	if err := c.Update(t.Context(), addr(), forge.Settings{Topics: &topics}); err != nil {
		t.Fatalf("Update topics: %v", err)
	}

	if n := len(f.recorded("PATCH", "/api/v1/repos/acme/app")); n != 1 {
		t.Errorf("PATCH count = %d, want still 1", n)
	}

	puts := f.recorded("PUT", "/api/v1/repos/acme/app/topics")
	if len(puts) != 1 || !strings.Contains(puts[0].Body, `"a","b"`) {
		t.Errorf("topics PUT = %+v", puts)
	}

	if err := c.Update(t.Context(), addr(), forge.Settings{}); err != nil {
		t.Fatalf("empty Update: %v", err)
	}
}

func TestUpdateAllFields(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/repos/acme/app": jsonReply(`{"name":"app"}`),
	})

	s, w, b, yes := "x", "https://w.example", "main", true
	set := forge.Settings{
		Description: &s, Website: &w, DefaultBranch: &b,
		Private: &yes, HasIssues: &yes, HasWiki: &yes, HasPullRequests: &yes,
	}

	if err := f.client(t, "").Update(t.Context(), addr(), set); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := decode(t, f.recorded("PATCH", "/api/v1/repos/acme/app")[0].Body)
	if len(got) != 7 || got["default_branch"] != "main" || got["website"] != w || got["private"] != true {
		t.Errorf("body = %v", got)
	}
}

func TestLabelCreateUpdateDelete(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app/labels": jsonReply(
			`[{"id":7,"name":"Bug","color":"ff0000"},{"id":8,"name":"other","color":"00ff00"}]`,
		),
		"POST /api/v1/repos/acme/app/labels":     jsonReply(`{"id":9}`),
		"PATCH /api/v1/repos/acme/app/labels/7":  jsonReply(`{"id":7}`),
		"DELETE /api/v1/repos/acme/app/labels/7": status(http.StatusNoContent),
	})

	c := f.client(t, "")
	l := forge.Label{Name: "bug", Color: "0000FF", Description: "x"}

	if err := c.CreateLabel(t.Context(), addr(), forge.Label{Name: "new", Color: "00ff00"}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	if err := c.UpdateLabel(t.Context(), addr(), l); err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}

	if err := c.DeleteLabel(t.Context(), addr(), "BUG"); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	if body := decode(t, f.recorded("POST", "/api/v1/repos/acme/app/labels")[0].Body); body["name"] != "new" {
		t.Errorf("create body = %v", body)
	}

	patch := decode(t, f.recorded("PATCH", "/api/v1/repos/acme/app/labels/7")[0].Body)
	if patch["name"] != "bug" || patch["description"] != "x" || fmt.Sprint(patch["color"]) != "0000ff" {
		t.Errorf("patch body = %v", patch)
	}

	if err := c.UpdateLabel(
		t.Context(),
		addr(),
		forge.Label{Name: "ghost", Color: "000000"},
	); !errorsIs(
		err,
		forge.ErrNotFound,
	) {
		t.Errorf("UpdateLabel missing = %v, want ErrNotFound", err)
	}

	if err := c.DeleteLabel(t.Context(), addr(), "ghost"); !errorsIs(err, forge.ErrNotFound) {
		t.Errorf("DeleteLabel missing = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	f := newFake(t, map[string]http.HandlerFunc{
		"DELETE /api/v1/repos/acme/app": status(http.StatusNoContent),
	})

	if err := f.client(t, "").Delete(t.Context(), addr()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	gone := newFake(t, nil)
	if err := gone.client(t, "").Delete(t.Context(), addr()); !errorsIs(err, forge.ErrNotFound) {
		t.Errorf("Delete missing = %v, want ErrNotFound", err)
	}
}

func TestErrorDoesNotLeakToken(t *testing.T) {
	t.Parallel()

	boom := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	}
	f := newFake(t, map[string]http.HandlerFunc{
		"GET /api/v1/repos/acme/app":        boom,
		"GET /api/v1/repos/acme/app/labels": boom,
		"DELETE /api/v1/repos/acme/app":     boom,
	})

	c := f.client(t, "")

	_, getErr := c.Get(t.Context(), addr())
	_, labelErr := c.Labels(t.Context(), addr())
	delErr := c.Delete(t.Context(), addr())

	for _, err := range []error{getErr, labelErr, delErr} {
		if err == nil {
			t.Fatal("expected an error")
		}

		for _, s := range []string{err.Error(), fmt.Sprintf("%+v", err)} {
			if strings.Contains(s, testToken) || strings.Contains(s, "Authorization") {
				t.Errorf("error leaks credentials: %q", s)
			}

			if !strings.Contains(s, "boom") || !strings.Contains(s, "acme/app") {
				t.Errorf("error lacks context: %q", s)
			}
		}
	}
}

func TestNewFailsWithoutLeakingToken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(status(http.StatusInternalServerError))
	t.Cleanup(srv.Close)

	_, err := forgejo.New(t.Context(), httpclient.New(time.Second), srv.URL, config.Secret(testToken))
	if err == nil {
		t.Fatal("expected error")
	}

	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaks token: %v", err)
	}
}

func errorsIs(err, target error) bool { return err != nil && errors.Is(err, target) }

var _ forge.Target = (*forgejo.Client)(nil)
