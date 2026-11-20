package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func (d *Dispatcher) fetchLFS(ctx context.Context, ws mirror.Workspace, e webhook.Event) error {
	release, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return ws.FetchLFS(ctx, d.sourceRemote(e))
}

func (d *Dispatcher) uploadLFS(ctx context.Context, ws mirror.Workspace, target *config.Target, e webhook.Event) error {
	permit, permitErr := d.beginMutation(e.Repository.FullName, target.Name)
	if permitErr != nil {
		return permitErr
	}
	defer permit()
	remote, err := targetRemote(target, e)
	if err != nil {
		return err
	}
	release, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return ws.PushLFS(ctx, remote)
}

func (d *Dispatcher) repairLFS(
	ctx context.Context,
	logger *slog.Logger,
	runs []*targetRun,
	e webhook.Event,
	start time.Time,
) {
	for _, tr := range runs {
		if tr.plan.Create {
			tr.err = errors.New("LFS repair requires an existing destination; sync first")
		}
		if tr.err != nil {
			d.finishTarget(ctx, logger, tr, start, nil, tr.err)
		}
	}
	d.mirror(ctx, logger, runs, e, start, true, false)
}
