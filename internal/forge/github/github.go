// Package github implements forge.Target on top of go-github.
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	gh "github.com/google/go-github/v93/github"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

const (
	publicAPIURL = "https://api.github.com"
	// labelPageSize is GitHub's maximum, keeping the request count low.
	labelPageSize = 100
	orgType       = "Organization"
	// maxTopicLen is GitHub's limit for one topic.
	maxTopicLen = 50
)

type foreignNamespaceError struct{ namespace string }

func (e *foreignNamespaceError) Error() string {
	return "cannot create repository in namespace " + e.namespace +
		": not an organisation and not the token's user"
}

// Client manages repositories on GitHub or GitHub Enterprise.
type Client struct {
	api *gh.Client
	// uploads is api on the transfer HTTP client: release assets can take
	// longer than the per-request timeout of API calls.
	uploads *gh.Client
}

var _ forge.Target = (*Client)(nil)

// New builds a client for apiURL. The public API URL uses the library default;
// anything else is treated as GitHub Enterprise, where the library appends
// /api/v3/ itself.
func New(httpClient *http.Client, apiURL string, token config.Secret) (*Client, error) {
	api, err := newAPI(httpClient, apiURL, token)
	if err != nil {
		return nil, err
	}

	uploads, err := newAPI(httpclient.NewTransfer(), apiURL, token)
	if err != nil {
		return nil, err
	}

	return &Client{api: api, uploads: uploads}, nil
}

func newAPI(httpClient *http.Client, apiURL string, token config.Secret) (*gh.Client, error) {
	opts := []gh.ClientOptionsFunc{
		gh.WithHTTPClient(httpClient),
		gh.WithAuthToken(token.Reveal()),
	}

	if strings.TrimRight(apiURL, "/") != publicAPIURL {
		opts = append(opts, gh.WithEnterpriseURLs(apiURL, apiURL))
	}

	api, err := gh.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("github connect: %w", err)
	}

	return api, nil
}

// wrap adds the operation as context and maps 404 to forge.ErrNotFound. The
// library error carries method, URL, status and API message but never the
// request, so the Authorization header cannot leak.
func wrap(op string, resp *gh.Response, err error) error {
	if err == nil {
		return nil
	}

	if resp != nil && resp.Response != nil && resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("github %s: %w: %s", op, forge.ErrNotFound, err.Error())
	}

	return fmt.Errorf("github %s: %w", op, err)
}

func opName(verb string, a forge.Address) string {
	return fmt.Sprintf("%s %s/%s", verb, a.Namespace, a.Name)
}

// Capabilities reports that GitHub stores a homepage, cannot disable pull
// requests and accepts only some topics.
func (c *Client) Capabilities() forge.Capabilities {
	return forge.Capabilities{Website: true, PullRequests: false, ValidTopic: validTopic}
}

// validTopic applies GitHub's topic rule: lower-case letters, digits and
// hyphens, starting with a letter or digit, at most 50 characters. GitHub
// rejects the whole topic list with 422 if one topic breaks it.
func validTopic(t string) bool {
	if t == "" || len(t) > maxTopicLen || t[0] == '-' {
		return false
	}

	for _, r := range t {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}

	return true
}

func (c *Client) topics(in []string) []string {
	kept, _ := forge.RepresentableTopics(in, c.Capabilities())

	return kept
}

// Get implements forge.Target.
func (c *Client) Get(ctx context.Context, a forge.Address) (forge.Repo, error) {
	r, resp, err := c.api.Repositories.Get(ctx, a.Namespace, a.Name)
	if err != nil {
		return forge.Repo{}, wrap(opName("get repository", a), resp, err)
	}

	return forge.Repo{
		Description:   r.GetDescription(),
		Website:       r.GetHomepage(),
		Topics:        c.topics(r.Topics),
		DefaultBranch: r.GetDefaultBranch(),
		Private:       r.GetPrivate(),
		HasIssues:     r.GetHasIssues(),
		HasWiki:       r.GetHasWiki(),
		// HasPullRequests stays false: GitHub exposes no such setting and the
		// capability is off, so callers never compare it.
	}, nil
}

// Create implements forge.Target. Only creation-time fields are sent; the
// remaining settings are applied by Update after the first push.
func (c *Client) Create(ctx context.Context, a forge.Address, r forge.Repo) error {
	op := opName("create repository", a)

	org, err := c.owner(ctx, op, a.Namespace)
	if err != nil {
		return err
	}

	// DefaultBranch stays unset on purpose: the branch does not exist before
	// the first push.
	_, resp, err := c.api.Repositories.Create(ctx, org, gh.CreateRepositoryRequest{
		Name:        a.Name,
		Description: &r.Description,
		Private:     &r.Private,
	})

	return wrap(op, resp, err)
}

// owner returns the organisation to create in, or "" for the token's user. A
// foreign user is refused because GitHub would otherwise silently create the
// repository under the token's own account instead.
func (c *Client) owner(ctx context.Context, op, namespace string) (string, error) {
	u, resp, err := c.api.Users.Get(ctx, namespace)
	if err != nil {
		return "", wrap(op, resp, err)
	}

	if u.GetType() == orgType {
		return namespace, nil
	}

	self, resp, err := c.api.Users.Get(ctx, "")
	if err != nil {
		return "", wrap(op, resp, err)
	}

	if !strings.EqualFold(self.GetLogin(), namespace) {
		return "", fmt.Errorf("github %s: %w", op, &foreignNamespaceError{namespace})
	}

	return "", nil
}

// Update implements forge.Target, sending only the fields that are set.
// HasPullRequests is ignored: GitHub cannot disable pull requests.
func (c *Client) Update(ctx context.Context, a forge.Address, s forge.Settings) error {
	edit := gh.Repository{
		Description:   s.Description,
		Homepage:      s.Website,
		DefaultBranch: s.DefaultBranch,
		Private:       s.Private,
		HasIssues:     s.HasIssues,
		HasWiki:       s.HasWiki,
	}

	// gh.Repository holds a slice and is not comparable, so the settings that
	// map to it are checked directly.
	if s.Description != nil || s.Website != nil || s.DefaultBranch != nil ||
		s.Private != nil || s.HasIssues != nil || s.HasWiki != nil {
		_, resp, err := c.api.Repositories.Edit(ctx, a.Namespace, a.Name, &edit)
		if err != nil {
			return wrap(opName("update repository", a), resp, err)
		}
	}

	if s.Topics != nil {
		_, resp, err := c.api.Repositories.ReplaceAllTopics(ctx, a.Namespace, a.Name, c.topics(*s.Topics))
		if err != nil {
			return wrap(opName("set topics of", a), resp, err)
		}
	}

	return nil
}

// Labels implements forge.Target.
func (c *Client) Labels(ctx context.Context, a forge.Address) ([]forge.Label, error) {
	var labels []forge.Label

	opts := &gh.ListOptions{PerPage: labelPageSize}

	for {
		page, resp, err := c.api.Issues.ListLabels(ctx, a.Namespace, a.Name, opts)
		if err != nil {
			return nil, wrap(opName("list labels of", a), resp, err)
		}

		for _, l := range page {
			labels = append(labels, forge.Label{
				Name:        l.GetName(),
				Color:       forge.NormalizeColor(l.GetColor()),
				Description: l.GetDescription(),
			})
		}

		if resp.NextPage == 0 {
			return labels, nil
		}

		opts.Page = resp.NextPage
	}
}

// CreateLabel implements forge.Target.
func (c *Client) CreateLabel(ctx context.Context, a forge.Address, l forge.Label) error {
	color := forge.NormalizeColor(l.Color)

	_, resp, err := c.api.Issues.CreateLabel(ctx, a.Namespace, a.Name, gh.CreateIssueLabelRequest{
		Name: l.Name, Color: &color, Description: &l.Description,
	})

	return wrap(opName("create label in", a), resp, err)
}

// UpdateLabel implements forge.Target.
func (c *Client) UpdateLabel(ctx context.Context, a forge.Address, l forge.Label) error {
	color := forge.NormalizeColor(l.Color)

	_, resp, err := c.api.Issues.UpdateLabel(
		ctx,
		a.Namespace,
		a.Name,
		url.PathEscape(l.Name),
		gh.UpdateIssueLabelRequest{
			NewName: &l.Name, Color: &color, Description: &l.Description,
		},
	)

	return wrap(opName("update label in", a), resp, err)
}

// DeleteLabel implements forge.Target. The name is path-escaped because the
// library interpolates it into the URL unescaped.
func (c *Client) DeleteLabel(ctx context.Context, a forge.Address, name string) error {
	resp, err := c.api.Issues.DeleteLabel(ctx, a.Namespace, a.Name, url.PathEscape(name))

	return wrap(opName("delete label in", a), resp, err)
}

// Delete implements forge.Target.
func (c *Client) Delete(ctx context.Context, a forge.Address) error {
	resp, err := c.api.Repositories.Delete(ctx, a.Namespace, a.Name)

	return wrap(opName("delete repository", a), resp, err)
}
