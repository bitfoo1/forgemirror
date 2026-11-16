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

var errNoReleaseSource = errors.New("the source API cannot read releases")

// loadArtifacts reads releases and packages from the source when at least one
// typed target syncs them for this repository. The features are resolved
// here as well as in planForge, because the source must be read once for all
// targets before they are planned in parallel.
func (d *Dispatcher) loadArtifacts(ctx context.Context, runs []*targetRun, e webhook.Event, src *sourceState) {
	var wantReleases, wantPackages bool
	for _, tr := range runs {
		if !tr.typed() || tr.err != nil {
			continue
		}
		f := forge.ResolveFeatures(tr.cfg.Layers(e.Repository.FullName)...)
		wantReleases = wantReleases || f.Releases
		wantPackages = wantPackages || f.Packages
	}

	if wantReleases {
		src.releases, src.releasesErr = d.loadReleases(ctx, e)
	}
	if wantPackages {
		src.packages, src.digests, src.packagesErr = d.loadPackages(ctx, e)
	}
}

func (d *Dispatcher) loadReleases(ctx context.Context, e webhook.Event) ([]forge.Release, error) {
	rs, ok := d.opts.ForgeSource.(forge.ReleaseSource)
	if !ok {
		return nil, errNoReleaseSource
	}

	release, err := d.acquireAPI(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	return rs.Releases(ctx, e.Repository.Owner, e.Repository.Name)
}

// planReleases compares the published source releases with the target's.
// Every failure to read either side leaves the releases out of the plan,
// so that pruning never acts on a partial listing.
func (d *Dispatcher) planReleases(ctx context.Context, tr *targetRun, src sourceState) {
	if !tr.features.Releases {
		return
	}
	rt, ok := tr.api.(forge.ReleaseTarget)
	if !ok {
		tr.warnf("the target forge cannot store releases")

		return
	}
	if src.releasesErr != nil {
		tr.warnf("listing source releases failed: %v", src.releasesErr)

		return
	}

	published := make(map[string]bool, len(src.releases))
	var desired []forge.Release
	for _, r := range src.releases {
		// Drafts have no published tag yet and may hold unreleased content.
		if r.Draft {
			continue
		}
		published[r.Tag] = true
		// The mirror push puts every source tag on the target; a release
		// whose tag the source does not list cannot be created there.
		if src.refs != nil && src.refs["refs/tags/"+r.Tag] == "" {
			tr.warnf("release %s skipped: its tag is missing on the source", r.Tag)

			continue
		}
		desired = append(desired, r)
	}

	var current []forge.Release
	if !tr.plan.Create {
		var err error
		if current, err = rt.Releases(ctx, tr.addr); err != nil {
			tr.warnf("listing releases failed: %v", err)

			return
		}
	}

	tr.plan.Releases = plan.ReleaseChanges(desired, current, published, rt.ReleaseCapabilities(), tr.cfg.PruneReleases)
}

// syncReleases applies the planned release changes in plan order. A failed
// release does not stop the others.
func (d *Dispatcher) syncReleases(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	rt, ok := tr.api.(forge.ReleaseTarget)
	if !ok {
		return nil
	}
	rs, ok := d.opts.ForgeSource.(forge.ReleaseSource)
	if !ok {
		return errNoReleaseSource
	}

	var errs []error
	for _, c := range tr.plan.Releases {
		var err error
		switch c.Kind() {
		case plan.KindCreate:
			err = d.createRelease(ctx, rt, rs, tr, c)
		case plan.KindUpdate:
			err = d.updateRelease(ctx, rt, rs, tr, c)
		default:
			err = rt.DeleteRelease(ctx, tr.addr, *c.Old)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("release %s: %w", c.Tag, err))

			continue
		}
		logRelease(ctx, logger, tr.cfg.Name, c)
	}

	err := errors.Join(errs...)
	d.syncFinished(ctx, logger, tr, opReleases, err)

	return err
}

func (d *Dispatcher) createRelease(
	ctx context.Context,
	rt forge.ReleaseTarget,
	rs forge.ReleaseSource,
	tr *targetRun,
	c plan.ReleaseChange,
) error {
	created, err := rt.CreateRelease(ctx, tr.addr, *c.New)
	if err != nil {
		return err
	}

	return uploadAssets(ctx, rt, rs, tr.addr, created, c.New.Assets, c.AssetsAdded)
}

func (d *Dispatcher) updateRelease(
	ctx context.Context,
	rt forge.ReleaseTarget,
	rs forge.ReleaseSource,
	tr *targetRun,
	c plan.ReleaseChange,
) error {
	if len(c.Fields) > 0 {
		if err := rt.UpdateRelease(ctx, tr.addr, *c.Old, *c.New); err != nil {
			return err
		}
	}

	for _, a := range c.Old.Assets {
		if slices.Contains(c.AssetsRemoved, a.Name) || slices.Contains(c.AssetsReplaced, a.Name) {
			if err := rt.DeleteAsset(ctx, tr.addr, *c.Old, a); err != nil {
				return fmt.Errorf("deleting asset %s: %w", a.Name, err)
			}
		}
	}

	return uploadAssets(
		ctx,
		rt,
		rs,
		tr.addr,
		*c.Old,
		c.New.Assets,
		append(slices.Clone(c.AssetsAdded), c.AssetsReplaced...),
	)
}

// uploadAssets streams the named source assets into the target release.
func uploadAssets(
	ctx context.Context,
	rt forge.ReleaseTarget,
	rs forge.ReleaseSource,
	addr forge.Address,
	rel forge.Release,
	assets []forge.Asset,
	names []string,
) error {
	for _, a := range assets {
		if !slices.Contains(names, a.Name) {
			continue
		}
		if err := uploadAsset(ctx, rt, rs, addr, rel, a); err != nil {
			return fmt.Errorf("uploading asset %s: %w", a.Name, err)
		}
	}

	return nil
}

func uploadAsset(
	ctx context.Context,
	rt forge.ReleaseTarget,
	rs forge.ReleaseSource,
	addr forge.Address,
	rel forge.Release,
	a forge.Asset,
) error {
	body, err := rs.OpenAsset(ctx, a)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	return rt.UploadAsset(ctx, addr, rel, a.Name, a.Size, body)
}
