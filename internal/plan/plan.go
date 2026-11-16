// Package plan describes what one run would change on the targets of a
// repository. The dispatcher computes it, the status store keeps the latest
// one for the UI, and the change log is written from the same values, so the
// diff an operator reviews is what a sync does.
package plan

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.bitfoo.de/forgemirror/internal/forge"
)

// Kinds of ref and label changes.
const (
	KindCreate = "create"
	KindUpdate = "update"
	KindDelete = "delete"
)

// Repo is the plan of one source repository.
type Repo struct {
	// Name is the full name, "owner/name".
	Name string
	// At is when the plan was computed.
	At time.Time
	// Apply reports whether the run applied the plan or only computed it.
	Apply bool
	// SourceDeleted is set when the repository no longer exists on the
	// source; the plan then only holds deletions.
	SourceDeleted bool
	// Error is set when no plan could be computed for any target, e.g. the
	// source API failed.
	Error string
	// Targets is in configuration order.
	Targets []Target
}

// Changes counts the changes over all targets.
func (r Repo) Changes() int {
	n := 0
	for _, t := range r.Targets {
		n += t.Changes()
	}

	return n
}

// Failed reports whether the plan, or the plan of any target, failed.
func (r Repo) Failed() bool {
	return r.Error != "" || slices.ContainsFunc(r.Targets, func(t Target) bool { return t.Error != "" })
}

// Target is the plan for one target.
type Target struct {
	Name string
	// Create means the target repository is missing and will be created.
	Create bool
	// Privatise means the public target repository is made private before
	// the push.
	Privatise bool
	// Delete means the target repository will be deleted.
	Delete bool
	// RefsUnknown means the target's refs could not be listed; the sync then
	// pushes everything, as git decides what actually changes.
	RefsUnknown bool
	Refs        []RefChange
	// RefsOmitted counts ref changes left out of Refs to bound memory; they
	// still count as changes.
	RefsOmitted int
	Settings    []FieldChange
	Labels      []LabelChange
	// Warnings are notes that do not block the sync, e.g. topics the target
	// forge cannot store.
	Warnings []string
	// Error is set when the plan of this target could not be computed or the
	// target cannot be synced (e.g. missing repository without create).
	Error string
}

// Changes counts the changes of the target. Unknown refs count as one: a
// push will happen, but how much it changes is unknown.
func (t Target) Changes() int {
	n := len(t.Refs) + t.RefsOmitted + len(t.Settings) + len(t.Labels)
	for _, b := range []bool{t.Create, t.Privatise, t.Delete, t.RefsUnknown} {
		if b {
			n++
		}
	}

	return n
}

// NeedsPush reports whether the target gets a git push.
func (t Target) NeedsPush() bool {
	return t.Error == "" && !t.Delete && (t.RefsUnknown || len(t.Refs)+t.RefsOmitted > 0)
}

// Truncate returns a copy of r whose targets keep at most maxRefs ref changes
// each; the rest is counted in RefsOmitted.
func (r Repo) Truncate(maxRefs int) Repo {
	r.Targets = slices.Clone(r.Targets)
	for i := range r.Targets {
		t := &r.Targets[i]
		if len(t.Refs) > maxRefs {
			t.RefsOmitted += len(t.Refs) - maxRefs
			t.Refs = t.Refs[:maxRefs:maxRefs]
		}
	}

	return r
}

// RefChange is one ref that a push creates (Old empty), updates or deletes
// (New empty).
type RefChange struct {
	Ref, Old, New string
}

// Kind is KindCreate, KindUpdate or KindDelete.
func (c RefChange) Kind() string {
	switch {
	case c.Old == "":
		return KindCreate
	case c.New == "":
		return KindDelete
	default:
		return KindUpdate
	}
}

// FieldChange is one repository setting, with values formatted for display.
type FieldChange struct {
	// Field is the config key of the setting, e.g. "default_branch".
	Field    string
	Old, New string
}

// LabelChange creates (Old nil), updates or deletes (New nil) a label.
type LabelChange struct {
	Name     string
	Old, New *forge.Label
}

// Kind is KindCreate, KindUpdate or KindDelete.
func (c LabelChange) Kind() string {
	switch {
	case c.Old == nil:
		return KindCreate
	case c.New == nil:
		return KindDelete
	default:
		return KindUpdate
	}
}

// RefChanges compares the refs of source and target (ref name to object id)
// and returns what a mirror push changes, sorted by ref.
func RefChanges(src, dst map[string]string) []RefChange {
	var out []RefChange
	for ref, id := range src {
		switch old, ok := dst[ref]; {
		case !ok:
			out = append(out, RefChange{Ref: ref, New: id})
		case old != id:
			out = append(out, RefChange{Ref: ref, Old: old, New: id})
		}
	}
	for ref, id := range dst {
		if _, ok := src[ref]; !ok {
			out = append(out, RefChange{Ref: ref, Old: id})
		}
	}
	slices.SortFunc(out, func(a, b RefChange) int { return cmp.Compare(a.Ref, b.Ref) })

	return out
}

// SettingsChanges lists the fields diff changes, with their current value,
// in a fixed order.
func SettingsChanges(current forge.Repo, diff forge.Settings) []FieldChange {
	var out []FieldChange
	add := func(field, from, to string) {
		out = append(out, FieldChange{Field: field, Old: from, New: to})
	}

	if diff.Description != nil {
		add("description", current.Description, *diff.Description)
	}
	if diff.Website != nil {
		add("website", current.Website, *diff.Website)
	}
	if diff.Topics != nil {
		add("topics", strings.Join(current.Topics, ", "), strings.Join(*diff.Topics, ", "))
	}
	if diff.DefaultBranch != nil {
		add("default_branch", current.DefaultBranch, *diff.DefaultBranch)
	}
	if diff.Private != nil {
		add("private", strconv.FormatBool(current.Private), strconv.FormatBool(*diff.Private))
	}
	if diff.HasIssues != nil {
		add("has_issues", strconv.FormatBool(current.HasIssues), strconv.FormatBool(*diff.HasIssues))
	}
	if diff.HasWiki != nil {
		add("has_wiki", strconv.FormatBool(current.HasWiki), strconv.FormatBool(*diff.HasWiki))
	}
	if diff.HasPullRequests != nil {
		add("has_pull_requests",
			strconv.FormatBool(current.HasPullRequests), strconv.FormatBool(*diff.HasPullRequests))
	}

	return out
}

// LabelChanges turns the result of [forge.LabelChanges] into changes with
// old and new values, sorted by name. current is the label list the changes
// were computed from.
func LabelChanges(create, update, remove, current []forge.Label) []LabelChange {
	have := make(map[string]forge.Label, len(current))
	for _, l := range current {
		have[strings.ToLower(l.Name)] = l
	}

	changes := make(map[string]LabelChange, len(create)+len(update)+len(remove))
	for _, l := range create {
		changes[l.Name] = LabelChange{Name: l.Name, New: &l}
	}
	for _, l := range update {
		c := LabelChange{Name: l.Name, New: &l}
		if old, ok := have[strings.ToLower(l.Name)]; ok {
			c.Old = &old
		}
		changes[l.Name] = c
	}
	for _, l := range remove {
		changes[l.Name] = LabelChange{Name: l.Name, Old: &l}
	}

	out := make([]LabelChange, 0, len(changes))
	for _, name := range slices.Sorted(maps.Keys(changes)) {
		out = append(out, changes[name])
	}

	return out
}
