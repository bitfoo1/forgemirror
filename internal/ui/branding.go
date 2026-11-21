package ui

import (
	"net/url"
	"strings"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
)

const (
	brandCodeberg = "codeberg"
	labelGitHub   = "GitHub"
	labelGitLab   = "GitLab"
	labelCodeberg = "Codeberg"
	labelForgejo  = "Forgejo"
)

type forgeIdentity struct {
	Label, Icon string
}

func targetBrand(target config.Target) forgeIdentity {
	raw, err := target.RemoteURL(repo.Repository{Owner: "owner", Name: "name", FullName: "owner/name"})
	host := ""
	if err == nil {
		if parsed, parseErr := url.Parse(raw); parseErr == nil {
			host = strings.ToLower(parsed.Hostname())
		}
	}
	switch {
	case host == "codeberg.org" && (target.Type == "" || target.Type == forge.TypeForgejo):
		return forgeIdentity{Label: labelCodeberg, Icon: brandCodeberg}
	case target.Type == forge.TypeGitHub || (target.Type == "" && host == "github.com"):
		return forgeIdentity{Label: labelGitHub, Icon: forge.TypeGitHub}
	case target.Type == forge.TypeGitLab || (target.Type == "" && host == "gitlab.com"):
		return forgeIdentity{Label: labelGitLab, Icon: forge.TypeGitLab}
	case target.Type == forge.TypeForgejo:
		return forgeIdentity{Label: labelForgejo}
	default:
		return forgeIdentity{Label: "Git"}
	}
}

// Brand names are presentation only: aliases still identify actions and saved
// results. In particular, arbitrary Forgejo hosts must not be called Codeberg.
func (h *handler) forgeIdentity(name string) forgeIdentity {
	for _, target := range h.opts.Config.Targets {
		if target.Name != name {
			continue
		}
		brand := targetBrand(target)
		if !strings.EqualFold(name, brand.Label) {
			brand.Label = name
		}
		return brand
	}
	// Retained history may outlive configuration. Only conventional brand names
	// have a recognizable identity without a current host or explicit API type.
	for _, brand := range []forgeIdentity{{Label: labelGitHub, Icon: forge.TypeGitHub}, {Label: labelGitLab, Icon: forge.TypeGitLab}, {Label: labelCodeberg, Icon: brandCodeberg}} {
		if strings.EqualFold(name, brand.Label) {
			return brand
		}
	}
	return forgeIdentity{Label: name}
}
