package github

import (
	"context"
	"io"

	gh "github.com/google/go-github/v93/github"

	"go.bitfoo.de/forgemirror/internal/forge"
)

// makeLatestLegacy lets GitHub pick the latest release by date and semver
// instead of "the one created last", which a backfill of old releases would
// otherwise get wrong.
const makeLatestLegacy = "legacy"

var _ forge.ReleaseTarget = (*Client)(nil)

// ReleaseCapabilities implements forge.ReleaseTarget.
func (c *Client) ReleaseCapabilities() forge.ReleaseCapabilities {
	return forge.ReleaseCapabilities{Prerelease: true}
}

func toRelease(r *gh.RepositoryRelease) forge.Release {
	rel := forge.Release{
		ID:         r.ID,
		Tag:        r.TagName,
		Name:       r.GetName(),
		Body:       r.GetBody(),
		Prerelease: r.Prerelease,
		Draft:      r.Draft,
		CreatedAt:  r.CreatedAt.Time,
		UploadURL:  r.UploadURL,
	}
	for _, a := range r.Assets {
		rel.Assets = append(rel.Assets, forge.Asset{ID: a.GetID(), Name: a.GetName(), Size: int64(a.GetSize())})
	}

	return rel
}

// Releases implements forge.ReleaseTarget. The listing embeds the assets of
// each release, so no request per release is needed.
func (c *Client) Releases(ctx context.Context, a forge.Address) ([]forge.Release, error) {
	var out []forge.Release

	opts := &gh.ListOptions{PerPage: labelPageSize}

	for {
		page, resp, err := c.api.Repositories.ListReleases(ctx, a.Namespace, a.Name, opts)
		if err != nil {
			return nil, wrap(opName("list releases of", a), resp, err)
		}

		for _, r := range page {
			out = append(out, toRelease(r))
		}

		if resp.NextPage == 0 {
			return out, nil
		}

		opts.Page = resp.NextPage
	}
}

// CreateRelease implements forge.ReleaseTarget.
func (c *Client) CreateRelease(ctx context.Context, a forge.Address, r forge.Release) (forge.Release, error) {
	created, resp, err := c.api.Repositories.CreateRelease(ctx, a.Namespace, a.Name, gh.CreateReleaseRequest{
		TagName:    r.Tag,
		Name:       &r.Name,
		Body:       &r.Body,
		Prerelease: &r.Prerelease,
		MakeLatest: new(makeLatestLegacy),
	})
	if err != nil {
		return forge.Release{}, wrap(opName("create release "+r.Tag+" in", a), resp, err)
	}

	return toRelease(created), nil
}

// UpdateRelease implements forge.ReleaseTarget.
func (c *Client) UpdateRelease(ctx context.Context, a forge.Address, current, desired forge.Release) error {
	_, resp, err := c.api.Repositories.UpdateRelease(ctx, a.Namespace, a.Name, current.ID, gh.UpdateReleaseRequest{
		Name:       &desired.Name,
		Body:       &desired.Body,
		Prerelease: &desired.Prerelease,
	})

	return wrap(opName("update release "+current.Tag+" in", a), resp, err)
}

// DeleteRelease implements forge.ReleaseTarget; the tag stays.
func (c *Client) DeleteRelease(ctx context.Context, a forge.Address, r forge.Release) error {
	resp, err := c.api.Repositories.DeleteRelease(ctx, a.Namespace, a.Name, r.ID)

	return wrap(opName("delete release "+r.Tag+" in", a), resp, err)
}

// UploadAsset implements forge.ReleaseTarget. It goes through the transfer
// client, whose lack of a per-request timeout lets large files finish; the
// library refuses an upload URL outside the configured hosts.
func (c *Client) UploadAsset(
	ctx context.Context,
	a forge.Address,
	r forge.Release,
	name string,
	size int64,
	body io.Reader,
) error {
	_, resp, err := c.uploads.Repositories.UploadReleaseAssetFromRelease(ctx,
		&gh.RepositoryRelease{ID: r.ID, UploadURL: r.UploadURL},
		&gh.UploadOptions{Name: name}, body, size)

	return wrap(opName("upload asset "+name+" to release "+r.Tag+" in", a), resp, err)
}

// DeleteAsset implements forge.ReleaseTarget.
func (c *Client) DeleteAsset(ctx context.Context, a forge.Address, r forge.Release, asset forge.Asset) error {
	resp, err := c.api.Repositories.DeleteReleaseAsset(ctx, a.Namespace, a.Name, asset.ID)

	return wrap(opName("delete asset "+asset.Name+" of release "+r.Tag+" in", a), resp, err)
}
