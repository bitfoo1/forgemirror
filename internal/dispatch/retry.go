package dispatch

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/retry"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// RetryState stores scheduling metadata separately from executable plans.
type RetryState interface {
	Retries() []retry.Entry
	RecordRetry(context.Context, string, string, string, bool, error) error
}

// StartRetries starts at most one dispatcher-owned scheduler, drained by Shutdown.
func (d *Dispatcher) StartRetries(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.retryStop != nil || d.opts.Retries == nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.retryStop = cancel
	d.wg.Go(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				d.RetryDue(runCtx)
			}
		}
	})
}

// RetryDue admits due work using current trusted repository/target information.
// Waiting never holds a git/API slot or stores a context in a retry entry.
func (d *Dispatcher) RetryDue(ctx context.Context) {
	if d.opts.Retries == nil {
		return
	}
	for _, entry := range d.opts.Retries.Retries() {
		if !retryDue(entry, d.opts.Now(), d.opts.Auto) {
			continue
		}
		d.mu.Lock()
		r, known := d.known[entry.Repo]
		_, busy := d.running[entry.Repo]
		allowed := known && !busy && !d.closed && !d.retryBlocked
		d.mu.Unlock()
		if !allowed ||
			!slices.ContainsFunc(d.opts.Targets, func(t config.Target) bool { return t.Name == entry.Target }) {
			continue
		}
		kind := webhook.EventSync
		if entry.Automatic {
			kind = webhook.EventScheduled
		}
		switch entry.Kind {
		case retry.KindRepair:
			kind = webhook.EventRepairLFS
		case "delete":
			kind = webhook.EventDelete
		}
		d.Dispatch(
			context.WithoutCancel(ctx),
			webhook.Event{
				Repository: r,
				Kind:       kind,
				Target:     entry.Target,
				Retry:      true,
				ParentID:   entry.AttemptID,
				Operator:   !entry.Automatic,
				Delivery:   "retry",
			},
		)
	}
}

func retryDue(entry retry.Entry, now time.Time, auto bool) bool {
	return (entry.State == "waiting" || entry.State == "paused") && !entry.Next.After(now) && (!entry.Automatic || auto)
}

func (d *Dispatcher) clearRetryBudget(ctx context.Context, runs []*targetRun, e webhook.Event, repairOnly bool) {
	if d.opts.Retries == nil {
		return
	}
	for _, tr := range runs {
		if repairOnly && !hasRepairRetry(d.opts.Retries.Retries(), tr.repo, tr.cfg.Name) {
			continue
		}
		d.writeRetry(ctx, e.Repository.FullName, tr.cfg.Name, "sync", !e.Operator, nil)
	}
}

func hasRepairRetry(entries []retry.Entry, repo, target string) bool {
	return slices.ContainsFunc(
		entries,
		func(e retry.Entry) bool { return e.Repo == repo && e.Target == target && e.Kind == retry.KindRepair },
	)
}

func (d *Dispatcher) recordRetryOutcomes(
	ctx context.Context,
	runs []*targetRun,
	e webhook.Event,
	apply, repair, deleted bool,
) {
	if d.opts.Retries == nil {
		return
	}
	kind := "sync"
	if repair && !apply {
		kind = retry.KindRepair
	}
	if deleted {
		kind = "delete"
	}
	for _, tr := range runs {
		d.writeRetry(ctx, e.Repository.FullName, tr.cfg.Name, kind, !e.Operator, errors.Join(tr.err, tr.syncErr))
	}
}

func (d *Dispatcher) writeRetry(ctx context.Context, repo, target, kind string, automatic bool, result error) {
	const checkpointTimeout = 5 * time.Second
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkpointTimeout)
	defer cancel()
	err := d.opts.Retries.RecordRetry(writeCtx, repo, target, kind, automatic, result)
	d.mu.Lock()
	d.retryBlocked = err != nil
	d.mu.Unlock()
	if err != nil {
		d.opts.Logger.ErrorContext(ctx, "retry state could not be saved; automatic retries suspended")
	}
}
