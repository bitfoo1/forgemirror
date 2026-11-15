package forge

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Address locates a repository on a forge.
type Address struct {
	APIURL    string
	Namespace string
	Name      string
}

// ParseAddress derives the API address from the rendered clone URL. The
// scheme and host (including port) of the clone URL are reused for the
// default API URL so self-hosted instances on http or odd ports work.
func ParseAddress(forgeType, apiURL, renderedURL string) (Address, error) {
	u, err := url.Parse(renderedURL)
	if err != nil {
		return Address{}, fmt.Errorf("parse repository url: %w", err)
	}

	segments := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(segments) < 2 || slices.Contains(segments, "") {
		return Address{}, fmt.Errorf("repository url %q needs a namespace and a name", u.Redacted())
	}

	api := strings.TrimRight(apiURL, "/")
	if api == "" {
		api, err = defaultAPIURL(forgeType, u)
		if err != nil {
			return Address{}, err
		}
	}

	return Address{
		APIURL:    api,
		Namespace: strings.Join(segments[:len(segments)-1], "/"),
		Name:      segments[len(segments)-1],
	}, nil
}

func defaultAPIURL(forgeType string, u *url.URL) (string, error) {
	base := u.Scheme + "://" + u.Host

	switch forgeType {
	case TypeGitHub:
		if u.Host == "github.com" {
			return "https://api.github.com", nil
		}

		return base + "/api/v3", nil
	case TypeGitLab:
		return base + "/api/v4", nil
	case TypeForgejo:
		return base + "/api/v1", nil
	default:
		return "", fmt.Errorf("unknown forge type %q", forgeType)
	}
}
