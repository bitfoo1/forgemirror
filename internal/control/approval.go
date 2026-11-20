package control

import (
	"context"
	"errors"
	"slices"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// ErrStaleApproval prevents an old browser form authorizing a remapped destination.
var ErrStaleApproval = errors.New("source or destination changed; refresh before approving")

// Approve grants ongoing destination authorization, then requests a fresh sync.
func (c *Controller) Approve(ctx context.Context, repo, target, identity string) error {
	if err := c.controlScope(repo, target); err != nil {
		return err
	}
	source, ok := c.opts.Dispatcher.Known(repo)
	if !ok {
		return ErrUnknownRepository
	}
	index := slices.IndexFunc(c.opts.Targets, func(t config.Target) bool { return t.Name == target })
	destination := &c.opts.Targets[index]
	if !destination.RequiresFirstPublicationApproval(repo, c.opts.FirstPublicationApproval) {
		return errors.New("first-publication approval is not required for this scope")
	}
	if c.opts.CheckCloneURL != nil {
		if err := c.opts.CheckCloneURL(source.CloneURL); err != nil {
			return errors.New("repository clone URL rejected")
		}
	}
	remote, err := destination.RemoteURL(source)
	if err != nil {
		return err
	}
	current := status.PublicationID(source.CloneURL, remote)
	if identity != current {
		return ErrStaleApproval
	}
	if err = c.opts.Store.Approve(ctx, repo, target, current); err != nil {
		return err
	}
	c.opts.Logger.InfoContext(ctx, "publication destination approved", "repository", repo, "target", target)
	c.opts.Dispatcher.Dispatch(
		context.WithoutCancel(ctx),
		webhook.Event{Repository: source, Kind: webhook.EventSync, Target: target, Delivery: c.delivery()},
	)
	return nil
}
