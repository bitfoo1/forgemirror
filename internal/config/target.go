package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"text/template"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// errTargetNotValidated guards against using a Target that did not go
// through [Config.Validate], which compiles the URL template.
var errTargetNotValidated = errors.New("target URL template not compiled; validate the config first")

// Target is a forge that repositories are mirrored to.
type Target struct {
	FirstPublicationApproval *bool `koanf:"first_publication_approval"`
	// Name identifies the target in logs and metrics.
	Name string `koanf:"name"`
	// URL is a text/template for the push URL, rendered with a
	// [repo.Repository], e.g. "https://github.com/acme/{{ .Name }}.git".
	URL string `koanf:"url"`
	// Username is sent together with Token; most forges accept "oauth2".
	Username  string `koanf:"username"`
	Token     Secret `koanf:"token"`
	TokenFile string `koanf:"token_file"`

	// Type selects the forge API (github, gitlab, forgejo); empty means the
	// target is only a git push destination, as before.
	Type string `koanf:"type"`
	// APIURL overrides the API base derived from Type and the URL host.
	APIURL string `koanf:"api_url"`
	// Create makes a missing target repository.
	Create bool `koanf:"create"`
	// Delete removes the target repository when the source was deleted.
	Delete bool `koanf:"delete"`
	// PruneLabels deletes labels that do not exist on the source.
	PruneLabels bool `koanf:"prune_labels"`
	// PruneReleases deletes releases whose tag has no published release on
	// the source.
	PruneReleases bool `koanf:"prune_releases"`
	// Registry is the container registry of the target forge, as host[:port]
	// or http(s)://host[:port]; empty derives it from Type and the URL host.
	Registry string `koanf:"registry"`
	// Settings are target-wide defaults that beat the source values.
	Settings forge.Settings `koanf:"settings"`
	// Overrides refine Settings for matching repositories, in order.
	Overrides []Override `koanf:"overrides"`

	urlTemplate *template.Template
}

// Override applies Settings to the repositories matched by Repos.
type Override struct {
	FirstPublicationApproval *bool `koanf:"first_publication_approval"`
	// Repos is matched against the whole "owner/name", like the filter rules.
	Repos    string         `koanf:"repos"`
	Settings forge.Settings `koanf:"settings"`

	re *regexp.Regexp
}

// RequiresFirstPublicationApproval applies policy overrides in the same order as settings.
func (t *Target) RequiresFirstPublicationApproval(repo string, fallback bool) bool {
	required := fallback
	if t.FirstPublicationApproval != nil {
		required = *t.FirstPublicationApproval
	}
	for _, o := range t.Overrides {
		if o.re != nil && o.re.MatchString(repo) && o.FirstPublicationApproval != nil {
			required = *o.FirstPublicationApproval
		}
	}
	return required
}

// Address derives the API address of r on this target from the rendered URL.
func (t *Target) Address(r repo.Repository) (forge.Address, error) {
	raw, err := t.RemoteURL(r)
	if err != nil {
		return forge.Address{}, err
	}

	addr, err := forge.ParseAddress(t.Type, t.APIURL, raw)
	if err != nil {
		return forge.Address{}, fmt.Errorf("target %q: %w", t.Name, err)
	}

	return addr, nil
}

// RegistryHost returns the container registry host of the target and
// whether it is reached over plain HTTP.
func (t *Target) RegistryHost() (string, bool, error) {
	if t.Registry != "" {
		return parseRegistry(t.Registry)
	}

	// The host is constant across repositories (see [Target.Address]), so a
	// sample rendering is enough.
	raw, err := t.RemoteURL(repo.Repository{Owner: "owner", Name: "name", FullName: "owner/name"})
	if err != nil {
		return "", false, err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false, errors.New("url: cannot derive the registry host")
	}

	switch {
	case t.Type == forge.TypeGitHub && strings.EqualFold(u.Host, "github.com"):
		return "ghcr.io", false, nil
	case t.Type == forge.TypeGitLab && strings.EqualFold(u.Host, "gitlab.com"):
		return "registry.gitlab.com", false, nil
	case t.Type == forge.TypeForgejo:
		// Forgejo serves its registry on the instance host itself.
		return u.Host, u.Scheme == schemeHTTP, nil
	default:
		return "", false, fmt.Errorf("cannot derive the container registry of %s; set registry", u.Host)
	}
}

// parseRegistry accepts host[:port] (HTTPS) or an http(s) URL without a path.
func parseRegistry(raw string) (string, bool, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return "", false, errors.New("not a valid host or URL")
	case u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP:
		return "", false, fmt.Errorf("scheme must be https or http, got %q", u.Scheme)
	case u.Host == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "":
		return "", false, errors.New("must be host[:port] or a scheme and host[:port] only")
	}

	return u.Host, u.Scheme == schemeHTTP, nil
}

// syncsPackages reports whether package sync is explicitly enabled in the
// target settings or any override. The default (on) is deliberately not
// counted: a GitHub Enterprise or self-hosted GitLab target without a
// registry must still start; its packages are skipped with a plan warning.
func (t *Target) syncsPackages() bool {
	if t.Settings.Packages != nil && *t.Settings.Packages {
		return true
	}

	for _, o := range t.Overrides {
		if o.Settings.Packages != nil && *o.Settings.Packages {
			return true
		}
	}

	return false
}

// Layers returns the settings layers for the repository fullName in
// precedence order, lowest first: the target defaults, then every matching
// override.
func (t *Target) Layers(fullName string) []forge.Settings {
	layers := []forge.Settings{t.Settings}
	for _, o := range t.Overrides {
		if o.re != nil && o.re.MatchString(fullName) {
			layers = append(layers, o.Settings)
		}
	}

	return layers
}

// RemoteURL renders the push URL for r, without credentials.
func (t *Target) RemoteURL(r repo.Repository) (string, error) {
	if t.urlTemplate == nil {
		return "", errTargetNotValidated
	}

	var b strings.Builder
	if err := t.urlTemplate.Execute(&b, r); err != nil {
		return "", fmt.Errorf("rendering URL of target %q: %w", t.Name, err)
	}

	return b.String(), nil
}

// compile parses the URL template and checks that it renders a usable URL.
func (t *Target) compile() error {
	tmpl, err := template.New(t.Name).Option("missingkey=error").Parse(t.URL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}

	var b strings.Builder
	sample := repo.Repository{Owner: "owner", Name: "name", FullName: "owner/name"}
	if execErr := tmpl.Execute(&b, sample); execErr != nil {
		return fmt.Errorf("url: %w", execErr)
	}
	if urlErr := checkRemoteURL(b.String()); urlErr != nil {
		return fmt.Errorf("url: %w", urlErr)
	}

	t.urlTemplate = tmpl

	return nil
}

func checkRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP {
		return fmt.Errorf("scheme must be https or http, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	// Credentials embedded in the URL would end up in logs and process lists;
	// they belong in token / token_file.
	if u.User != nil {
		return errors.New("must not contain credentials; use token or token_file")
	}

	return nil
}

// compileOverrides validates the overrides and compiles their patterns. The
// anchoring matches the filter rules so both read the same way.
func (t *Target) compileOverrides() []error {
	var errs []error
	for j := range t.Overrides {
		o := &t.Overrides[j]
		prefix := fmt.Sprintf("overrides[%d]", j)

		if o.Repos == "" {
			errs = append(errs, fmt.Errorf("%s.repos: %w", prefix, errRequired))
		} else if re, err := regexp.Compile(`^(?:` + o.Repos + `)$`); err != nil {
			errs = append(errs, fmt.Errorf("%s.repos: %w", prefix, err))
		} else {
			o.re = re
		}
		if err := o.Settings.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s.settings: %w", prefix, err))
		}
	}

	return errs
}
