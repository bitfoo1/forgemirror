package dispatch

import (
	"context"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/repo"
)

// Permits always precede API slots; reversing the order can deadlock when
// an operator control writer waits behind another operation's permit.
func (d *Dispatcher) mutateAPI(ctx context.Context, tr *targetRun, operation string, fn func() error) error {
	started := false
	err := d.mutate(tr, func() error {
		release, slotErr := d.acquireAPI(ctx)
		if slotErr != nil {
			return slotErr
		}
		defer release()
		started = true
		return fn()
	})
	if err != nil && !started {
		d.opts.Observer.SyncFinished(ctx, tr.repo, tr.cfg.Name, operation, err)
	}
	return err
}

func (d *Dispatcher) beginMutation(source repo.Repository, target *config.Target, publication bool) (func(), error) {
	if d.opts.Mutation == nil {
		return func() {}, nil
	}
	return d.opts.Mutation(source, target, publication)
}

func (d *Dispatcher) mutate(tr *targetRun, fn func() error) error { return d.mutateWith(tr, true, fn) }

func (d *Dispatcher) mutateWith(tr *targetRun, publication bool, fn func() error) error {
	release, err := d.beginMutation(tr.source, tr.cfg, publication)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}
