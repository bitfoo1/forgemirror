package ui

import (
	"reflect"
	"slices"

	"go.bitfoo.de/forgemirror/internal/plan"
)

type changeGroup struct {
	Plan   *plan.Target
	Badges []forgeBadge
}

type changeGroups struct {
	rows  []changeGroup
	index map[string][]int
}

// Full payload equality keeps changes with identical shortened IDs, but different
// preconditions or destination values, from being presented as the same write.
func (g *changeGroups) add(key string, part plan.Target, badge forgeBadge) {
	for _, i := range g.index[key] {
		row := &g.rows[i]
		if reflect.DeepEqual(*row.Plan, part) {
			if !slices.ContainsFunc(row.Badges, func(b forgeBadge) bool { return b.Name == badge.Name }) {
				row.Badges = append(row.Badges, badge)
			}
			return
		}
	}
	g.rows = append(
		g.rows,
		changeGroup{Plan: &part, Badges: []forgeBadge{badge}},
	)
	g.index[key] = append(g.index[key], len(g.rows)-1)
}

func groupedChanges(targets []targetView) []changeGroup {
	groups := changeGroups{index: make(map[string][]int)}
	for _, target := range targets {
		if target.Plan == nil {
			continue
		}
		groups.addRepositoryChanges(target.Plan, target.Badge)
		groups.addRefChanges(target.Plan, target.Badge)
		groups.addMetadataChanges(target.Plan, target.Badge)
	}
	return groups.rows
}

func (g *changeGroups) addRepositoryChanges(p *plan.Target, badge forgeBadge) {
	if p.Error != "" {
		g.add("error", plan.Target{Error: p.Error}, badge)
	}
	for _, part := range []struct {
		enabled bool
		key     string
		plan    plan.Target
	}{
		{p.Create, "create", plan.Target{Create: true}},
		{p.Privatise, "privatise", plan.Target{Privatise: true}},
		{p.Delete, "delete", plan.Target{Delete: true}},
		{p.RefsUnknown, "refs-unknown", plan.Target{RefsUnknown: true}},
	} {
		if part.enabled {
			g.add(part.key, part.plan, badge)
		}
	}
	for _, warning := range p.Warnings {
		g.add("warning", plan.Target{Warnings: []string{warning}}, badge)
	}
}

func (g *changeGroups) addRefChanges(p *plan.Target, badge forgeBadge) {
	for _, ref := range p.Refs {
		g.add("ref:"+ref.Ref, plan.Target{Refs: []plan.RefChange{ref}}, badge)
	}
	// Omitted refs can differ even when their counts match; don't imply equality.
	if p.RefsOmitted > 0 {
		g.add("omitted:"+badge.Name, plan.Target{RefsOmitted: p.RefsOmitted}, badge)
	}
}

func (g *changeGroups) addMetadataChanges(p *plan.Target, badge forgeBadge) {
	for _, field := range p.Settings {
		g.add("setting:"+field.Field, plan.Target{Settings: []plan.FieldChange{field}}, badge)
	}
	for _, label := range p.Labels {
		g.add("label:"+label.Name, plan.Target{Labels: []plan.LabelChange{label}}, badge)
	}
	for _, release := range p.Releases {
		g.add("release:"+release.Tag, plan.Target{Releases: []plan.ReleaseChange{release}}, badge)
	}
	for _, pkg := range p.Packages {
		key := "package:" + pkg.Package
		if pkg.TagsOmitted > 0 {
			key += ":" + badge.Name
		}
		g.add(key, plan.Target{Packages: []plan.PackageChange{pkg}}, badge)
	}
}
