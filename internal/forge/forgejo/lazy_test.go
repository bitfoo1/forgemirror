package forgejo_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/forgejo"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

// flakyForgejo answers the version probe with 503 while down is set.
type flakyForgejo struct {
	srv           *httptest.Server
	down          atomic.Bool
	versionProbes atomic.Int32
}

func newFlakyForgejo(t *testing.T) *flakyForgejo {
	t.Helper()

	f := &flakyForgejo{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, `{"message":"unavailable"}`, http.StatusServiceUnavailable)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/version":
			f.versionProbes.Add(1)
			fmt.Fprint(w, `{"version":"15.0.0+gitea-1.22.0"}`)
		case "/api/v1/repos/acme/app":
			fmt.Fprint(w, `{"name":"app","description":"d"}`)
		case "/api/v1/repos/acme/app/topics":
			fmt.Fprint(w, `{"topics":[]}`)
		default:
			http.Error(w, `{"message":"no route"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func TestLazyConnectsOnFirstUseAndRetries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(ctx context.Context, l *forgejo.Lazy) error
	}{
		{name: "target", call: func(ctx context.Context, l *forgejo.Lazy) error {
			_, err := l.Get(ctx, addr())

			return err
		}},
		{name: "source", call: func(ctx context.Context, l *forgejo.Lazy) error {
			_, err := l.Source().Repo(ctx, "acme", "app")

			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFlakyForgejo(t)
			f.down.Store(true)

			l := forgejo.NewLazy(httpclient.New(5*time.Second), f.srv.URL, config.Secret(testToken))

			err := tc.call(t.Context(), l)
			if err == nil {
				t.Fatal("call while Forgejo is down: error = nil")
			}
			if errors.Is(err, forge.ErrNotFound) {
				t.Errorf("connect failure = %v, must not look like a missing repository", err)
			}

			f.down.Store(false)
			for range 2 {
				if err = tc.call(t.Context(), l); err != nil {
					t.Fatalf("call after recovery: %v", err)
				}
			}
			if n := f.versionProbes.Load(); n != 1 {
				t.Errorf("successful version probes = %d, want 1 (client cached)", n)
			}
		})
	}
}

func TestLazyMakesNoRequestUntilUsed(t *testing.T) {
	t.Parallel()

	f := newFlakyForgejo(t)
	l := forgejo.NewLazy(httpclient.New(5*time.Second), f.srv.URL, config.Secret(testToken))

	if caps := l.Capabilities(); !caps.Website || !caps.PullRequests {
		t.Errorf("Capabilities() = %+v, want website and pull requests", caps)
	}
	if n := f.versionProbes.Load(); n != 0 {
		t.Errorf("version probes = %d before first use, want 0", n)
	}
}

// TestLazyEveryMethodConnects: each method fails with the connect error while
// Forgejo is down and reaches the server once it is up.
func TestLazyEveryMethodConnects(t *testing.T) {
	t.Parallel()

	lb := forge.Label{Name: "bug", Color: "ff0000"}
	tests := []struct {
		name string
		call func(ctx context.Context, l *forgejo.Lazy) error
	}{
		{name: "Create", call: func(ctx context.Context, l *forgejo.Lazy) error {
			return l.Create(ctx, addr(), forge.Repo{})
		}},
		{name: "Update", call: func(ctx context.Context, l *forgejo.Lazy) error {
			d := "x"

			return l.Update(ctx, addr(), forge.Settings{Description: &d})
		}},
		{name: "Labels", call: func(ctx context.Context, l *forgejo.Lazy) error {
			_, err := l.Labels(ctx, addr())

			return err
		}},
		{name: "CreateLabel", call: func(ctx context.Context, l *forgejo.Lazy) error {
			return l.CreateLabel(ctx, addr(), lb)
		}},
		{name: "UpdateLabel", call: func(ctx context.Context, l *forgejo.Lazy) error {
			return l.UpdateLabel(ctx, addr(), lb)
		}},
		{name: "DeleteLabel", call: func(ctx context.Context, l *forgejo.Lazy) error {
			return l.DeleteLabel(ctx, addr(), "bug")
		}},
		{name: "Delete", call: func(ctx context.Context, l *forgejo.Lazy) error {
			return l.Delete(ctx, addr())
		}},
		{name: "Source.Labels", call: func(ctx context.Context, l *forgejo.Lazy) error {
			_, err := l.Source().Labels(ctx, "acme", "app")

			return err
		}},
		{name: "Source.Repos", call: func(ctx context.Context, l *forgejo.Lazy) error {
			_, err := l.Source().Repos(ctx)

			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFlakyForgejo(t)
			f.down.Store(true)
			l := forgejo.NewLazy(httpclient.New(5*time.Second), f.srv.URL, config.Secret(testToken))

			if err := tc.call(t.Context(), l); err == nil || !strings.Contains(err.Error(), "forgejo connect") {
				t.Errorf("error while down = %v, want connect error", err)
			}

			f.down.Store(false)
			_ = tc.call(t.Context(), l) // the fake has no route for most calls; only the connection matters
			if n := f.versionProbes.Load(); n != 1 {
				t.Errorf("version probes after recovery = %d, want 1", n)
			}
		})
	}
}
