// Package forgejo implements the forge-neutral Source and Target interfaces
// on top of the Forgejo SDK.
package forgejo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v4"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/retry"
)

// pageSize is Forgejo's default MAX_RESPONSE_ITEMS. An instance may cap pages
// lower, so a page shorter than pageSize does not mean it is the last one.
const pageSize = 50

const headerTotalCount = "X-Total-Count"

const apiSuffix = "/api/v1"

type foreignNamespaceError struct{ namespace string }

func (e *foreignNamespaceError) Error() string {
	return "cannot create repository in namespace " + e.namespace +
		": not an organisation and not the token's user"
}

// Client talks to one Forgejo instance. It is a forge.Target; the source side
// is available through Source because both interfaces declare a Labels method
// with different signatures.
type Client struct {
	httpClient *http.Client
	// transfer carries attachment uploads and downloads, which the API
	// client's per-request timeout would cut off.
	transfer *http.Client
	baseURL  string
	token    config.Secret
	// version is the server version learned in New. The SDK stores its context
	// inside the client, so a client is built per call to honour the caller's
	// context; pinning the version keeps that free of extra requests.
	version string
}

// New connects to the Forgejo instance at baseURL, which may be the root URL
// or the /api/v1 URL (the SDK adds the API path itself).
func New(ctx context.Context, httpClient *http.Client, baseURL string, token config.Secret) (*Client, error) {
	c := &Client{
		httpClient: httpClient,
		transfer:   httpclient.NewTransfer(),
		baseURL:    strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(baseURL, "/"), apiSuffix), "/"),
		token:      token,
	}

	probe, err := c.newSDK(ctx, sdk.SetForgejoVersion(""))
	if err != nil {
		return nil, fmt.Errorf("forgejo connect: %w", err)
	}

	raw, _, err := probe.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("forgejo connect: read server version: %w", err)
	}

	// An unparsable version (custom builds) would make every later client
	// construction fail, so fall back to skipping the SDK's version checks.
	if sdk.SetForgejoVersion(raw)(&sdk.Client{}) == nil {
		c.version = raw
	}

	if _, err = c.sdk(ctx); err != nil {
		return nil, fmt.Errorf("forgejo connect: %w", err)
	}

	return c, nil
}

func (c *Client) newSDK(ctx context.Context, extra ...sdk.ClientOption) (*sdk.Client, error) {
	opts := append([]sdk.ClientOption{
		sdk.SetHTTPClient(c.httpClient),
		sdk.SetToken(c.token.Reveal()),
		sdk.SetContext(ctx),
	}, extra...)

	cl, err := sdk.NewClient(c.baseURL, opts...)
	if err != nil && !errors.Is(err, &sdk.ErrUnknownVersion{}) {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return cl, nil
}

func (c *Client) sdk(ctx context.Context) (*sdk.Client, error) {
	return c.newSDK(ctx, sdk.SetForgejoVersion(c.version))
}

// wrap adds the operation as context and maps 404 to forge.ErrNotFound. The
// SDK reports errors as plain strings, so the status code on the response is
// the only reliable not-found signal.
func wrap(op string, resp *sdk.Response, err error) error {
	if err == nil {
		return nil
	}

	if resp != nil && resp.Response != nil && resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("forgejo %s: %w: %s", op, forge.ErrNotFound, err.Error())
	}

	if resp != nil {
		err = retry.HTTP(err, resp.Response, time.Now())
	}
	return fmt.Errorf("forgejo %s: %w", op, err)
}

func opName(verb, owner, name string) string {
	return fmt.Sprintf("%s %s/%s", verb, owner, name)
}

// paginate fetches pages until X-Total-Count items have arrived or a page is
// empty. Stopping at the first short page would silently truncate listings on
// instances with a MAX_RESPONSE_ITEMS below pageSize; with prune_labels that
// would delete the missing source labels on every target.
func paginate[T any](fetch func(page int) ([]T, *sdk.Response, error)) ([]T, *sdk.Response, error) {
	var all []T

	for page := 1; ; page++ {
		items, resp, err := fetch(page)
		if err != nil {
			return nil, resp, err
		}

		all = append(all, items...)

		total, known := totalCount(resp)
		if len(items) == 0 || (known && len(all) >= total) {
			return all, resp, nil
		}
	}
}

func totalCount(resp *sdk.Response) (int, bool) {
	if resp == nil || resp.Response == nil {
		return 0, false
	}

	total, err := strconv.Atoi(resp.Header.Get(headerTotalCount))

	return total, err == nil
}

func (c *Client) getRepo(ctx context.Context, owner, name string) (forge.Repo, error) {
	api, err := c.sdk(ctx)
	if err != nil {
		return forge.Repo{}, err
	}

	r, resp, err := api.GetRepo(owner, name)
	if err != nil {
		return forge.Repo{}, wrap(opName("get repository", owner, name), resp, err)
	}

	topics, err := c.topics(api, owner, name)
	if err != nil {
		return forge.Repo{}, err
	}

	return forge.Repo{
		Description:     r.Description,
		Website:         r.Website,
		Topics:          topics,
		DefaultBranch:   r.DefaultBranch,
		Private:         r.Private,
		HasIssues:       r.HasIssues,
		HasWiki:         r.HasWiki,
		HasPullRequests: r.HasPullRequests,
	}, nil
}

func (c *Client) topics(api *sdk.Client, owner, name string) ([]string, error) {
	topics, resp, err := paginate(func(page int) ([]string, *sdk.Response, error) {
		return api.ListRepoTopics(owner, name, sdk.ListRepoTopicsOptions{
			Page: page, PageSize: pageSize,
		})
	})
	if err != nil {
		return nil, wrap(opName("list topics of", owner, name), resp, err)
	}

	return topics, nil
}

func (c *Client) labels(ctx context.Context, owner, name string) ([]forge.Label, error) {
	api, err := c.sdk(ctx)
	if err != nil {
		return nil, err
	}

	raw, resp, err := c.rawLabels(api, owner, name)
	if err != nil {
		return nil, wrap(opName("list labels of", owner, name), resp, err)
	}

	labels := make([]forge.Label, 0, len(raw))
	for _, l := range raw {
		labels = append(labels, forge.Label{
			Name:        l.Name,
			Color:       forge.NormalizeColor(l.Color),
			Description: l.Description,
		})
	}

	return labels, nil
}

func (c *Client) rawLabels(api *sdk.Client, owner, name string) ([]*sdk.Label, *sdk.Response, error) {
	return paginate(func(page int) ([]*sdk.Label, *sdk.Response, error) {
		return api.ListRepoLabels(owner, name, sdk.ListLabelsOptions{
			Page: page, PageSize: pageSize,
		})
	})
}

// Source returns the forge.Source view of the client.
func (c *Client) Source() forge.Source { return source{c} }

type source struct{ c *Client }

func (s source) Repo(ctx context.Context, owner, name string) (forge.Repo, error) {
	return s.c.getRepo(ctx, owner, name)
}

func (s source) Labels(ctx context.Context, owner, name string) ([]forge.Label, error) {
	return s.c.labels(ctx, owner, name)
}

func (s source) Repos(ctx context.Context) ([]repo.Repository, error) {
	api, err := s.c.sdk(ctx)
	if err != nil {
		return nil, err
	}

	found, resp, err := paginate(func(page int) ([]*sdk.Repository, *sdk.Response, error) {
		return api.SearchRepos(sdk.SearchRepoOptions{
			Page: page, PageSize: pageSize,
		})
	})
	if err != nil {
		return nil, wrap("search repositories", resp, err)
	}

	repos := make([]repo.Repository, 0, len(found))

	for _, r := range found {
		owner, _, _ := strings.Cut(r.FullName, "/")
		if r.Owner != nil && r.Owner.UserName != "" {
			owner = r.Owner.UserName
		}

		topics, topicsErr := s.c.topics(api, owner, r.Name)
		if topicsErr != nil {
			return nil, topicsErr
		}

		repos = append(repos, repo.Repository{
			Owner:    owner,
			Name:     r.Name,
			FullName: r.FullName,
			CloneURL: r.CloneURL,
			Private:  r.Private,
			Fork:     r.Fork,
			Topics:   topics,
		})
	}

	return repos, nil
}

// Capabilities reports that Forgejo stores a website and pull-request setting.
func (c *Client) Capabilities() forge.Capabilities {
	return forge.Capabilities{Website: true, PullRequests: true}
}

// Get implements forge.Target.
func (c *Client) Get(ctx context.Context, a forge.Address) (forge.Repo, error) {
	return c.getRepo(ctx, a.Namespace, a.Name)
}

// Labels implements forge.Target.
func (c *Client) Labels(ctx context.Context, a forge.Address) ([]forge.Label, error) {
	return c.labels(ctx, a.Namespace, a.Name)
}

// Create implements forge.Target. Only creation-time fields are sent; the
// remaining settings are applied by Update after the first push.
func (c *Client) Create(ctx context.Context, a forge.Address, r forge.Repo) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	// DefaultBranch stays empty on purpose: the branch does not exist before
	// the first push and Forgejo rejects a nonexistent one.
	opt := sdk.CreateRepoOption{Name: a.Name, Description: r.Description, Private: r.Private}
	op := opName("create repository", a.Namespace, a.Name)

	_, resp, err := api.GetOrg(a.Namespace)

	switch {
	case err == nil:
		_, resp, err = api.CreateOrgRepo(a.Namespace, opt)
	case resp != nil && resp.Response != nil && resp.StatusCode == http.StatusNotFound:
		if err = requireSelf(api, op, a.Namespace); err != nil {
			return err
		}
		_, resp, err = api.CreateRepo(opt)
	}

	return wrap(op, resp, err)
}

// requireSelf refuses a namespace that is neither an organisation nor the
// token's user: Forgejo creates a user repository in the token's own account,
// so it would otherwise land somewhere other than the configured URL.
func requireSelf(api *sdk.Client, op, namespace string) error {
	self, resp, err := api.GetMyUserInfo()
	if err != nil {
		return wrap(op, resp, err)
	}

	if !strings.EqualFold(self.UserName, namespace) {
		return fmt.Errorf("forgejo %s: %w", op, &foreignNamespaceError{namespace})
	}

	return nil
}

// Update implements forge.Target, sending only the fields that are set.
func (c *Client) Update(ctx context.Context, a forge.Address, s forge.Settings) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	opt := sdk.EditRepoOption{
		Description:     s.Description,
		Website:         s.Website,
		DefaultBranch:   s.DefaultBranch,
		Private:         s.Private,
		HasIssues:       s.HasIssues,
		HasWiki:         s.HasWiki,
		HasPullRequests: s.HasPullRequests,
	}

	if opt != (sdk.EditRepoOption{}) {
		_, resp, editErr := api.EditRepo(a.Namespace, a.Name, opt)
		if editErr != nil {
			return wrap(opName("update repository", a.Namespace, a.Name), resp, editErr)
		}
	}

	if s.Topics != nil {
		resp, topicsErr := api.SetRepoTopics(a.Namespace, a.Name, *s.Topics)
		if topicsErr != nil {
			return wrap(opName("set topics of", a.Namespace, a.Name), resp, topicsErr)
		}
	}

	return nil
}

// CreateLabel implements forge.Target.
func (c *Client) CreateLabel(ctx context.Context, a forge.Address, l forge.Label) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	_, resp, err := api.CreateLabel(a.Namespace, a.Name, sdk.CreateLabelOption{
		Name: l.Name, Color: forge.NormalizeColor(l.Color), Description: l.Description,
	})

	return wrap(opName("create label in", a.Namespace, a.Name), resp, err)
}

// labelID resolves a label name to its ID; the API addresses labels by ID only.
func (c *Client) labelID(api *sdk.Client, a forge.Address, name string) (int64, error) {
	labels, resp, err := c.rawLabels(api, a.Namespace, a.Name)
	if err != nil {
		return 0, wrap(opName("list labels of", a.Namespace, a.Name), resp, err)
	}

	for _, l := range labels {
		if strings.EqualFold(l.Name, name) {
			return l.ID, nil
		}
	}

	return 0, fmt.Errorf("forgejo find label %q in %s/%s: %w", name, a.Namespace, a.Name, forge.ErrNotFound)
}

// UpdateLabel implements forge.Target, matching the label by name
// case-insensitively.
func (c *Client) UpdateLabel(ctx context.Context, a forge.Address, l forge.Label) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	id, err := c.labelID(api, a, l.Name)
	if err != nil {
		return err
	}

	color := forge.NormalizeColor(l.Color)

	_, resp, err := api.EditLabel(a.Namespace, a.Name, id, sdk.EditLabelOption{
		Name: &l.Name, Color: &color, Description: &l.Description,
	})

	return wrap(opName("update label in", a.Namespace, a.Name), resp, err)
}

// DeleteLabel implements forge.Target, matching the label by name
// case-insensitively.
func (c *Client) DeleteLabel(ctx context.Context, a forge.Address, name string) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	id, err := c.labelID(api, a, name)
	if err != nil {
		return err
	}

	resp, err := api.DeleteLabel(a.Namespace, a.Name, id)

	return wrap(opName("delete label in", a.Namespace, a.Name), resp, err)
}

// Delete implements forge.Target.
func (c *Client) Delete(ctx context.Context, a forge.Address) error {
	api, err := c.sdk(ctx)
	if err != nil {
		return err
	}

	resp, err := api.DeleteRepo(a.Namespace, a.Name)

	return wrap(opName("delete repository", a.Namespace, a.Name), resp, err)
}
