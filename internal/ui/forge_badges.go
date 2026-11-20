package ui

import (
	"slices"

	"go.bitfoo.de/forgemirror/internal/status"
)

type forgeBadge struct {
	Name, Class, Status string
}

func targetBadge(repository *repoView, target *targetView, removed bool) forgeBadge {
	badge := forgeBadge{Name: target.Name, Class: "muted", Status: "not checked"}
	switch {
	case targetHasError(repository, target):
		badge.Class, badge.Status = "fail", "error"
	case removed:
		badge.Status = "no longer configured"
	case !repository.Actionable:
		badge.Status = "awaiting source admission"
	case repository.Stale || repository.Applying:
		badge.Status = "awaiting verification"
	case repository.Running:
		badge.Status = activityRefreshing
	case target.Plan == nil:
	case target.Plan.RefsUnknown:
		badge.Status = "not verified"
	case target.Plan.Changes() > 0:
		badge.Class, badge.Status = "warn", "out of sync"
	default:
		badge.Class, badge.Status = "ok", "in sync"
	}
	if target.Publication != nil && target.Publication.Held {
		badge.Status += " · " + target.Publication.Headline
	}
	return badge
}

func targetHasError(repository *repoView, target *targetView) bool {
	if repository.Stale && repository.LastAttempt != nil && repository.LastAttempt.Plan.Error != "" {
		return true
	}
	if repository.Current != nil && repository.Current.Error != "" {
		return true
	}
	if target.Plan != nil && target.Plan.Error != "" {
		return true
	}
	return target.Last != nil &&
		(target.Last.Error != "" || slices.ContainsFunc(target.Last.Operations, func(op status.Operation) bool { return op.Error != "" }))
}
