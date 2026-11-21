package control

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// PauseScope admits the complete selection before storing any part of it.
func (c *Controller) PauseScope(ctx context.Context, scope hold.Scope, reason string, until time.Time) error {
	if c.opts.Store == nil {
		return errors.New("publication controls are unavailable")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	for _, target := range scope.Targets {
		if !slices.ContainsFunc(c.opts.Targets, func(t config.Target) bool { return t.Name == target }) {
			return ErrUnknownTarget
		}
	}
	for _, name := range scope.Repositories {
		if strings.ContainsAny(name, "*?") {
			continue
		}
		if _, ok := c.opts.Dispatcher.Known(name); !ok {
			return ErrUnknownRepository
		}
	}
	matched := false
	for _, name := range c.opts.Dispatcher.KnownNames() {
		if _, ok := c.opts.Dispatcher.Known(name); !ok {
			continue
		}
		if slices.ContainsFunc(
			c.opts.Targets,
			func(target config.Target) bool { return scope.Matches(name, target.Name) },
		) {
			matched = true
			break
		}
	}
	if !scope.AllRepositories && !matched {
		return ErrUnknownRepository
	}
	_, err := c.opts.Store.AddHold(ctx, scope, reason, until)
	return err
}

// ResumeHold preflights every affected admission before removing the hold.
// Publication permits still enforce all other holds and approvals when work runs.
func (c *Controller) ResumeHold(ctx context.Context, id string) error {
	if c.opts.Store == nil {
		return errors.New("publication controls are unavailable")
	}
	p, ok := c.opts.Store.Hold(id)
	if !ok {
		return status.ErrUnknownHold
	}
	events, err := c.resumeEvents(p.Scope)
	if err != nil {
		return err
	}
	if _, err = c.opts.Store.ResumeHold(ctx, id); err != nil {
		return err
	}
	for _, event := range events {
		c.opts.Dispatcher.Dispatch(context.WithoutCancel(ctx), event)
	}
	return nil
}

func (c *Controller) resumeEvents(scope hold.Scope) ([]webhook.Event, error) {
	var events []webhook.Event
	names := c.opts.Dispatcher.KnownNames()
	slices.Sort(names)
	for _, name := range names {
		repository, ok := c.opts.Dispatcher.Known(name)
		if !ok {
			continue
		}
		var matchedTargets []string
		for _, target := range c.opts.Targets {
			if scope.Matches(name, target.Name) {
				matchedTargets = append(matchedTargets, target.Name)
			}
		}
		if len(matchedTargets) == 0 {
			continue
		}
		if c.opts.CheckCloneURL != nil {
			if err := c.opts.CheckCloneURL(repository.CloneURL); err != nil {
				return nil, errors.New("repository clone URL rejected")
			}
		}
		events = append(
			events,
			webhook.Event{
				Repository: repository,
				Targets:    matchedTargets,
				Kind:       webhook.EventSync,
				Delivery:   c.delivery(),
			},
		)
	}
	return events, nil
}
