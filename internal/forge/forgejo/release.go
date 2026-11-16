package forgejo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"

	sdk "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v4"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

// untaggedPrefix marks container package versions that are bare manifests
// ("sha256:<hex>"), reachable only through an index or tag that refers to
// them; copying a tag copies them along.
const untaggedPrefix = "sha256:"

// maxErrorBody bounds how much of an error response is quoted in an error.
const maxErrorBody = 512

var (
	_ forge.ReleaseTarget = (*Client)(nil)
	_ forge.ReleaseSource = source{}
	_ forge.PackageSource = source{}
)

func (c *Client) releases(ctx context.Context, owner, name string) ([]forge.Release, error) {
	api, err := c.sdk(ctx)
	if err != nil {
		return nil, err
	}

	raw, resp, err := paginate(func(page int) ([]*sdk.Release, *sdk.Response, error) {
		return api.ListReleases(owner, name, sdk.ListReleasesOptions{Page: page, PageSize: pageSize})
	})
	if err != nil {
		return nil, wrap(opName("list releases of", owner, name), resp, err)
	}

	out := make([]forge.Release, 0, len(raw))
	for _, r := range raw {
		out = append(out, toRelease(r))
	}

	return out, nil
}

func toRelease(r *sdk.Release) forge.Release {
	rel := forge.Release{
		ID:         r.ID,
		Tag:        r.TagName,
		Name:       r.Title,
		Body:       r.Note,
		Prerelease: r.IsPrerelease,
		Draft:      r.IsDraft,
		CreatedAt:  r.CreatedAt,
	}
	// Forgejo shows the tag for a release without a title but refuses to
	// create one; using the tag keeps source and target comparable.
	if strings.TrimSpace(rel.Name) == "" {
		rel.Name = rel.Tag
	}
	for _, a := range r.Attachments {
		rel.Assets = append(rel.Assets, forge.Asset{ID: a.ID, Name: a.Name, Size: a.Size, URL: a.DownloadURL})
	}

	return rel
}

func (s source) Releases(ctx context.Context, owner, name string) ([]forge.Release, error) {
	return s.c.releases(ctx, owner, name)
}

// OpenAsset streams an attachment. The download URL comes from the API
// response, so it is checked against the instance URL before the token is
// sent with it.
func (s source) OpenAsset(ctx context.Context, a forge.Asset) (io.ReadCloser, error) {
	if err := (config.Source{BaseURL: s.c.baseURL}).CheckCloneURL(a.URL); err != nil {
		return nil, fmt.Errorf("forgejo download %s: %w", a.Name, err)
	}

	body, err := httpclient.Download(ctx, s.c.transfer, a.URL, "token "+s.c.token.Reveal())
	if err != nil {
		return nil, fmt.Errorf("forgejo download %s: %w", a.Name, err)
	}

	return body, nil
}

// Packages lists the packages linked to owner/name, one entry per package
// with all its tags. Forgejo lists one entry per version and owner, so the
// entries are grouped and filtered here.
func (s source) Packages(ctx context.Context, owner, name string) ([]forge.Package, error) {
	api, err := s.c.sdk(ctx)
	if err != nil {
		return nil, err
	}

	raw, resp, err := paginate(func(page int) ([]*sdk.Package, *sdk.Response, error) {
		return api.ListPackages(owner, sdk.ListPackagesOptions{Page: page, PageSize: pageSize})
	})
	if err != nil {
		return nil, wrap("list packages of "+owner, resp, err)
	}

	fullName := owner + "/" + name
	byKey := make(map[[2]string]*forge.Package)
	var keys [][2]string
	for _, p := range raw {
		if p.Repository == nil || !strings.EqualFold(p.Repository.FullName, fullName) {
			continue
		}
		key := [2]string{p.Type, p.Name}
		pkg, ok := byKey[key]
		if !ok {
			pkg = &forge.Package{Name: p.Name, Type: p.Type}
			byKey[key] = pkg
			keys = append(keys, key)
		}
		if p.Type == forge.PackageTypeContainer && strings.HasPrefix(p.Version, untaggedPrefix) {
			continue
		}
		pkg.Tags = append(pkg.Tags, p.Version)
	}

	out := make([]forge.Package, 0, len(keys))
	for _, key := range keys {
		pkg := byKey[key]
		slices.Sort(pkg.Tags)
		out = append(out, *pkg)
	}

	return out, nil
}

// ReleaseCapabilities implements forge.ReleaseTarget.
func (c *Client) ReleaseCapabilities() forge.ReleaseCapabilities {
	return forge.ReleaseCapabilities{Prerelease: true}
}

// Releases implements forge.ReleaseTarget.
func (c *Client) Releases(ctx context.Context, a forge.Address) ([]forge.Release, error) {
	return c.releases(ctx, a.Namespace, a.Name)
}

// CreateRelease implements forge.ReleaseTarget.
func (c *Client) CreateRelease(ctx context.Context, a forge.Address, r forge.Release) (forge.Release, error) {
	api, err := c.sdk(ctx)
	if err != nil {
		return forge.Release{}, err
	}

	created, resp, err := api.CreateRelease(a.Namespace, a.Name, sdk.CreateReleaseOption{
		TagName: r.Tag, Title: r.Name, Note: r.Body, IsPrerelease: r.Prerelease,
	})
	if err != nil {
		return forge.Release{}, wrap(opName("create release "+r.Tag+" in", a.Namespace, a.Name), resp, err)
	}

	return toRelease(created), nil
}

// UpdateRelease implements forge.ReleaseTarget.
func (c *Client) UpdateRelease(ctx context.Context, a forge.Address, current, desired forge.Release) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	_, resp, err := api.EditRelease(a.Namespace, a.Name, current.ID, sdk.EditReleaseOption{
		Title: desired.Name, Note: desired.Body, IsPrerelease: &desired.Prerelease,
	})

	return wrap(opName("update release "+current.Tag+" in", a.Namespace, a.Name), resp, err)
}

// DeleteRelease implements forge.ReleaseTarget; the tag stays.
func (c *Client) DeleteRelease(ctx context.Context, a forge.Address, r forge.Release) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	resp, err := api.DeleteRelease(a.Namespace, a.Name, r.ID)

	return wrap(opName("delete release "+r.Tag+" in", a.Namespace, a.Name), resp, err)
}

// DeleteAsset implements forge.ReleaseTarget.
func (c *Client) DeleteAsset(ctx context.Context, a forge.Address, r forge.Release, asset forge.Asset) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	resp, err := api.DeleteReleaseAttachment(a.Namespace, a.Name, r.ID, asset.ID)

	return wrap(opName("delete asset "+asset.Name+" of release "+r.Tag+" in", a.Namespace, a.Name), resp, err)
}

// UploadAsset implements forge.ReleaseTarget. The SDK builds the multipart
// body in memory, which would hold a whole release binary per upload; the
// request is built here instead and streamed through a pipe.
func (c *Client) UploadAsset(
	ctx context.Context,
	a forge.Address,
	r forge.Release,
	name string,
	_ int64,
	body io.Reader,
) error {
	op := opName("upload asset "+name+" to release "+r.Tag+" in", a.Namespace, a.Name)
	endpoint := fmt.Sprintf("%s%s/repos/%s/%s/releases/%d/assets?name=%s", c.baseURL, apiSuffix,
		url.PathEscape(a.Namespace), url.PathEscape(a.Name), r.ID, url.QueryEscape(name))

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("attachment", name)
		if err == nil {
			_, err = io.Copy(part, body)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		_ = pr.CloseWithError(err)

		return fmt.Errorf("forgejo %s: %w", op, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "token "+c.token.Reveal())

	resp, err := c.transfer.Do(req)
	_ = pr.CloseWithError(errors.New("request finished"))
	if err != nil {
		return fmt.Errorf("forgejo %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

		return fmt.Errorf("forgejo %s: unexpected status %s: %s", op, resp.Status, strings.TrimSpace(string(msg)))
	}

	return nil
}
