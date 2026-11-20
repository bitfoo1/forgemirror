package control

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// ErrUnknownTarget rejects control requests for unconfigured destinations.
var ErrUnknownTarget = errors.New("unknown target")

func (c *Controller) controlScope(repo, target string) error {
	if !slices.ContainsFunc(c.opts.Targets, func(t config.Target) bool { return t.Name == target }) {
		return ErrUnknownTarget
	}
	if repo != "" {
		if _, ok := c.opts.Dispatcher.Known(repo); !ok {
			return ErrUnknownRepository
		}
	}
	if c.opts.Store == nil {
		return errors.New("publication controls are unavailable")
	}
	return nil
}

// Pause holds a whole target or one admitted repository on it.
func (c *Controller) Pause(ctx context.Context, repo, target, reason string, until time.Time) error {
	if err := c.controlScope(repo, target); err != nil {
		return err
	}
	return c.opts.Store.Pause(ctx, status.Pause{Repo: repo, Target: target, Reason: reason, Until: until})
}

// Resume removes the requested hold and reconciles current state on that target.
func (c *Controller) Resume(ctx context.Context, repo, target string) error {
	if err := c.controlScope(repo, target); err != nil {
		return err
	}
	if err := c.opts.Store.Resume(ctx, repo, target); err != nil {
		return err
	}
	names := []string{repo}
	if repo == "" {
		names = c.opts.Dispatcher.KnownNames()
	}
	for _, name := range names {
		r, ok := c.opts.Dispatcher.Known(name)
		if !ok {
			continue
		}
		if c.opts.CheckCloneURL != nil {
			if err := c.opts.CheckCloneURL(r.CloneURL); err != nil {
				return errors.New("repository clone URL rejected")
			}
		}
		c.opts.Dispatcher.Dispatch(
			context.WithoutCancel(ctx),
			webhook.Event{Repository: r, Kind: webhook.EventSync, Target: target, Delivery: c.delivery()},
		)
	}
	return nil
}
