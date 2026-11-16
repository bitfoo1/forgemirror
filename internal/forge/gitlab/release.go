package gitlab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go/v3"

	"go.bitfoo.de/forgemirror/internal/forge"
)

// assetPackage is the generic package that holds release files. GitLab
// releases cannot store uploads themselves, and a link back to the source
// forge would be dead when it is not internet-exposed.
const assetPackage = "release-assets"

const (
	genericType  = "generic"
	maxErrorBody = 512
	releasePage  = 100
)

var _ forge.ReleaseTarget = (*Client)(nil)

// ReleaseCapabilities implements forge.ReleaseTarget: GitLab has no
// prerelease flag.
func (c *Client) ReleaseCapabilities() forge.ReleaseCapabilities {
	return forge.ReleaseCapabilities{Prerelease: false}
}

// assetFile is one file of the asset package.
type assetFile struct {
	pkg, file int64
	size      int64
}

// assetFiles maps tag → file name → file of the asset package. A project
// without the package yields an empty map.
func (c *Client) assetFiles(ctx context.Context, a forge.Address) (map[string]map[string]assetFile, error) {
	op := opName("list release files of", a)
	out := make(map[string]map[string]assetFile)

	opts := &gl.ListProjectPackagesOptions{PackageName: new(assetPackage), PackageType: new(genericType)}
	opts.PerPage = releasePage

	for {
		pkgs, resp, err := c.api.Packages.ListProjectPackages(projectID(a), opts, gl.WithContext(ctx))
		if err != nil {
			return nil, wrap(op, resp, err)
		}

		for _, p := range pkgs {
			if p.Name != assetPackage {
				continue
			}
			files, filesErr := c.packageFiles(ctx, a, p.ID)
			if filesErr != nil {
				return nil, filesErr
			}
			out[p.Version] = files
		}

		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

func (c *Client) packageFiles(ctx context.Context, a forge.Address, pkg int64) (map[string]assetFile, error) {
	out := make(map[string]assetFile)

	opts := &gl.ListPackageFilesOptions{}
	opts.PerPage = releasePage

	for {
		files, resp, err := c.api.Packages.ListPackageFiles(projectID(a), pkg, opts, gl.WithContext(ctx))
		if err != nil {
			return nil, wrap(opName("list release files of", a), resp, err)
		}

		// Uploading a name again adds a file; the newest one is what GitLab
		// serves, and files are listed oldest first.
		for _, f := range files {
			out[f.FileName] = assetFile{pkg: pkg, file: f.ID, size: f.Size}
		}

		if resp.NextPage == 0 {
			return out, nil
		}
		opts.Page = resp.NextPage
	}
}

// Releases implements forge.ReleaseTarget. Assets are the release links;
// their sizes come from the asset package (0 for links to elsewhere, which
// makes them differ from any real file and get replaced).
func (c *Client) Releases(ctx context.Context, a forge.Address) ([]forge.Release, error) {
	var raw []*gl.Release

	opts := &gl.ListReleasesOptions{}
	opts.PerPage = releasePage

	for {
		page, resp, err := c.api.Releases.ListReleases(projectID(a), opts, gl.WithContext(ctx))
		if err != nil {
			return nil, wrap(opName("list releases of", a), resp, err)
		}

		raw = append(raw, page...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if len(raw) == 0 {
		return nil, nil
	}

	files, err := c.assetFiles(ctx, a)
	if err != nil {
		return nil, err
	}

	out := make([]forge.Release, 0, len(raw))
	for _, r := range raw {
		out = append(out, toRelease(r, files[r.TagName]))
	}

	return out, nil
}

func toRelease(r *gl.Release, files map[string]assetFile) forge.Release {
	rel := forge.Release{Tag: r.TagName, Name: r.Name, Body: r.Description}
	if r.CreatedAt != nil {
		rel.CreatedAt = *r.CreatedAt
	}

	for _, l := range r.Assets.Links {
		rel.Assets = append(rel.Assets, forge.Asset{ID: l.ID, Name: l.Name, Size: files[l.Name].size})
	}

	return rel
}

// CreateRelease implements forge.ReleaseTarget.
func (c *Client) CreateRelease(ctx context.Context, a forge.Address, r forge.Release) (forge.Release, error) {
	created, resp, err := c.api.Releases.CreateRelease(projectID(a), &gl.CreateReleaseOptions{
		Name: &r.Name, TagName: &r.Tag, Description: &r.Body,
	}, gl.WithContext(ctx))
	if err != nil {
		return forge.Release{}, wrap(opName("create release "+r.Tag+" in", a), resp, err)
	}

	return toRelease(created, nil), nil
}

// UpdateRelease implements forge.ReleaseTarget.
func (c *Client) UpdateRelease(ctx context.Context, a forge.Address, current, desired forge.Release) error {
	_, resp, err := c.api.Releases.UpdateRelease(projectID(a), current.Tag, &gl.UpdateReleaseOptions{
		Name: &desired.Name, Description: &desired.Body,
	}, gl.WithContext(ctx))

	return wrap(opName("update release "+current.Tag+" in", a), resp, err)
}

// DeleteRelease implements forge.ReleaseTarget. The files of the release go
// with it, so the asset package does not keep orphaned binaries.
func (c *Client) DeleteRelease(ctx context.Context, a forge.Address, r forge.Release) error {
	op := opName("delete release "+r.Tag+" in", a)

	_, resp, err := c.api.Releases.DeleteRelease(projectID(a), r.Tag, gl.WithContext(ctx))
	if err != nil {
		return wrap(op, resp, err)
	}

	files, err := c.assetFiles(ctx, a)
	if err != nil {
		return err
	}
	for _, f := range files[r.Tag] {
		resp, err = c.api.Packages.DeleteProjectPackage(projectID(a), f.pkg, gl.WithContext(ctx))
		if err != nil && !errors.Is(wrap(op, resp, err), forge.ErrNotFound) {
			return wrap(op, resp, err)
		}

		break // every file of a tag belongs to the same package version
	}

	return nil
}

// UploadAsset implements forge.ReleaseTarget: the file goes into the asset
// package, then a link to it is added to the release. The upload is built
// here because the library's retrying client reads the whole body into
// memory to be able to resend it.
func (c *Client) UploadAsset(
	ctx context.Context,
	a forge.Address,
	r forge.Release,
	name string,
	size int64,
	body io.Reader,
) error {
	op := opName("upload asset "+name+" to release "+r.Tag+" in", a)

	path, err := c.api.GenericPackages.FormatPackageURL(projectID(a), assetPackage, r.Tag, name)
	if err != nil {
		return fmt.Errorf("gitlab %s: %w", op, err)
	}
	fileURL := c.api.BaseURL().String() + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fileURL, body)
	if err != nil {
		return fmt.Errorf("gitlab %s: %w", op, err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(bearerHeader, bearerPrefix+c.token.Reveal())

	resp, err := c.transfer.Do(req)
	if err != nil {
		return fmt.Errorf("gitlab %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

		return fmt.Errorf("gitlab %s: unexpected status %s: %s", op, resp.Status, strings.TrimSpace(string(msg)))
	}

	_, linkResp, err := c.api.ReleaseLinks.CreateReleaseLink(projectID(a), r.Tag, &gl.CreateReleaseLinkOptions{
		Name:     &name,
		URL:      &fileURL,
		LinkType: new(gl.PackageLinkType),
	}, gl.WithContext(ctx))

	return wrap(op, linkResp, err)
}

// DeleteAsset implements forge.ReleaseTarget: the link and the file.
func (c *Client) DeleteAsset(ctx context.Context, a forge.Address, r forge.Release, asset forge.Asset) error {
	op := opName("delete asset "+asset.Name+" of release "+r.Tag+" in", a)

	_, resp, err := c.api.ReleaseLinks.DeleteReleaseLink(projectID(a), r.Tag, asset.ID, gl.WithContext(ctx))
	if err != nil {
		return wrap(op, resp, err)
	}

	files, err := c.assetFiles(ctx, a)
	if err != nil {
		return err
	}
	if f, ok := files[r.Tag][asset.Name]; ok {
		resp, err = c.api.Packages.DeletePackageFile(projectID(a), f.pkg, f.file, gl.WithContext(ctx))

		return wrap(op, resp, err)
	}

	return nil
}
