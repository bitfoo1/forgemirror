// Package forge is the forge-neutral model used to create target repositories
// and keep their settings in line with the source repository.
package forge

import (
	"context"
	"errors"
	"strings"

	"go.bitfoo.de/forgemirror/internal/repo"
)

// Supported forge types.
const (
	TypeGitHub  = "github"
	TypeGitLab  = "gitlab"
	TypeForgejo = "forgejo"
)

// ErrNotFound is wrapped by Target.Get when the repository does not exist, so
// callers can tell "create it" apart from a real failure.
var ErrNotFound = errors.New("not found")

// Label is a repository label.
type Label struct {
	Name string `json:"name"`
	// Color is canonical: lower-case hex without "#". Forges disagree on the
	// "#" and on case, so comparing raw values would cause endless updates.
	Color       string `json:"color"`
	Description string `json:"description"`
}

// NormalizeColor returns the canonical form of a label color.
func NormalizeColor(c string) string {
	return strings.ToLower(strings.TrimPrefix(c, "#"))
}

// Repo is the set of repository properties forgemirror synchronises.
type Repo struct {
	Description     string
	Website         string
	Topics          []string
	DefaultBranch   string
	Private         bool
	HasIssues       bool
	HasWiki         bool
	HasPullRequests bool
}

// Settings are optional overrides. A nil field means "leave untouched". The
// koanf tags let the config package embed it unchanged.
type Settings struct {
	Description     *string   `koanf:"description"`
	Website         *string   `koanf:"website"`
	Topics          *[]string `koanf:"topics"`
	DefaultBranch   *string   `koanf:"default_branch"`
	Private         *bool     `koanf:"private"`
	HasIssues       *bool     `koanf:"has_issues"`
	HasWiki         *bool     `koanf:"has_wiki"`
	HasPullRequests *bool     `koanf:"has_pull_requests"`
	Labels          *bool     `koanf:"labels"`
	Releases        *bool     `koanf:"releases"`
	Packages        *bool     `koanf:"packages"`
	// MirrorNote and MirrorTopic mark the target as a mirror (see [Marker]);
	// nil keeps the default, "" disables.
	MirrorNote             *string `koanf:"mirror_note"`
	MirrorTopic            *string `koanf:"mirror_topic"`
	MirrorURL              *string `koanf:"mirror_url"`
	MirrorDescriptionLimit *int    `koanf:"mirror_description_limit"`
}

// Validate rejects settings that could expose a private source repository
// and mirror notes that cannot be rendered.
func (s Settings) Validate() error {
	if s.Private != nil && !*s.Private {
		return errors.New("private: only true is allowed")
	}
	if s.MirrorNote != nil {
		return errors.Join(validateMirrorNote(*s.MirrorNote), validateMirrorIdentity(s))
	}

	return validateMirrorIdentity(s)
}

// IsZero reports whether no field is set.
func (s Settings) IsZero() bool {
	return s == Settings{}
}

// Capabilities describe what a target forge can store.
type Capabilities struct {
	Website      bool
	PullRequests bool
	// ValidTopic reports whether the forge accepts a topic; nil accepts all.
	// Topics it rejects are dropped instead of failing every settings sync.
	ValidTopic func(topic string) bool
}

// RepresentableTopics splits topics into those caps can store and those it
// cannot.
func RepresentableTopics(topics []string, caps Capabilities) ([]string, []string) {
	if caps.ValidTopic == nil {
		return topics, nil
	}

	var kept, dropped []string
	for _, t := range topics {
		if caps.ValidTopic(t) {
			kept = append(kept, t)
		} else {
			dropped = append(dropped, t)
		}
	}

	return kept, dropped
}

// Source reads repositories and labels from the source forge.
type Source interface {
	Repo(ctx context.Context, owner, name string) (Repo, error)
	Labels(ctx context.Context, owner, name string) ([]Label, error)
	Repos(ctx context.Context) ([]repo.Repository, error)
}

// Target manages repositories on a target forge.
type Target interface {
	// Get returns an error wrapping ErrNotFound for a missing repository.
	Get(ctx context.Context, a Address) (Repo, error)
	// Create must not set a default branch: the branch does not exist before
	// the first push, and some forges reject a nonexistent one.
	Create(ctx context.Context, a Address, r Repo) error
	Update(ctx context.Context, a Address, s Settings) error
	Labels(ctx context.Context, a Address) ([]Label, error)
	CreateLabel(ctx context.Context, a Address, l Label) error
	UpdateLabel(ctx context.Context, a Address, l Label) error
	DeleteLabel(ctx context.Context, a Address, name string) error
	Delete(ctx context.Context, a Address) error
	Capabilities() Capabilities
}
