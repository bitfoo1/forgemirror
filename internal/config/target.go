package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/template"

	"go.bitfoo.de/forgemirror/internal/repo"
)

// errTargetNotValidated guards against using a Target that did not go
// through [Config.Validate], which compiles the URL template.
var errTargetNotValidated = errors.New("target URL template not compiled; validate the config first")

// Target is a forge that repositories are mirrored to.
type Target struct {
	// Name identifies the target in logs and metrics.
	Name string `koanf:"name"`
	// URL is a text/template for the push URL, rendered with a
	// [repo.Repository], e.g. "https://github.com/acme/{{ .Name }}.git".
	URL string `koanf:"url"`
	// Username is sent together with Token; most forges accept "oauth2".
	Username  string `koanf:"username"`
	Token     Secret `koanf:"token"`
	TokenFile string `koanf:"token_file"`

	urlTemplate *template.Template
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
	if u.Scheme != "https" && u.Scheme != "http" {
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
