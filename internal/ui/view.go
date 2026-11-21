package ui

import (
	"cmp"
	"slices"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/publication"
	"go.bitfoo.de/forgemirror/internal/status"
)

// Repository states, in display order: what needs attention comes first.
// Running is shown in addition, not as a state, so rows do not jump around
// while runs come and go.
const (
	stateFailed    = "failed"
	stateOutOfSync = "out-of-sync"
	stateUnchecked = "unchecked"
	stateSynced    = "synced"
	stateInSync    = "in-sync"
)

func stateRank(s string) int {
	return slices.Index([]string{stateFailed, stateOutOfSync, stateUnchecked, stateSynced, stateInSync}, s)
}

// repoView is one repository row.
type repoView struct {
	status.Repo

	State           string
	Activity        *activity
	Queued          bool
	Targets         []targetView
	AppliedTargets  []targetView
	Actionable      bool
	HeldTargets     int
	ChangeGroups    []changeGroup
	DistinctChanges int
}

// targetView merges a target's part of the newest plan with its last
// results.
type targetView struct {
	Badge       forgeBadge
	Badges      []forgeBadge
	Publication *publication.View
	Repo        string
	Actionable  bool
	Pause       *status.Pause
	Name        string
	// Plan is nil when the newest plan does not mention the target (e.g. a
	// deletion that does not touch it).
	Plan *plan.Target
	// Last holds the last push and forge operation results; nil before the
	// first.
	Last *status.Target
}

func repoViews(repos []status.Repo, queued map[string]bool) ([]repoView, counts) {
	var c counts
	views := make([]repoView, 0, len(repos))
	for _, r := range repos {
		v := repoView{Repo: r, Queued: queued[r.Name], State: repoState(r)}
		current := r.Current
		if current == nil && r.LastAttempt == nil {
			current = r.Plan
		}
		v.Targets = targetViews(status.Repo{Plan: current, Targets: r.Targets})
		if r.LastAttempt != nil {
			v.AppliedTargets = targetViews(status.Repo{Plan: &r.LastAttempt.Plan, Targets: r.LastAttempt.Targets})
		}

		switch v.State {
		case stateFailed:
			c.Failed++
		case stateOutOfSync:
			c.OutOfSync++
		}
		if pending(r) {
			c.Pending++
		}
		views = append(views, v)
	}

	slices.SortStableFunc(views, func(a, b repoView) int {
		return cmp.Or(cmp.Compare(stateRank(a.State), stateRank(b.State)), cmp.Compare(a.Name, b.Name))
	})

	return views, c
}

// Pending reports the repositories whose newest plan was only computed and
// has changes: exactly what "Sync all" applies.
func Pending(s status.Snapshot) []string {
	var names []string
	for _, r := range s.Repos {
		if pending(r) {
			names = append(names, r.Name)
		}
	}

	return names
}

func pending(r status.Repo) bool {
	return !r.Stale && r.Plan != nil && !r.Plan.Apply && r.Plan.Changes() > 0
}

// repoState derives the state from the newest plan. An applied plan is in
// sync unless a result recorded since it failed.
func repoState(r status.Repo) string {
	p := r.Plan
	if r.Stale {
		if p != nil && p.Failed() {
			return stateFailed
		}
		if r.LastAttempt != nil {
			switch r.LastAttempt.Status {
			case "failed", "partially failed", "interrupted":
				return stateFailed
			}
		}
		return stateUnchecked
	}
	switch {
	case p == nil:
		return stateUnchecked
	case p.Failed():
		return stateFailed
	case !p.Apply && p.Changes() > 0:
		return stateOutOfSync
	case !p.Apply:
		return stateInSync
	case failedSince(r.Targets, p):
		return stateFailed
	case p.Changes() > 0:
		return stateSynced
	default:
		return stateInSync
	}
}

// failedSince reports a push or forge operation that failed after the plan
// was computed, i.e. while it was applied. Older results do not count: an
// operation keeps its last result until it runs again.
func failedSince(targets []status.Target, p *plan.Repo) bool {
	for _, t := range targets {
		if t.Error != "" && !t.LastRun.Before(p.At) {
			return true
		}
		for _, op := range t.Operations {
			if op.Error != "" && !op.At.Before(p.At) {
				return true
			}
		}
	}

	return false
}

func targetViews(r status.Repo) []targetView {
	var views []targetView
	index := make(map[string]int)
	if r.Plan != nil {
		for i := range r.Plan.Targets {
			t := &r.Plan.Targets[i]
			index[t.Name] = len(views)
			views = append(views, targetView{Name: t.Name, Plan: t})
		}
	}
	for i := range r.Targets {
		t := &r.Targets[i]
		if j, ok := index[t.Name]; ok {
			views[j].Last = t

			continue
		}
		views = append(views, targetView{Name: t.Name, Last: t})
	}

	return views
}

// targetType is one kind of target the configuration accepts. Every kind is
// listed, used or not, so the overview shows what is possible as well as
// what is configured.
type targetType struct {
	Label string
	Icon  string
	// Names are the configured targets of this kind, in configuration order.
	Names []string
}

// Codeberg uses the Forgejo API, but has its own identity in the overview.
func targetTypes(targets []config.Target) []targetType {
	types := []targetType{
		{Label: labelGitHub, Icon: forge.TypeGitHub},
		{Label: labelGitLab, Icon: forge.TypeGitLab},
		{Label: labelCodeberg, Icon: brandCodeberg},
		{Label: labelForgejo},
		{Label: "Git"},
	}
	for _, target := range targets {
		brand := targetBrand(target)
		for i := range types {
			if types[i].Label == brand.Label {
				types[i].Names = append(types[i].Names, target.Name)
			}
		}
	}
	return types
}
