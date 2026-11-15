// Package schedule mirrors every repository of the source forge on a fixed
// interval, so targets converge even when webhook deliveries are lost or
// webhooks are disabled.
package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// Observer is notified after every scheduled run; telemetry implements it.
type Observer interface {
	// ScheduledRunFinished reports how many repositories were dispatched,
	// or the error that prevented listing them.
	ScheduledRunFinished(ctx context.Context, repos int, err error)
}

// Options configure [Run].
type Options struct {
	// Interval between runs; ignored when Tick is set.
	Interval time.Duration
	Source   forge.Source
	Filter   interface{ Matches(r repo.Repository) bool }
	// Dispatcher coalesces runs per repository, so a slow run overlapping
	// the next tick is not mirrored twice in parallel.
	Dispatcher webhook.Dispatcher
	Logger     *slog.Logger
	// Observer may be nil.
	Observer Observer
	// Tick replaces the ticker in tests; nil uses time.NewTicker(Interval).
	Tick <-chan time.Time
}

type noopObserver struct{}

func (noopObserver) ScheduledRunFinished(context.Context, int, error) {}

// Run starts a run immediately, so a restart does not wait a whole interval,
// then one on every tick until ctx is done. A failed listing is logged and
// retried on the next tick: an unreachable Forgejo must not end the loop.
func Run(ctx context.Context, opts Options) {
	if opts.Observer == nil {
		opts.Observer = noopObserver{}
	}

	tick := opts.Tick
	if tick == nil {
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	for {
		if ctx.Err() != nil {
			return
		}
		runOnce(ctx, opts)

		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

func runOnce(ctx context.Context, opts Options) {
	started := time.Now().UTC()
	delivery := "schedule-" + started.Format(time.RFC3339)
	logger := opts.Logger.With(slog.String("delivery", delivery))

	all, err := opts.Source.Repos(ctx)
	if err != nil {
		// Aborted by shutdown: says nothing about Forgejo, so neither an
		// error log nor a failed run.
		if ctx.Err() != nil {
			return
		}
		err = fmt.Errorf("listing source repositories: %w", err)
		logger.ErrorContext(ctx, "scheduled run failed", slog.Any("error", err))
		opts.Observer.ScheduledRunFinished(ctx, 0, err)

		return
	}

	dispatched := 0
	for _, r := range all {
		// The dispatcher drains after the scheduler stops; dispatching the
		// rest of a cancelled sweep would only log dropped events.
		if ctx.Err() != nil {
			break
		}
		if !opts.Filter.Matches(r) {
			continue
		}
		// The dispatcher derives the run's context from this one; it must
		// outlive the scheduler so Shutdown drains running mirrors instead
		// of the scheduler's cancellation killing them, as for webhooks.
		opts.Dispatcher.Dispatch(context.WithoutCancel(ctx), webhook.Event{
			Repository: r,
			Kind:       webhook.EventScheduled,
			Delivery:   delivery,
		})
		dispatched++
	}

	logger.InfoContext(ctx, "scheduled run dispatched",
		slog.Int("repositories", dispatched),
		slog.Int("listed", len(all)),
	)
	opts.Observer.ScheduledRunFinished(ctx, dispatched, nil)
}
