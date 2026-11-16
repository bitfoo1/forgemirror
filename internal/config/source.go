package config

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

var errCloneURLOutsideBase = errors.New("clone URL is outside source.base_url")

// CheckCloneURL reports whether raw, the clone URL of a webhook payload, lies
// below BaseURL. The source token is sent to whatever host the clone URL
// names, so without this check anyone holding the webhook secret could
// redirect the token to a host of their choice. An empty BaseURL accepts
// every URL, because existing configurations predate the setting.
func (s Source) CheckCloneURL(raw string) error {
	if s.BaseURL == "" {
		return nil
	}

	base, err := url.Parse(s.BaseURL)
	if err != nil {
		return fmt.Errorf("source.base_url: %w", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("clone URL: %w", err)
	}

	// The path is cleaned so that "/base/../other" cannot pass as being
	// below "/base/".
	if !strings.EqualFold(u.Scheme, base.Scheme) ||
		!strings.EqualFold(u.Host, base.Host) ||
		u.User != nil ||
		!strings.HasPrefix(path.Clean("/"+u.Path)+"/", basePath(base)) {
		return errCloneURLOutsideBase
	}

	return nil
}

// RegistryHost returns the host of the Forgejo container registry (the host
// of BaseURL) and whether it is reached over plain HTTP.
func (s Source) RegistryHost() (string, bool, error) {
	u, err := url.Parse(s.BaseURL)
	if err != nil || u.Host == "" {
		return "", false, errors.New("source.base_url: required for package sync")
	}

	return u.Host, u.Scheme == schemeHTTP, nil
}

func (s Source) validate() error {
	if s.BaseURL == "" {
		return nil
	}
	if err := checkRemoteURL(s.BaseURL); err != nil {
		return err
	}

	u, err := url.Parse(s.BaseURL)
	if err != nil {
		return err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must not contain a query or fragment")
	}

	return nil
}

// basePath returns the cleaned base path with a trailing slash, so that
// "https://git.example/forgejo" does not also admit "/forgejo-evil/...".
func basePath(base *url.URL) string {
	p := path.Clean("/" + base.Path)
	if p == "/" {
		return p
	}

	return p + "/"
}
