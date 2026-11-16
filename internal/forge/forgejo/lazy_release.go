package forgejo

import (
	"context"
	"io"

	"go.bitfoo.de/forgemirror/internal/forge"
)

func (s lazySource) Releases(ctx context.Context, owner, name string) ([]forge.Release, error) {
	c, err := s.l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return source{c}.Releases(ctx, owner, name)
}

func (s lazySource) OpenAsset(ctx context.Context, a forge.Asset) (io.ReadCloser, error) {
	c, err := s.l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return source{c}.OpenAsset(ctx, a)
}

func (s lazySource) Packages(ctx context.Context, owner, name string) ([]forge.Package, error) {
	c, err := s.l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return source{c}.Packages(ctx, owner, name)
}

// ReleaseCapabilities implements forge.ReleaseTarget without a connection.
func (l *Lazy) ReleaseCapabilities() forge.ReleaseCapabilities {
	return new(Client).ReleaseCapabilities()
}

// Releases implements forge.ReleaseTarget.
func (l *Lazy) Releases(ctx context.Context, a forge.Address) ([]forge.Release, error) {
	c, err := l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return c.Releases(ctx, a)
}

// CreateRelease implements forge.ReleaseTarget.
func (l *Lazy) CreateRelease(ctx context.Context, a forge.Address, r forge.Release) (forge.Release, error) {
	c, err := l.connect(ctx)
	if err != nil {
		return forge.Release{}, err
	}

	return c.CreateRelease(ctx, a, r)
}

// UpdateRelease implements forge.ReleaseTarget.
func (l *Lazy) UpdateRelease(ctx context.Context, a forge.Address, current, desired forge.Release) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.UpdateRelease(ctx, a, current, desired)
}

// DeleteRelease implements forge.ReleaseTarget.
func (l *Lazy) DeleteRelease(ctx context.Context, a forge.Address, r forge.Release) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.DeleteRelease(ctx, a, r)
}

// UploadAsset implements forge.ReleaseTarget.
func (l *Lazy) UploadAsset(
	ctx context.Context,
	a forge.Address,
	r forge.Release,
	name string,
	size int64,
	body io.Reader,
) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.UploadAsset(ctx, a, r, name, size, body)
}

// DeleteAsset implements forge.ReleaseTarget.
func (l *Lazy) DeleteAsset(ctx context.Context, a forge.Address, r forge.Release, asset forge.Asset) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.DeleteAsset(ctx, a, r, asset)
}
