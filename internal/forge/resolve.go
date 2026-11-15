package forge

import (
	"slices"
	"strings"
)

// Resolve applies the layers in order (last wins) on top of the source
// repository. syncLabels defaults to true.
func Resolve(src Repo, layers ...Settings) (Repo, bool) {
	out := src
	out.Topics = normalizeTopics(src.Topics)
	syncLabels := true

	for _, l := range layers {
		if l.Description != nil {
			out.Description = *l.Description
		}
		if l.Website != nil {
			out.Website = *l.Website
		}
		if l.Topics != nil {
			out.Topics = normalizeTopics(*l.Topics)
		}
		if l.DefaultBranch != nil {
			out.DefaultBranch = *l.DefaultBranch
		}
		// A layer can only make a repository private, never public: a private
		// source must not leak through configuration.
		if l.Private != nil && *l.Private {
			out.Private = true
		}
		if l.HasIssues != nil {
			out.HasIssues = *l.HasIssues
		}
		if l.HasWiki != nil {
			out.HasWiki = *l.HasWiki
		}
		if l.HasPullRequests != nil {
			out.HasPullRequests = *l.HasPullRequests
		}
		if l.Labels != nil {
			syncLabels = *l.Labels
		}
	}

	return out, syncLabels
}

// Diff returns the settings that must change to turn current into desired.
// Fields the target cannot store are ignored, otherwise every sync would
// report a difference that can never be resolved.
func Diff(current, desired Repo, caps Capabilities) Settings {
	var s Settings

	if current.Description != desired.Description {
		s.Description = &desired.Description
	}
	if caps.Website && current.Website != desired.Website {
		s.Website = &desired.Website
	}
	if !slices.Equal(normalizeTopics(current.Topics), normalizeTopics(desired.Topics)) {
		topics := normalizeTopics(desired.Topics)
		s.Topics = &topics
	}
	if current.DefaultBranch != desired.DefaultBranch {
		s.DefaultBranch = &desired.DefaultBranch
	}
	if current.Private != desired.Private {
		s.Private = &desired.Private
	}
	if current.HasIssues != desired.HasIssues {
		s.HasIssues = &desired.HasIssues
	}
	if current.HasWiki != desired.HasWiki {
		s.HasWiki = &desired.HasWiki
	}
	if caps.PullRequests && current.HasPullRequests != desired.HasPullRequests {
		s.HasPullRequests = &desired.HasPullRequests
	}

	return s
}

// LabelChanges computes the label operations that turn current into desired.
// Removal is opt-in so labels added by hand on the target survive by default.
func LabelChanges(current, desired []Label, prune bool) ([]Label, []Label, []Label) {
	var create, update, remove []Label

	have := make(map[string]Label, len(current))
	for _, l := range current {
		have[strings.ToLower(l.Name)] = l
	}

	want := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		key := strings.ToLower(d.Name)
		want[key] = struct{}{}

		c, ok := have[key]
		switch {
		case !ok:
			create = append(create, d)
		case NormalizeColor(c.Color) != NormalizeColor(d.Color) || c.Description != d.Description:
			update = append(update, d)
		}
	}

	if prune {
		for _, c := range current {
			if _, ok := want[strings.ToLower(c.Name)]; !ok {
				remove = append(remove, c)
			}
		}
	}

	return create, update, remove
}

func normalizeTopics(in []string) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		out = append(out, strings.ToLower(t))
	}

	slices.Sort(out)

	return slices.Compact(out)
}
