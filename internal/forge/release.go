package forge

import (
	"context"
	"io"
	"time"
)

// Release is a release of a repository, matched across forges by its tag.
type Release struct {
	// ID is the forge's own identifier on the forge the release was read
	// from; it never crosses forges.
	ID         int64
	Tag        string
	Name       string
	Body       string
	Prerelease bool
	Draft      bool
	CreatedAt  time.Time
	Assets     []Asset
	// UploadURL is where a target forge accepts new assets, when it uses a
	// separate upload endpoint (GitHub); empty otherwise.
	UploadURL string
}

// Asset is a file attached to a release.
type Asset struct {
	// ID is the forge's own identifier, like [Release.ID].
	ID   int64
	Name string
	Size int64
	// URL is the download URL on the source forge; it carries no
	// credentials.
	URL string
}

// ReleaseCapabilities describe what a target forge stores about a release.
type ReleaseCapabilities struct {
	// Prerelease reports whether the forge has a prerelease flag; without
	// one the flag is left out of every comparison.
	Prerelease bool
}

// ReleaseSource reads releases from the source forge.
type ReleaseSource interface {
	// Releases lists all releases, drafts included (with Draft set).
	Releases(ctx context.Context, owner, name string) ([]Release, error)
	// OpenAsset streams the content of an asset of a listed release.
	OpenAsset(ctx context.Context, a Asset) (io.ReadCloser, error)
}

// ReleaseTarget manages releases on a target forge. It is separate from
// [Target] so that a target forge without release support, and every test
// fake, can omit it.
type ReleaseTarget interface {
	Releases(ctx context.Context, a Address) ([]Release, error)
	// CreateRelease creates r without assets and returns the release as the
	// forge stored it, with the ID and UploadURL needed to add assets.
	CreateRelease(ctx context.Context, a Address, r Release) (Release, error)
	// UpdateRelease sets name, body and prerelease of current to the values
	// of desired.
	UpdateRelease(ctx context.Context, a Address, current, desired Release) error
	DeleteRelease(ctx context.Context, a Address, r Release) error
	// UploadAsset adds a file of size bytes, read from body, to r.
	UploadAsset(ctx context.Context, a Address, r Release, name string, size int64, body io.Reader) error
	DeleteAsset(ctx context.Context, a Address, r Release, asset Asset) error
	ReleaseCapabilities() ReleaseCapabilities
}

// Package is a package of the source forge with the tags (versions) it has.
type Package struct {
	Name string
	// Type is the forge's package type, e.g. "container".
	Type string
	Tags []string
}

// PackageTypeContainer is the only package type that is synced.
const PackageTypeContainer = "container"

// PackageSource lists the packages linked to a source repository.
type PackageSource interface {
	Packages(ctx context.Context, owner, name string) ([]Package, error)
}
