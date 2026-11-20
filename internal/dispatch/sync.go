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
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/repo"
)

// Operations reported through [Observer.SyncFinished].
const (
	opCreate   = "create"
	opSettings = "settings"
	opLabels   = "labels"
	opReleases = "releases"
	opPackages = "packages"
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
	source repo.Repository
	// repo is the full name of the source repository of this run.
	repo string
	cfg  *config.Target
	// api is nil for untyped targets and for typed targets without a client.
	api  forge.Target
	addr forge.Address
	// current is the target repository as last seen; for a repository the
	// plan creates, what Create is asked to make.
	current forge.Repo
	// created means the repository was made in this run and must be read
	// again before its settings are compared.
	created bool
	// plan is this target's part of the run's plan.
	plan       plan.Target
	desired    forge.Repo
	syncLabels bool
	// features says which further parts (releases, packages) the target
	// syncs for this repository.
	features forge.Features
	// labels is shared read-only between all targets of a run.
	labels []forge.Label
	// err removes the target from the remaining steps of the run.
	err error
	// syncErr records failed settings or label steps; the push still counts.
	syncErr error
}

func (tr *targetRun) typed() bool { return tr.cfg.Type != "" }

// warnf adds a note to the target's plan that does not block the sync.
func (tr *targetRun) warnf(format string, args ...any) {
	tr.plan.Warnings = append(tr.plan.Warnings, fmt.Sprintf(format, args...))
}

// sourceState is what a run read from the source, shared read-only by all
// targets.
type sourceState struct {
	repo   forge.Repo
	labels []forge.Label
	// refs are the source refs; nil when they could not be listed.
	refs map[string]string
	// releases and packages are only loaded when a target syncs them; a
	// failed listing is kept, so that no target acts on a partial view.
	releases    []forge.Release
	releasesErr error
	packages    []forge.Package
	packagesErr error
	// digests maps container package → tag → manifest digest.
	digests map[string]map[string]string
}

func (d *Dispatcher) targetRuns(repo string) []*targetRun {
	runs := make([]*targetRun, len(d.opts.Targets))
	for i := range d.opts.Targets {
		t := &d.opts.Targets[i]
		runs[i] = &targetRun{repo: repo, cfg: t, api: d.opts.Forges[t.Name]}
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
	d.finishTarget(ctx, logger, tr, start, nil, err)
}

// prepareTarget makes sure a typed target has a repository to push to:
// it creates a missing one, or makes a public one private, as planned.
func (d *Dispatcher) prepareTarget(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	if !tr.plan.Create && !tr.plan.Privatise {
		return nil
	}

	release, err := d.acquireAPI(ctx)
	if err != nil {
		return err
	}
	defer release()

	if tr.plan.Privatise {
		return d.privatise(ctx, logger, tr)
	}

	err = tr.api.Create(ctx, tr.addr, createdRepo(tr.desired))
	d.syncFinished(ctx, logger, tr, opCreate, err)
	if err != nil {
		return fmt.Errorf("target %q: creating repository: %w", tr.cfg.Name, err)
	}
	tr.created = true
	logger.InfoContext(ctx, "repository created",
		slog.String("target", tr.cfg.Name), slog.Bool("private", tr.desired.Private))

	return nil
}

// privatise makes an existing public target private before anything is
// pushed to it. The settings step runs only after the push and may fail as a
// whole (e.g. on another field), which would leave new commits of a private
// source on a public repository; a failure here keeps the target out of the
// push instead.
func (d *Dispatcher) privatise(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	private := true
	diff := forge.Settings{Private: &private}
	err := tr.api.Update(ctx, tr.addr, diff)
	d.syncFinished(ctx, logger, tr, opSettings, err)
	if err != nil {
		return fmt.Errorf("target %q: making repository private: %w", tr.cfg.Name, err)
	}
	logSettings(ctx, logger, tr.cfg.Name, plan.SettingsChanges(tr.current, diff))
	tr.current.Private = true

	return nil
}

// syncTarget brings settings and labels of a pushed target in line.
func (d *Dispatcher) syncTarget(ctx context.Context, logger *slog.Logger, tr *targetRun) {
	var errs []error

	if err := d.mutateAPI(ctx, tr, opSettings, func() error { return d.syncSettings(ctx, logger, tr) }); err != nil {
		errs = append(errs, fmt.Errorf("target %q: updating settings: %w", tr.cfg.Name, err))
	}

	if tr.syncLabels {
		if err := d.mutateAPI(ctx, tr, opLabels, func() error { return d.syncLabels(ctx, logger, tr) }); err != nil {
			errs = append(errs, fmt.Errorf("target %q: syncing labels: %w", tr.cfg.Name, err))
		}
	}

	// Attachments and image layers can be large; the transfer timeout bounds
	// them instead of the per-request API timeout.
	transferCtx, cancel := d.transferContext(ctx)
	defer cancel()

	if len(tr.plan.Releases) > 0 {
		if err := d.mutateAPI(
			ctx,
			tr,
			opReleases,
			func() error { return d.syncReleases(transferCtx, logger, tr) },
		); err != nil {
			errs = append(errs, fmt.Errorf("target %q: syncing releases: %w", tr.cfg.Name, err))
		}
	}

	if len(tr.plan.Packages) > 0 {
		if err := d.mutateAPI(
			ctx,
			tr,
			opPackages,
			func() error { return d.syncPackages(transferCtx, logger, tr) },
		); err != nil {
			errs = append(errs, fmt.Errorf("target %q: syncing packages: %w", tr.cfg.Name, err))
		}
	}

	tr.syncErr = errors.Join(errs...)
}

func (d *Dispatcher) transferContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.opts.TransferTimeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, d.opts.TransferTimeout)
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
			d.syncFinished(ctx, logger, tr, opSettings, err)

			return err
		}
	}

	diff := forge.Diff(current, tr.desired, tr.api.Capabilities())
	if diff.IsZero() {
		return nil
	}

	err := tr.api.Update(ctx, tr.addr, diff)
	d.syncFinished(ctx, logger, tr, opSettings, err)
	if err == nil {
		logSettings(ctx, logger, tr.cfg.Name, plan.SettingsChanges(current, diff))
	}

	return err
}

// syncLabels reports at most one result per run: a run without label changes
// is not an operation, mirroring the settings step that skips equal settings.
func (d *Dispatcher) syncLabels(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	current, err := tr.api.Labels(ctx, tr.addr)
	if err != nil {
		err = fmt.Errorf("listing labels: %w", err)
		d.syncFinished(ctx, logger, tr, opLabels, err)

		return err
	}

	create, update, remove := forge.LabelChanges(current, tr.labels, tr.cfg.PruneLabels)
	if len(create)+len(update)+len(remove) == 0 {
		return nil
	}

	var errs []error
	var done []plan.LabelChange
	for _, c := range plan.LabelChanges(create, update, remove, current) {
		var opErr error
		switch c.Kind() {
		case plan.KindCreate:
			opErr = tr.api.CreateLabel(ctx, tr.addr, *c.New)
		case plan.KindUpdate:
			opErr = tr.api.UpdateLabel(ctx, tr.addr, *c.New)
		default:
			opErr = tr.api.DeleteLabel(ctx, tr.addr, c.Name)
		}
		if opErr != nil {
			errs = append(errs, fmt.Errorf("%s label %q: %w", labelVerb(c.Kind()), c.Name, opErr))

			continue
		}
		done = append(done, c)
	}
	logLabels(ctx, logger, tr.cfg.Name, done)

	err = errors.Join(errs...)
	d.syncFinished(ctx, logger, tr, opLabels, err)

	return err
}

// deleteRepos deletes the target repositories the plan marked for deletion
// after the source repository was deleted. Nothing is cloned.
func (d *Dispatcher) deleteRepos(ctx context.Context, logger *slog.Logger, runs []*targetRun) error {
	start := time.Now()
	logger.InfoContext(ctx, "deletion started")

	var planned []*targetRun
	for _, tr := range runs {
		if tr.err != nil {
			d.syncFinished(ctx, logger, tr, opDelete, tr.err)
		}
		if tr.err == nil && tr.plan.Delete {
			planned = append(planned, tr)
		}
	}

	parallel(planned, func(tr *targetRun) { tr.err = d.deleteRepo(ctx, logger, tr) })

	if err := runErrors(runs); err != nil {
		logger.ErrorContext(ctx, "deletion finished with errors",
			slog.Duration("duration", time.Since(start)), slog.Any("error", err))

		return err
	}
	logger.InfoContext(ctx, "deletion finished", slog.Duration("duration", time.Since(start)))

	return nil
}

func (d *Dispatcher) deleteRepo(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	err := d.mutateWith(tr, false, func() error { return d.deleteAt(ctx, tr.api, tr.addr) })
	// A repository that is already gone is exactly the desired outcome.
	if errors.Is(err, forge.ErrNotFound) {
		err = nil
	}
	if err != nil {
		err = fmt.Errorf("target %q: deleting repository: %w", tr.cfg.Name, err)
	}
	d.syncFinished(ctx, logger, tr, opDelete, err)
	if err == nil {
		logger.InfoContext(ctx, "repository deleted", slog.String("target", tr.cfg.Name))
	}

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

func (d *Dispatcher) syncFinished(
	ctx context.Context,
	logger *slog.Logger,
	tr *targetRun,
	operation string,
	err error,
) {
	d.opts.Observer.SyncFinished(ctx, tr.repo, tr.cfg.Name, operation, err)

	logger = logger.With(slog.String("target", tr.cfg.Name), slog.String("operation", operation))
	if err != nil {
		logger.ErrorContext(ctx, "forge operation failed", slog.String("outcome", "failure"), slog.Any("error", err))

		return
	}
	// The change records next to each call say what changed; this line only
	// keeps the operation outcome traceable at debug level.
	logger.DebugContext(ctx, "forge operation succeeded", slog.String("outcome", "success"))
}
