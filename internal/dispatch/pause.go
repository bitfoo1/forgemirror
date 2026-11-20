package dispatch

import "context"

// Acquire the permit before the API slot everywhere. Reversing this order
// can deadlock when a pause writer waits behind another operation's permit.
func (d *Dispatcher) mutateAPI(ctx context.Context, tr *targetRun, fn func() error) error {
	return d.mutate(tr, func() error {
		release, err := d.acquireAPI(ctx)
		if err != nil {
			return err
		}
		defer release()
		return fn()
	})
}

func (d *Dispatcher) beginMutation(repo, target string) (func(), error) {
	if d.opts.Mutation == nil {
		return func() {}, nil
	}
	return d.opts.Mutation(repo, target)
}

func (d *Dispatcher) mutate(tr *targetRun, fn func() error) error {
	release, err := d.beginMutation(tr.repo, tr.cfg.Name)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}
