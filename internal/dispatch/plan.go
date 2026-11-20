package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

// plan computes what a run of e changes on every target, with read-only
// calls only. It fills the targetRuns, which the apply steps reuse, and
// returns the plan for observers and the log.
func (d *Dispatcher) plan(ctx context.Context, logger *slog.Logger, runs []*targetRun, e webhook.Event) plan.Repo {
	p := plan.Repo{
		Name:     e.Repository.FullName,
		At:       d.opts.Now(),
		Trigger:  eventTrigger(e),
		Retry:    e.Retry,
		ParentID: e.ParentID,
	}

	src, err := d.loadSource(ctx, e, runs)
	if e.Kind == webhook.EventDelete || errors.Is(err, forge.ErrNotFound) {
		p.SourceDeleted = true
		parallel(runs, func(tr *targetRun) { d.planDelete(ctx, tr, e) })
	} else {
		d.planMirror(ctx, logger, runs, e, src, err)
	}

	for _, tr := range runs {
		tr.plan.Name = tr.cfg.Name
		if remote, remoteErr := targetRemote(tr.cfg, e); remoteErr == nil {
			tr.plan.URL = remote.URL
		}
		if tr.err != nil {
			tr.plan.Error = tr.err.Error()
		}
		if p.SourceDeleted && !tr.plan.Delete && tr.plan.Error == "" {
			continue
		}
		p.Targets = append(p.Targets, tr.plan)
	}

	return p
}

// planMirror plans a mirror run. srcErr is the failure to read the source
// API, which only fails typed targets: untyped ones do not need it.
func (d *Dispatcher) planMirror(
	ctx context.Context,
	logger *slog.Logger,
	runs []*targetRun,
	e webhook.Event,
	src sourceState,
	srcErr error,
) {
	if srcErr != nil {
		for _, tr := range runs {
			if tr.typed() {
				tr.err = srcErr
			}
		}
	}

	srcRefs, refsErr := d.refs(ctx, d.sourceRemote(e))
	if refsErr != nil {
		logger.WarnContext(ctx, "listing source refs failed; every target gets a full push",
			slog.Any("error", refsErr))
	} else {
		src.refs = srcRefs
	}
	if srcErr == nil {
		d.loadArtifacts(ctx, runs, e, &src)
	}
	parallel(runs, func(tr *targetRun) {
		if tr.typed() && tr.err == nil {
			d.planForge(ctx, logger, tr, e, src)
		}
		if tr.err == nil {
			d.planRefs(ctx, tr, e, srcRefs, refsErr)
		}
	})
}

// loadSource reads the source repository and, for typed targets, its labels.
// UI actions ask the source even without typed targets when its API is
// configured: its 404 is how a sync of a deleted repository turns into a
// deletion. Pushes and scheduled runs know the repository exists, and a
// delete event knows it is gone, so they skip the call.
func (d *Dispatcher) loadSource(ctx context.Context, e webhook.Event, runs []*targetRun) (sourceState, error) {
	typed := slices.ContainsFunc(runs, (*targetRun).typed)
	uiAction := e.Kind == webhook.EventSync || e.Kind == webhook.EventRefresh
	switch {
	case e.Kind == webhook.EventDelete:
		return sourceState{}, nil
	case d.opts.ForgeSource == nil && typed:
		return sourceState{}, errNoSource
	case d.opts.ForgeSource == nil, !typed && !uiAction:
		return sourceState{}, nil
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
	if !typed {
		return sourceState{repo: src}, nil
	}
	labels, err := d.opts.ForgeSource.Labels(ctx, r.Owner, r.Name)
	if err != nil {
		return sourceState{}, fmt.Errorf("reading source labels: %w", err)
	}

	return sourceState{repo: src, labels: labels}, nil
}

// planForge resolves the desired state of a typed target and compares it
// with the target repository: create, privatise, settings and labels.
func (d *Dispatcher) planForge(
	ctx context.Context,
	logger *slog.Logger,
	tr *targetRun,
	e webhook.Event,
	src sourceState,
) {
	name := tr.cfg.Name
	if tr.api == nil {
		tr.err = fmt.Errorf("target %q: %w", name, errNoClient)

		return
	}

	release, err := d.acquireAPI(ctx)
	if err != nil {
		tr.err = err

		return
	}
	defer release()

	addr, err := tr.cfg.Address(e.Repository)
	if err != nil {
		tr.err = err

		return
	}
	tr.addr = addr
	layers := tr.cfg.Layers(e.Repository.FullName)
	tr.desired, tr.syncLabels = forge.Resolve(src.repo, layers...)
	// The marker goes on after the layers, so that settings.topics or
	// settings.description cannot drop it by accident.
	if tr.desired, err = forge.ResolveMarker(layers...).Apply(tr.desired, e.Repository); err != nil {
		tr.err = fmt.Errorf("target %q: %w", name, err)

		return
	}
	tr.features = forge.ResolveFeatures(layers...)
	tr.labels = src.labels

	// Dropping topics the target rejects keeps one bad topic from failing
	// every settings sync, and keeps it out of the diff so it is not retried
	// on every run.
	var dropped []string
	tr.desired.Topics, dropped = forge.RepresentableTopics(tr.desired.Topics, tr.api.Capabilities())
	for _, topic := range dropped {
		logger.WarnContext(ctx, "topic not supported by the target forge; skipped",
			slog.String("target", name), slog.String("topic", topic))
		tr.plan.Warnings = append(tr.plan.Warnings, fmt.Sprintf("topic %q is not supported by the target forge", topic))
	}

	current, err := tr.api.Get(ctx, addr)
	switch {
	case err == nil:
		tr.current = current
	case !errors.Is(err, forge.ErrNotFound):
		tr.err = fmt.Errorf("target %q: reading repository: %w", name, err)

		return
	case !tr.cfg.Create:
		tr.err = fmt.Errorf("target %q: %w", name, errMissingRepo)

		return
	default:
		// The settings step after the push sets what Create cannot: the
		// default branch, which only exists after the first push.
		tr.plan.Create = true
		tr.current = createdRepo(tr.desired)
	}

	tr.plan.Privatise = !tr.plan.Create && tr.desired.Private && !tr.current.Private
	after := tr.current
	if tr.plan.Privatise {
		after.Private = true
	}
	tr.plan.Settings = plan.SettingsChanges(after, forge.Diff(after, tr.desired, tr.api.Capabilities()))

	d.planLabels(ctx, tr)
	d.planReleases(ctx, tr, src)
	d.planPackages(ctx, tr, src)
}

func (d *Dispatcher) planLabels(ctx context.Context, tr *targetRun) {
	if !tr.syncLabels {
		return
	}
	var have []forge.Label
	if !tr.plan.Create {
		var err error
		if have, err = tr.api.Labels(ctx, tr.addr); err != nil {
			// The labels step lists them again and reports a real failure.
			tr.warnf("listing labels failed: %v", err)

			return
		}
	}
	create, update, remove := forge.LabelChanges(have, tr.labels, tr.cfg.PruneLabels)
	tr.plan.Labels = plan.LabelChanges(create, update, remove, have)
}

// createdRepo is what Create is asked to make: the desired settings without
// the default branch, which does not exist before the first push.
func createdRepo(desired forge.Repo) forge.Repo {
	created := desired
	created.DefaultBranch = ""

	return created
}

// planRefs compares the refs of source and target. Refs that cannot be listed
// are unknown, and the target then gets a full push: git itself decides what
// changes, as before plans existed.
func (d *Dispatcher) planRefs(
	ctx context.Context,
	tr *targetRun,
	e webhook.Event,
	src map[string]string,
	srcErr error,
) {
	if srcErr != nil {
		tr.plan.RefsUnknown = true

		return
	}
	if tr.plan.Create {
		tr.plan.Refs = plan.RefChanges(src, nil)

		return
	}

	remote, err := targetRemote(tr.cfg, e)
	if err != nil {
		tr.err = err

		return
	}
	have, err := d.refs(ctx, remote)
	if err != nil {
		tr.plan.RefsUnknown = true
		tr.plan.Warnings = append(tr.plan.Warnings, fmt.Sprintf("listing target refs failed: %v", err))

		return
	}
	tr.plan.Refs = plan.RefChanges(src, have)
}

// planDelete plans the deletion of a target repository whose source is gone.
// Only typed targets with delete enabled are touched, and only when the
// repository still exists there.
func (d *Dispatcher) planDelete(ctx context.Context, tr *targetRun, e webhook.Event) {
	if !tr.typed() || !tr.cfg.Delete {
		return
	}
	if tr.api == nil {
		tr.err = fmt.Errorf("target %q: %w", tr.cfg.Name, errNoClient)

		return
	}
	addr, err := tr.cfg.Address(e.Repository)
	if err != nil {
		tr.err = err

		return
	}
	tr.addr = addr

	release, err := d.acquireAPI(ctx)
	if err != nil {
		tr.err = err

		return
	}
	defer release()

	_, err = tr.api.Get(ctx, addr)
	switch {
	case errors.Is(err, forge.ErrNotFound):
		return
	case err != nil:
		// Unknown is not "gone": plan the deletion, which treats a missing
		// repository as success anyway.
		tr.plan.Warnings = append(tr.plan.Warnings, fmt.Sprintf("reading repository failed: %v", err))
	}
	tr.plan.Delete = true
}
