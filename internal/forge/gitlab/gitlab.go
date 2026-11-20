// Package gitlab implements forge.Target on top of the GitLab client library.
package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gl "gitlab.com/gitlab-org/api/client-go/v3"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
	"go.bitfoo.de/forgemirror/internal/retry"
)

const (
	// labelPageSize is GitLab's maximum, keeping the request count low.
	labelPageSize = 100
	// retryMax bounds the library's default of five retries so a failing
	// server cannot hold a run past mirror.timeout; the context still wins.
	retryMax      = 2
	groupKind     = "group"
	userKind      = "user"
	bearerHeader  = "Authorization"
	bearerPrefix  = "Bearer "
	errTokenEmpty = "gitlab connect: empty token"
)

type foreignNamespaceError struct{ namespace string }

func (e *foreignNamespaceError) Error() string {
	return "cannot create repository in namespace " + e.namespace +
		": not an organisation and not the token's user"
}

// bearerAuth sends the token as "Authorization: Bearer". The library's
// default access-token source would use PRIVATE-TOKEN, and the OAuth source
// needs golang.org/x/oauth2 as a direct dependency just to wrap a string.
// Bearer works for personal, project and group access tokens alike.
type bearerAuth struct{ token config.Secret }

func (bearerAuth) Init(context.Context, *gl.Client) error { return nil }

func (a bearerAuth) Header(context.Context) (string, string, error) {
	return bearerHeader, bearerPrefix + a.token.Reveal(), nil
}

// Client manages projects on GitLab.com or a self-managed instance.
type Client struct {
	api *gl.Client
	// transfer and token carry release file uploads, which bypass the
	// library (see UploadAsset).
	transfer *http.Client
	token    config.Secret
}

var _ forge.Target = (*Client)(nil)

// New builds a client for apiURL. The library appends /api/v4/ only when it
// is missing, so the URL from forge.Address can be passed unchanged.
func New(httpClient *http.Client, apiURL string, token config.Secret) (*Client, error) {
	if token.Reveal() == "" {
		return nil, errors.New(errTokenEmpty)
	}

	api, err := gl.NewAuthSourceClient(
		bearerAuth{token: token},
		gl.WithBaseURL(apiURL),
		gl.WithHTTPClient(httpClient),
		gl.WithCustomRetryMax(retryMax),
	)
	if err != nil {
		return nil, fmt.Errorf("gitlab connect: %w", err)
	}

	return &Client{api: api, transfer: httpclient.NewTransfer(), token: token}, nil
}

// wrap adds the operation as context and maps 404 to forge.ErrNotFound. The
// library error carries method, URL, status and API message but never the
// request, so the Authorization header cannot leak.
func wrap(op string, resp *gl.Response, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, gl.ErrNotFound) ||
		(resp != nil && resp.Response != nil && resp.StatusCode == http.StatusNotFound) {
		return fmt.Errorf("gitlab %s: %w: %s", op, forge.ErrNotFound, err.Error())
	}

	if resp != nil {
		err = retry.HTTP(err, resp.Response, time.Now())
	}
	return fmt.Errorf("gitlab %s: %w", op, err)
}

func opName(verb string, a forge.Address) string {
	return fmt.Sprintf("%s %s/%s", verb, a.Namespace, a.Name)
}

func projectID(a forge.Address) string {
	return a.Namespace + "/" + a.Name
}

// Capabilities reports that GitLab has no website field but can disable merge
// requests.
func (c *Client) Capabilities() forge.Capabilities {
	return forge.Capabilities{Website: false, PullRequests: true}
}

// enabled reports whether a feature is usable; "private" means members only,
// which is still on.
func enabled(l gl.AccessControlValue) bool {
	return l == gl.EnabledAccessControl || l == gl.PrivateAccessControl
}

func accessLevel(on bool) *gl.AccessControlValue {
	if on {
		return new(gl.EnabledAccessControl)
	}

	return new(gl.DisabledAccessControl)
}

func visibility(private bool) *gl.VisibilityValue {
	if private {
		return new(gl.PrivateVisibility)
	}

	return new(gl.PublicVisibility)
}

// Get implements forge.Target.
func (c *Client) Get(ctx context.Context, a forge.Address) (forge.Repo, error) {
	p, resp, err := c.api.Projects.GetProject(projectID(a), nil, gl.WithContext(ctx))
	if err != nil {
		return forge.Repo{}, wrap(opName("get project", a), resp, err)
	}

	return forge.Repo{
		Description:   p.Description,
		Topics:        p.Topics,
		DefaultBranch: p.DefaultBranch,
		// Internal projects are visible to every user of the instance, so
		// only "private" counts as private: otherwise a private source would
		// never turn an internal target private.
		Private:         p.Visibility == gl.PrivateVisibility,
		HasIssues:       enabled(p.IssuesAccessLevel),
		HasWiki:         enabled(p.WikiAccessLevel),
		HasPullRequests: enabled(p.MergeRequestsAccessLevel),
	}, nil
}

// Create implements forge.Target. DefaultBranch stays unset on purpose: the
// branch does not exist before the first push.
func (c *Client) Create(ctx context.Context, a forge.Address, r forge.Repo) error {
	op := opName("create project", a)

	nsID, err := c.namespaceID(ctx, op, a.Namespace)
	if err != nil {
		return err
	}

	_, resp, err := c.api.Projects.CreateProject(&gl.CreateProjectOptions{
		NamespaceID:              &nsID,
		Name:                     &a.Name,
		Path:                     &a.Name,
		Description:              &r.Description,
		Topics:                   &r.Topics,
		Visibility:               visibility(r.Private),
		IssuesAccessLevel:        accessLevel(r.HasIssues),
		WikiAccessLevel:          accessLevel(r.HasWiki),
		MergeRequestsAccessLevel: accessLevel(r.HasPullRequests),
	}, gl.WithContext(ctx))

	return wrap(op, resp, err)
}

// namespaceID resolves the namespace path and refuses foreign personal
// namespaces: creating there needs admin rights and is never intended.
func (c *Client) namespaceID(ctx context.Context, op, path string) (int64, error) {
	ns, resp, err := c.api.Namespaces.GetNamespace(path, gl.WithContext(ctx))
	if err != nil {
		return 0, wrap(op, resp, err)
	}

	if ns.Kind == groupKind {
		return ns.ID, nil
	}

	self, resp, err := c.api.Users.CurrentUser(gl.WithContext(ctx))
	if err != nil {
		return 0, wrap(op, resp, err)
	}

	if ns.Kind != userKind || !strings.EqualFold(self.Username, ns.Path) {
		return 0, fmt.Errorf("gitlab %s: %w", op, &foreignNamespaceError{path})
	}

	return ns.ID, nil
}

// Update implements forge.Target, sending only the fields that are set.
// Website is ignored: GitLab has no such field.
func (c *Client) Update(ctx context.Context, a forge.Address, s forge.Settings) error {
	opts := gl.EditProjectOptions{
		Description:   s.Description,
		DefaultBranch: s.DefaultBranch,
		Topics:        s.Topics,
	}

	set := s.Description != nil || s.DefaultBranch != nil || s.Topics != nil

	if s.Private != nil {
		opts.Visibility, set = visibility(*s.Private), true
	}

	if s.HasIssues != nil {
		opts.IssuesAccessLevel, set = accessLevel(*s.HasIssues), true
	}

	if s.HasWiki != nil {
		opts.WikiAccessLevel, set = accessLevel(*s.HasWiki), true
	}

	if s.HasPullRequests != nil {
		opts.MergeRequestsAccessLevel, set = accessLevel(*s.HasPullRequests), true
	}

	if !set {
		return nil
	}

	_, resp, err := c.api.Projects.EditProject(projectID(a), &opts, gl.WithContext(ctx))

	return wrap(opName("update project", a), resp, err)
}

// Labels implements forge.Target.
func (c *Client) Labels(ctx context.Context, a forge.Address) ([]forge.Label, error) {
	var labels []forge.Label

	opts := &gl.ListLabelsOptions{}
	opts.PerPage = labelPageSize
	// GitLab includes labels of ancestor groups by default; those are not the
	// project's to create, update or delete, so asking for them would make
	// every prune run fail on them.
	opts.IncludeAncestorGroups = new(false)

	for {
		page, resp, err := c.api.Labels.ListLabels(projectID(a), opts, gl.WithContext(ctx))
		if err != nil {
			return nil, wrap(opName("list labels of", a), resp, err)
		}

		for _, l := range page {
			labels = append(labels, forge.Label{
				Name:        l.Name,
				Color:       forge.NormalizeColor(l.Color),
				Description: l.Description,
			})
		}

		if resp.NextPage == 0 {
			return labels, nil
		}

		opts.Page = resp.NextPage
	}
}

// hashColor adds the "#" GitLab requires on write; forge.Label keeps it off.
func hashColor(c string) *string {
	return new("#" + forge.NormalizeColor(c))
}

// CreateLabel implements forge.Target.
func (c *Client) CreateLabel(ctx context.Context, a forge.Address, l forge.Label) error {
	_, resp, err := c.api.Labels.CreateLabel(projectID(a), &gl.CreateLabelOptions{
		Name: &l.Name, Color: hashColor(l.Color), Description: &l.Description,
	}, gl.WithContext(ctx))

	return wrap(opName("create label in", a), resp, err)
}

// UpdateLabel implements forge.Target. The library path-escapes the label
// name, so a slash cannot leave its path segment.
func (c *Client) UpdateLabel(ctx context.Context, a forge.Address, l forge.Label) error {
	_, resp, err := c.api.Labels.UpdateLabel(projectID(a), l.Name, &gl.UpdateLabelOptions{
		Color: hashColor(l.Color), Description: &l.Description,
	}, gl.WithContext(ctx))

	return wrap(opName("update label in", a), resp, err)
}

// DeleteLabel implements forge.Target.
func (c *Client) DeleteLabel(ctx context.Context, a forge.Address, name string) error {
	resp, err := c.api.Labels.DeleteLabel(projectID(a), name, nil, gl.WithContext(ctx))

	return wrap(opName("delete label in", a), resp, err)
}

// Delete implements forge.Target.
func (c *Client) Delete(ctx context.Context, a forge.Address) error {
	resp, err := c.api.Projects.DeleteProject(projectID(a), nil, gl.WithContext(ctx))

	return wrap(opName("delete project", a), resp, err)
}
