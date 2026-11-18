package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/registry"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

var (
	errNoPackageSource = errors.New("the source API cannot list packages")
	errNoRegistry      = errors.New("no registry client")
)

// loadPackages lists the packages linked to the repository and the digests
// of every tag of its container packages.
func (d *Dispatcher) loadPackages(
	ctx context.Context,
	e webhook.Event,
) ([]forge.Package, map[string]map[string]string, error) {
	ps, ok := d.opts.ForgeSource.(forge.PackageSource)
	if !ok {
		return nil, nil, errNoPackageSource
	}
	if d.opts.Registry == nil {
		return nil, nil, errNoRegistry
	}

	release, err := d.acquireAPI(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	pkgs, err := ps.Packages(ctx, e.Repository.Owner, e.Repository.Name)
	if err != nil {
		return nil, nil, err
	}

	digests := make(map[string]map[string]string)
	for _, p := range pkgs {
		if p.Type != forge.PackageTypeContainer {
			continue
		}
		srcRepo, repoErr := d.sourceImage(e.Repository.Owner, p.Name)
		if repoErr != nil {
			return nil, nil, repoErr
		}
		tags, digestErr := d.opts.Registry.Digests(ctx, srcRepo, p.Tags, false)
		if digestErr != nil {
			return nil, nil, digestErr
		}
		digests[p.Name] = tags
	}

	return pkgs, digests, nil
}

// planPackages compares the tags of every container package with the
// target registry. Packages are visible to everyone who can see the owner
// on some forges, independent of the repository, so private repositories
// never get them.
func (d *Dispatcher) planPackages(ctx context.Context, tr *targetRun, src sourceState) {
	if !tr.features.Packages {
		return
	}
	if tr.desired.Private {
		tr.warnf("packages are not synced for private repositories")

		return
	}
	if src.packagesErr != nil {
		tr.warnf("listing source packages failed: %v", src.packagesErr)

		return
	}

	for _, p := range src.packages {
		if p.Type != forge.PackageTypeContainer {
			tr.warnf("package %s skipped: only container packages are synced, not %s", p.Name, p.Type)

			continue
		}
		srcTags := src.digests[p.Name]
		if len(srcTags) == 0 {
			continue
		}

		dst, err := targetImage(tr, p.Name)
		if err != nil {
			tr.warnf("package %s skipped: %v", p.Name, err)

			continue
		}
		have, err := d.opts.Registry.Digests(ctx, dst, slices.Sorted(maps.Keys(srcTags)), true)
		if err != nil {
			tr.warnf("package %s skipped: %v", p.Name, err)

			continue
		}
		if tags := plan.TagChanges(srcTags, have); len(tags) > 0 {
			tr.plan.Packages = append(
				tr.plan.Packages,
				plan.PackageChange{Package: p.Name, Image: dst.String(), Tags: tags},
			)
		}
	}
}

// syncPackages copies the planned tags. A failed tag does not stop the
// others, except for an access denial (see [Dispatcher.copyTags]).
func (d *Dispatcher) syncPackages(ctx context.Context, logger *slog.Logger, tr *targetRun) error {
	owner, _, _ := strings.Cut(tr.repo, "/")

	var errs []error
	for _, c := range tr.plan.Packages {
		copied, err := d.copyTags(ctx, tr, owner, c)
		if err != nil {
			errs = append(errs, fmt.Errorf("package %s: %w", c.Package, err))
		}
		logPackageTags(ctx, logger, tr.cfg.Name, c, copied)
	}

	err := errors.Join(errs...)
	d.syncFinished(ctx, logger, tr, opPackages, err)

	return err
}

func (d *Dispatcher) copyTags(
	ctx context.Context,
	tr *targetRun,
	owner string,
	c plan.PackageChange,
) ([]string, error) {
	src, err := d.sourceImage(owner, c.Package)
	if err != nil {
		return nil, err
	}
	dst, err := targetImage(tr, c.Package)
	if err != nil {
		return nil, err
	}

	var copied []string
	var errs []error
	for i, t := range c.Tags {
		copyErr := d.opts.Registry.Copy(ctx, src, dst, t.Tag)
		if copyErr == nil {
			copied = append(copied, t.Tag)

			continue
		}
		// A denial comes from the token or its scope, so every further tag of
		// the package would fail the same way and only repeat the error.
		if registry.IsDenied(copyErr) {
			if rest := len(c.Tags) - i - 1; rest > 0 {
				copyErr = fmt.Errorf("%w (access denied, %d remaining tags skipped)", copyErr, rest)
			}
			errs = append(errs, copyErr)

			break
		}
		errs = append(errs, copyErr)
	}

	return copied, errors.Join(errs...)
}

// sourceImage is the repository of a Forgejo container package: the
// registry on the instance host, below the package owner.
func (d *Dispatcher) sourceImage(owner, pkg string) (registry.Repository, error) {
	host, insecure, err := d.opts.Source.RegistryHost()
	if err != nil {
		return registry.Repository{}, err
	}

	return registry.Repository{
		Host:     host,
		Path:     strings.ToLower(owner + "/" + pkg),
		Insecure: insecure,
		Username: d.opts.Source.Username,
		Password: d.opts.Source.Token,
	}, nil
}

// targetImage is where a package goes on the target: below the namespace of
// the target repository, keeping the package name. GitLab only accepts
// images inside a project, so there the project path comes first, and a
// package named like the repository becomes the project's root image.
func targetImage(tr *targetRun, pkg string) (registry.Repository, error) {
	host, insecure, err := tr.cfg.RegistryHost()
	if err != nil {
		return registry.Repository{}, err
	}

	path := tr.addr.Namespace + "/" + pkg
	if tr.cfg.Type == forge.TypeGitLab {
		_, srcName, _ := strings.Cut(tr.repo, "/")
		path = tr.addr.Namespace + "/" + tr.addr.Name
		if !strings.EqualFold(pkg, srcName) {
			path += "/" + pkg
		}
	}

	return registry.Repository{
		Host:     host,
		Path:     strings.ToLower(path),
		Insecure: insecure,
		Username: tr.cfg.Username,
		Password: tr.cfg.Token,
	}, nil
}
