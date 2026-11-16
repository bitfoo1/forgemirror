package control_test

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

type fakeDispatcher struct {
	mu     sync.Mutex
	known  map[string]repo.Repository
	events []webhook.Event
	ctxErr []error
}

func (d *fakeDispatcher) Dispatch(ctx context.Context, e webhook.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, e)
	d.ctxErr = append(d.ctxErr, ctx.Err())
}

func (d *fakeDispatcher) Known(name string) (repo.Repository, bool) {
	r, ok := d.known[name]

	return r, ok
}

func (d *fakeDispatcher) KnownNames() []string { return slices.Sorted(maps.Keys(d.known)) }

func (d *fakeDispatcher) dispatched() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var out []string
	for _, e := range d.events {
		kind := "refresh"
		if e.Kind == webhook.EventSync {
			kind = "sync"
		}
		out = append(out, kind+" "+e.Repository.FullName)
	}

	return out
}

type fakeSource struct {
	repos []repo.Repository
	err   error
}

func (s fakeSource) Repo(context.Context, string, string) (forge.Repo, error) {
	return forge.Repo{}, nil
}

func (s fakeSource) Labels(context.Context, string, string) ([]forge.Label, error) { return nil, nil }

func (s fakeSource) Repos(context.Context) ([]repo.Repository, error) { return s.repos, s.err }

type nameFilter struct{ deny string }

func (f nameFilter) Matches(r repo.Repository) bool { return r.FullName != f.deny }

func repository(name string) repo.Repository {
	return repo.Repository{
		Owner:    "acme",
		Name:     name,
		FullName: "acme/" + name,
		CloneURL: "https://git.example/acme/" + name,
	}
}

func newController(d *fakeDispatcher, src forge.Source, pending []string, logs *strings.Builder) *control.Controller {
	return control.New(control.Options{
		Source:     src,
		Filter:     nameFilter{deny: "acme/secret"},
		Dispatcher: d,
		Pending:    func() []string { return pending },
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
		Now:        func() time.Time { return time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC) },
	})
}

func TestOneRepository(t *testing.T) {
	t.Parallel()

	d := &fakeDispatcher{known: map[string]repo.Repository{"acme/app": repository("app")}}
	var logs strings.Builder
	c := newController(d, nil, nil, &logs)

	ctx, cancel := context.WithCancel(t.Context())
	if err := c.Refresh(ctx, "acme/app"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := c.Sync(ctx, "acme/app"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	cancel()

	if got := d.dispatched(); !slices.Equal(got, []string{"refresh acme/app", "sync acme/app"}) {
		t.Errorf("dispatched = %v", got)
	}
	if d.events[0].Delivery != "ui-2026-11-16T10:00:00Z" || d.events[0].Repository.CloneURL == "" {
		t.Errorf("event = %+v, want UI delivery and the known clone URL", d.events[0])
	}
	if slices.ContainsFunc(d.ctxErr, func(err error) bool { return err != nil }) {
		t.Error("the run context must not end with the request")
	}
	if !strings.Contains(logs.String(), `msg="sync requested from the UI" repository=acme/app`) {
		t.Errorf("log lacks the sync request:\n%s", logs.String())
	}

	for _, action := range []func(context.Context, string) error{c.Refresh, c.Sync} {
		if err := action(t.Context(), "acme/other"); !errors.Is(err, control.ErrUnknownRepository) {
			t.Errorf("unknown repository: err = %v, want ErrUnknownRepository", err)
		}
	}
}

func TestRefreshAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source forge.Source
		known  []string
		want   []string
	}{
		{
			name:   "listed and filtered, plus known but no longer listed",
			source: fakeSource{repos: []repo.Repository{repository("b"), repository("secret"), repository("a")}},
			known:  []string{"a", "gone"},
			want:   []string{"refresh acme/a", "refresh acme/b", "refresh acme/gone"},
		},
		{
			name:  "without source only known repositories",
			known: []string{"x"},
			want:  []string{"refresh acme/x"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := &fakeDispatcher{known: map[string]repo.Repository{}}
			for _, n := range tc.known {
				d.known["acme/"+n] = repository(n)
			}
			var logs strings.Builder
			c := newController(d, tc.source, nil, &logs)

			n, err := c.RefreshAll(t.Context())
			if err != nil || n != len(tc.want) {
				t.Fatalf("RefreshAll = %d, %v; want %d", n, err, len(tc.want))
			}
			if got := d.dispatched(); !slices.Equal(got, tc.want) {
				t.Errorf("dispatched = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRefreshAllListingFails(t *testing.T) {
	t.Parallel()

	d := &fakeDispatcher{known: map[string]repo.Repository{"acme/a": repository("a")}}
	var logs strings.Builder
	c := newController(d, fakeSource{err: errors.New("forgejo down")}, nil, &logs)

	if _, err := c.RefreshAll(t.Context()); err == nil || !strings.Contains(err.Error(), "forgejo down") {
		t.Errorf("err = %v, want listing error", err)
	}
	if got := d.dispatched(); len(got) != 0 {
		t.Errorf("dispatched = %v, want nothing", got)
	}
}

func TestSyncAll(t *testing.T) {
	t.Parallel()

	d := &fakeDispatcher{known: map[string]repo.Repository{
		"acme/a": repository("a"), "acme/b": repository("b"), "acme/c": repository("c"),
	}}
	var logs strings.Builder
	c := newController(d, nil, []string{"acme/c", "acme/a", "acme/vanished"}, &logs)

	if n := c.SyncAll(t.Context()); n != 2 {
		t.Errorf("SyncAll = %d, want 2", n)
	}
	if got := d.dispatched(); !slices.Equal(got, []string{"sync acme/a", "sync acme/c"}) {
		t.Errorf("dispatched = %v", got)
	}
	if !strings.Contains(logs.String(), `msg="sync requested from the UI" repositories=2`) {
		t.Errorf("log lacks the sync-all request:\n%s", logs.String())
	}
}
