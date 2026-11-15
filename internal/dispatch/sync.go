package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// Operations reported through [Observer.SyncFinished].
const (
	opCreate   = "create"
	opSettings = "settings"
	opLabels   = "labels"
	opDelete   = "delete"
)

var (
	errMissingRepo = errors.New("target repository does not exist and create is false")
	errNoClient    = errors.New("no API client for the target type")
	errNoSource    = errors.New("no source API client")
)

// targetRun is the state of one target during one run. Each step hands a
// targetRun to exactly one goroutine, so it needs no lock.
type targetRun struct {
	cfg *config.Target
	// api is nil for untyped targets and for typed targets without a client.
	api  forge.Target
	addr forge.Address
	// current is the target repository as last seen; unset when created.
	current forge.Repo
	// created means the repository was made in this run and must be read
	// again before its settings are compared.
	created    bool
	desired    forge.Repo
	syncLabels bool
	// labels is shared read-only between all targets of a run.
	labels []forge.Label
	// err removes the target from the remaining steps of the run.
	err error
	// syncErr records failed settings or label steps; the push still counts.
	syncErr error
}

func (tr *targetRun) typed() bool { return tr.cfg.Type != "" }

type sourceState struct {
	repo   forge.Repo
	labels []forge.Label
}

func (d *Dispatcher) targetRuns() []*targetRun {
	runs := make([]*targetRun, len(d.opts.Targets))
	for i := range d.opts.Targets {
		t := &d.opts.Targets[i]
		runs[i] = &targetRun{cfg: t, api: d.opts.Forges[t.Name]}
	}

	return runs
}

// parallel runs fn once per run concurrently. wg.Wait makes every write of fn
// to its own targetRun visible to the caller.
func parallel(runs []*targetRun, fn func(tr *targetRun)) {
	var wg sync.WaitGroup
	for _, tr := range runs {
		wg.Go(func() { fn(tr) })
	}
	wg.Wait()
}

func pendingRuns(runs []*targetRun) []*targetRun {
	var out []*targetRun
	for _, tr := range runs {
		if tr.err == nil {
			out = append(out, tr)
		}
	}

	return out
}

func runErrors(runs []*targetRun) error {
	var errs []error
	for _, tr := range runs {
		errs = append(errs, tr.err, tr.syncErr)
	}

	return errors.Join(errs...)
}

// fail removes tr from the run and reports it like a failed push, so a
// target that never got pushed still shows up in the mirror metrics.
func (d *Dispatcher) fail(ctx context.Context, logger *slog.Logger, tr *targetRun, start time.Time, err error) {
	tr.err = err
	d.finishTarget(ctx, logger, tr.cfg.Name, start, err)
}

// prepare loads the source state and makes sure every typed target has a
// repository to push to. Untyped targets are left alone.
func (d *Dispatcher) prepare(
	ctx context.Context,
	logger *slog.Logger,
	runs []*targetRun,
	e webhook.Event,
	start time.Time,
) {
	var typed []*targetRun
	for _, tr := range runs {
		if tr.typed() {
			typed = append(typed, tr)
		}
	}
	if len(typed) == 0 {
		return
	}

	src, err := d.loadSource(ctx, e)
	if err != nil {
		for _, tr := range typed {
			d.fail(ctx, logger, tr, start, err)
		}

		return
	}

	parallel(typed, func(tr *targetRun) {
		if prepErr := d.prepareTarget(ctx, logger, tr, e, src); prepErr != nil {
			d.fail(ctx, logger, tr, start, prepErr)
		}
	})
}

func (d *Dispatcher) loadSource(ctx context.Context, e webhook.Event) (sourceState, error) {
	if d.opts.ForgeSource == nil {
		return sourceState{}, errNoSource
	}

	release, err := d.acquireAPI(ctx)
	if err != nil {
		return sourceState{}, err
	}
	defer release()

	r := e.Repository
	src, err := d.opts.ForgeSource.Repo(ctx, r.Owner, r.Name)
	if err != nil {
		return sourceState{}, fmt.Errorf("reading source repository: %w", err)
	}
	labels, err := d.opts.ForgeSource.Labels(ctx, r.Owner, r.Name)
	if err != nil {
		return sourceState{}, fmt.Errorf("reading source labels: %w", err)
	}

	return sourceState{repo: src, labels: labels}, nil
}

// prepareTarget resolves the desired state of tr and creates its repository
// when it is missing and creation is allowed.
func (d *Dispatcher) prepareTarget(
	ctx context.Context,
	logger *slog.Logger,
	tr *targetRun,
	e webhook.Event,
	src sourceState,
) error {
	name := tr.cfg.Name
	if tr.api == nil {
		return fmt.Errorf("target %q: %w", name, errNoClient)
	}

	release, err := d.acquireAPI(ctx)
	if err != nil {
		return err
	}
	defer release()

	addr, err := tr.cfg.Address(e.Repository)
	if err != nil {
		return err
	}
	tr.addr = addr
	tr.desired, tr.syncLabels = forge.Resolve(src.repo, tr.cfg.Layers(e.Repository.FullName)...)
	tr.labels = src.labels

	current, err := tr.api.Get(ctx, addr)
	switch {
	case err == nil:
		tr.current = current

		return nil
	case !errors.Is(err, forge.ErrNotFound):
		return fmt.Errorf("target %q: reading repository: %w", name, err)
	case !tr.cfg.Create:
		return fmt.Errorf("target %q: %w", name, errMissingRepo)
	}

	// The default branch only exists after the first push; it is set by the
	// settings step afterwards.
	created := tr.desired
	created.DefaultBranch = ""

	err = tr.api.Create(ctx, addr, created)
	d.syncFinished(ctx, logger, name, opCreate, err)
	if err != nil {
		return fmt.Errorf("target %q: creating repository: %w", name, err)
	}
	tr.created = true

	return nil
}

// syncTarget brings settings and labels of a pushed target in line.
func (d *Dispatcher) syncTarget(ctx context.Context, logger *slog.Logger, tr *targetRun) {
	release, slotErr := d.acquireAPI(ctx)
	if slotErr != nil {
		tr.syncErr = fmt.Errorf("target %q: %w", tr.cfg.Name, slotErr)

		return
	}
	defer release()

	var errs []error

	if err := d.syncSettings(ctx, logger, tr); err != nil {
		errs = append(errs, fmt.Errorf("target %q: updating settings: %w", tr.cfg.Name, err))
	}

	if tr.syncLabels {
		if err := d.syncLabels(ctx, logger, tr); err != nil {
			errs = append(errs, fmt.Errorf("target %q: syncing labels: %w", tr.cfg.Name, err))
		}
	}

	tr.syncErr = errors.Join(errs...)
}

func (d *Dispatcher) syncSettings(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	current := tr.current
	if tr.created {
		// Create sends only a subset of the settings (Forgejo: name,
		// description, private), and forges fill the rest with their own
		// defaults, so only a fresh read shows what still differs.
		var err error
		if current, err = tr.api.Get(ctx, tr.addr); err != nil {
			err = fmt.Errorf("reading created repository: %w", err)
			d.syncFinished(ctx, logger, tr.cfg.Name, opSettings, err)

			return err
		}
	}

	diff := forge.Diff(current, tr.desired, tr.api.Capabilities())
	if diff.IsZero() {
		return nil
	}

	err := tr.api.Update(ctx, tr.addr, diff)
	d.syncFinished(ctx, logger, tr.cfg.Name, opSettings, err)

	return err
}

// syncLabels reports at most one result per run: a run without label changes
// is not an operation, mirroring the settings step that skips equal settings.
func (d *Dispatcher) syncLabels(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	current, err := tr.api.Labels(ctx, tr.addr)
	if err != nil {
		err = fmt.Errorf("listing labels: %w", err)
		d.syncFinished(ctx, logger, tr.cfg.Name, opLabels, err)

		return err
	}

	create, update, remove := forge.LabelChanges(current, tr.labels, tr.cfg.PruneLabels)
	if len(create)+len(update)+len(remove) == 0 {
		return nil
	}

	var errs []error
	for _, l := range create {
		if opErr := tr.api.CreateLabel(ctx, tr.addr, l); opErr != nil {
			errs = append(errs, fmt.Errorf("creating label %q: %w", l.Name, opErr))
		}
	}
	for _, l := range update {
		if opErr := tr.api.UpdateLabel(ctx, tr.addr, l); opErr != nil {
			errs = append(errs, fmt.Errorf("updating label %q: %w", l.Name, opErr))
		}
	}
	for _, l := range remove {
		if opErr := tr.api.DeleteLabel(ctx, tr.addr, l.Name); opErr != nil {
			errs = append(errs, fmt.Errorf("deleting label %q: %w", l.Name, opErr))
		}
	}

	err = errors.Join(errs...)
	d.syncFinished(ctx, logger, tr.cfg.Name, opLabels, err)

	return err
}

// deleteRepos handles a deleted source repository. Only typed targets with
// delete enabled are touched; nothing is cloned and the source API is not
// asked, since the repository no longer exists there.
func (d *Dispatcher) deleteRepos(ctx context.Context, logger *slog.Logger, e webhook.Event) {
	start := time.Now()
	logger.InfoContext(ctx, "deletion started")

	var runs []*targetRun
	for _, tr := range d.targetRuns() {
		if !tr.typed() || !tr.cfg.Delete {
			logger.DebugContext(ctx, "target keeps the repository; delete is not enabled",
				slog.String("target", tr.cfg.Name))

			continue
		}
		runs = append(runs, tr)
	}

	parallel(runs, func(tr *targetRun) { tr.err = d.deleteRepo(ctx, logger, tr, e) })

	if err := runErrors(runs); err != nil {
		logger.ErrorContext(ctx, "deletion finished with errors",
			slog.Duration("duration", time.Since(start)), slog.Any("error", err))

		return
	}
	logger.InfoContext(ctx, "deletion finished", slog.Duration("duration", time.Since(start)))
}

func (d *Dispatcher) deleteRepo(ctx context.Context, logger *slog.Logger, tr *targetRun, e webhook.Event) error {
	err := errNoClient
	if tr.api != nil {
		var addr forge.Address
		if addr, err = tr.cfg.Address(e.Repository); err == nil {
			err = d.deleteAt(ctx, tr.api, addr)
		}
	}
	// A repository that is already gone is exactly the desired outcome.
	if errors.Is(err, forge.ErrNotFound) {
		err = nil
	}
	if err != nil {
		err = fmt.Errorf("target %q: deleting repository: %w", tr.cfg.Name, err)
	}
	d.syncFinished(ctx, logger, tr.cfg.Name, opDelete, err)

	return err
}

func (d *Dispatcher) deleteAt(ctx context.Context, api forge.Target, addr forge.Address) error {
	release, err := d.acquireAPI(ctx)
	if err != nil {
		return err
	}
	defer release()

	return api.Delete(ctx, addr)
}

func (d *Dispatcher) syncFinished(ctx context.Context, logger *slog.Logger, target, operation string, err error) {
	d.opts.Observer.SyncFinished(ctx, target, operation, err)

	logger = logger.With(slog.String("target", target), slog.String("operation", operation))
	if err != nil {
		logger.ErrorContext(ctx, "forge operation failed", slog.String("outcome", "failure"), slog.Any("error", err))

		return
	}
	logger.InfoContext(ctx, "forge operation succeeded", slog.String("outcome", "success"))
}
