// Package control turns operator actions from the web UI (refresh, sync, for
// one repository or all) into dispatcher events.
package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// ErrUnknownRepository is returned for a repository forgemirror has not
// seen. Only repositories that passed the filter on their way in are known,
// so the UI cannot be used to mirror arbitrary repositories.
var ErrUnknownRepository = errors.New("unknown repository")

// Dispatcher runs events and remembers the repositories it saw.
type Dispatcher interface {
	webhook.Dispatcher
	Known(fullName string) (repo.Repository, bool)
	KnownNames() []string
}

// Options configures a [Controller].
type Options struct {
	// Source lists the source repositories; nil limits "all" to the known
	// repositories (push-only setups without source API credentials).
	Source        forge.Source
	Filter        interface{ Matches(r repo.Repository) bool }
	Dispatcher    Dispatcher
	CheckCloneURL func(string) error
	// Pending returns the repositories whose newest plan has changes.
	Pending func() []string
	Logger  *slog.Logger
	// Now returns the current time; nil means [time.Now].
	Now func() time.Time
}

// Controller implements the UI actions.
type Controller struct {
	opts Options
}

// New returns a [Controller].
func New(opts Options) *Controller {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Pending == nil {
		opts.Pending = func() []string { return nil }
	}

	return &Controller{opts: opts}
}

// Refresh plans one repository.
func (c *Controller) Refresh(ctx context.Context, fullName string) error {
	return c.one(ctx, fullName, webhook.EventRefresh)
}

// Sync plans and applies one repository.
func (c *Controller) Sync(ctx context.Context, fullName string) error {
	return c.one(ctx, fullName, webhook.EventSync)
}

func (c *Controller) one(ctx context.Context, fullName string, kind webhook.EventKind) error {
	r, ok := c.opts.Dispatcher.Known(fullName)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRepository, fullName)
	}
	delivery := c.delivery()
	c.opts.Logger.InfoContext(ctx, actionMessage(kind),
		slog.String("repository", fullName), slog.String("delivery", delivery))
	return c.dispatch(ctx, r, kind, delivery)
}

// RefreshAll plans every repository: those the source lists and the filter
// admits, plus known ones it no longer lists (their plan turns into a
// deletion). It returns the number of repositories.
func (c *Controller) RefreshAll(ctx context.Context) (int, error) {
	repos := make(map[string]repo.Repository)
	if c.opts.Source != nil {
		listed, err := c.opts.Source.Repos(ctx)
		if err != nil {
			return 0, fmt.Errorf("listing source repositories: %w", err)
		}
		for _, r := range listed {
			if c.opts.Filter.Matches(r) {
				repos[r.FullName] = r
			}
		}
	}
	for _, name := range c.opts.Dispatcher.KnownNames() {
		if _, listed := repos[name]; listed {
			continue
		}
		if r, ok := c.opts.Dispatcher.Known(name); ok {
			repos[name] = r
		}
	}

	return c.all(ctx, webhook.EventRefresh, repos), nil
}

// SyncAll applies every repository whose newest plan has changes, and
// returns their number. It syncs what was reviewed as out of sync, not
// repositories that were never planned.
func (c *Controller) SyncAll(ctx context.Context) int {
	repos := make(map[string]repo.Repository)
	for _, name := range c.opts.Pending() {
		if r, ok := c.opts.Dispatcher.Known(name); ok {
			repos[name] = r
		}
	}

	return c.all(ctx, webhook.EventSync, repos)
}

func (c *Controller) all(ctx context.Context, kind webhook.EventKind, repos map[string]repo.Repository) int {
	delivery := c.delivery()
	c.opts.Logger.InfoContext(ctx, actionMessage(kind),
		slog.Int("repositories", len(repos)), slog.String("delivery", delivery))

	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	slices.Sort(names)
	dispatched := 0
	for _, name := range names {
		if err := c.dispatch(ctx, repos[name], kind, delivery); err == nil {
			dispatched++
		}
	}
	return dispatched
}

func (c *Controller) dispatch(ctx context.Context, r repo.Repository, kind webhook.EventKind, delivery string) error {
	if c.opts.CheckCloneURL != nil {
		if err := c.opts.CheckCloneURL(r.CloneURL); err != nil {
			c.opts.Logger.ErrorContext(ctx, "repository clone URL rejected", slog.String("repository", r.FullName))
			return errors.New("repository clone URL rejected")
		}
	}
	// The run outlives the HTTP request that asked for it, as for webhooks.
	c.opts.Dispatcher.Dispatch(context.WithoutCancel(ctx), webhook.Event{
		Repository: r,
		Kind:       kind,
		Delivery:   delivery,
	})
	return nil
}

// delivery names the runs of one action in the logs, like the schedule's
// "schedule-<time>".
func (c *Controller) delivery() string {
	return "ui-" + c.opts.Now().UTC().Format(time.RFC3339)
}

func actionMessage(kind webhook.EventKind) string {
	if kind == webhook.EventSync {
		return "sync requested from the UI"
	}

	return "refresh requested from the UI"
}
