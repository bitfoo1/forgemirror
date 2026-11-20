package dispatch

import (
	"context"
	"log/slog"
	"time"

	"go.bitfoo.de/forgemirror/internal/webhook"
)

const defaultVerificationTimeout = 10 * time.Minute

// verify stays inside the repository's serialized run; it neither dispatches
// another application nor overwrites a newer queued event.
func (d *Dispatcher) verify(ctx context.Context, logger *slog.Logger, e webhook.Event) {
	timeout := d.opts.TransferTimeout
	if timeout <= 0 {
		timeout = defaultVerificationTimeout
	}
	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if e.Kind != webhook.EventDelete {
		e.Kind = webhook.EventRefresh
	}
	checked := d.plan(verifyCtx, logger, d.targetRuns(e.Repository.FullName), e)
	for i := range checked.Targets {
		// Unknown refs permit a full push when applying, but cannot certify a
		// comparison after it. Keep the two meanings distinct for the UI.
		if checked.Targets[i].RefsUnknown && checked.Targets[i].Error == "" {
			checked.Targets[i].Error = "Could not verify refs; refresh to retry."
		}
	}
	d.opts.Observer.PlanFinished(verifyCtx, checked)
}
