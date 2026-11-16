package plan

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"go.bitfoo.de/forgemirror/internal/forge"
)

// ReleaseChange creates (Old nil), updates or deletes (New nil) the release
// of a tag. Old is the release on the target, New the source release.
type ReleaseChange struct {
	Tag      string
	Old, New *forge.Release
	// Fields lists the changed release fields of an update.
	Fields []FieldChange
	// Assets by file name. An update with only asset changes has no Fields;
	// a creation lists every source asset as added.
	AssetsAdded    []string
	AssetsReplaced []string
	AssetsRemoved  []string
}

// Kind is KindCreate, KindUpdate or KindDelete.
func (c ReleaseChange) Kind() string {
	switch {
	case c.Old == nil:
		return KindCreate
	case c.New == nil:
		return KindDelete
	default:
		return KindUpdate
	}
}

// PackageChange is a container package whose tags are copied to Image.
type PackageChange struct {
	// Package is the source package name.
	Package string
	// Image is the target repository (host/path), without credentials.
	Image string
	Tags  []TagChange
	// TagsOmitted counts tag changes left out of Tags to bound memory.
	TagsOmitted int
}

// Count is the number of tag changes, omitted ones included.
func (c PackageChange) Count() int { return len(c.Tags) + c.TagsOmitted }

// TagChange creates (Old empty) or moves a tag; values are digests.
type TagChange struct {
	Tag, Old, New string
}

// Kind is KindCreate or KindUpdate.
func (c TagChange) Kind() string {
	if c.Old == "" {
		return KindCreate
	}

	return KindUpdate
}

// ReleaseChanges compares the desired source releases with the target's.
// published holds the tags of every published source release: with prune,
// target releases whose tag is not among them are deleted. Desired releases
// must not be drafts.
func ReleaseChanges(
	desired, current []forge.Release,
	published map[string]bool,
	caps forge.ReleaseCapabilities,
	prune bool,
) []ReleaseChange {
	have := make(map[string]forge.Release, len(current))
	for _, r := range current {
		have[r.Tag] = r
	}

	sorted := slices.Clone(desired)
	slices.SortStableFunc(sorted, func(a, b forge.Release) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.Tag, b.Tag))
	})

	var out []ReleaseChange
	for _, d := range sorted {
		old, ok := have[d.Tag]
		if !ok {
			out = append(out, ReleaseChange{Tag: d.Tag, New: &d, AssetsAdded: assetNames(d.Assets)})

			continue
		}
		c := ReleaseChange{Tag: d.Tag, Old: &old, New: &d, Fields: releaseFields(old, d, caps)}
		c.AssetsAdded, c.AssetsReplaced, c.AssetsRemoved = assetChanges(old.Assets, d.Assets)
		if len(c.Fields)+len(c.AssetsAdded)+len(c.AssetsReplaced)+len(c.AssetsRemoved) > 0 {
			out = append(out, c)
		}
	}

	if prune {
		var removed []ReleaseChange
		for _, r := range current {
			if !published[r.Tag] {
				removed = append(removed, ReleaseChange{Tag: r.Tag, Old: &r})
			}
		}
		slices.SortFunc(removed, func(a, b ReleaseChange) int { return cmp.Compare(a.Tag, b.Tag) })
		out = append(out, removed...)
	}

	return out
}

func releaseFields(current, desired forge.Release, caps forge.ReleaseCapabilities) []FieldChange {
	var out []FieldChange
	if current.Name != desired.Name {
		out = append(out, FieldChange{Field: "name", Old: current.Name, New: desired.Name})
	}
	if normalizeBody(current.Body) != normalizeBody(desired.Body) {
		out = append(out, FieldChange{Field: "body", Old: summary(current.Body), New: summary(desired.Body)})
	}
	if caps.Prerelease && current.Prerelease != desired.Prerelease {
		out = append(out, FieldChange{
			Field: "prerelease",
			Old:   strconv.FormatBool(current.Prerelease),
			New:   strconv.FormatBool(desired.Prerelease),
		})
	}

	return out
}

// normalizeBody ignores what forges change on their own when storing notes
// (line endings, surrounding whitespace); comparing raw values would report
// a difference on every run that no update can remove.
func normalizeBody(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

// bodySummaryLen bounds how much of the release notes a plan carries.
const bodySummaryLen = 80

func summary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > bodySummaryLen {
		return string(r[:bodySummaryLen]) + "…"
	}

	return s
}

func assetNames(assets []forge.Asset) []string {
	out := make([]string, 0, len(assets))
	for _, a := range assets {
		out = append(out, a.Name)
	}
	slices.Sort(out)

	return out
}

// assetChanges compares assets by name; a different size means new content.
func assetChanges(current, desired []forge.Asset) ([]string, []string, []string) {
	have := make(map[string]int64, len(current))
	for _, a := range current {
		have[a.Name] = a.Size
	}
	want := make(map[string]bool, len(desired))

	var added, replaced, removed []string
	for _, a := range desired {
		want[a.Name] = true
		switch size, ok := have[a.Name]; {
		case !ok:
			added = append(added, a.Name)
		case size != a.Size:
			replaced = append(replaced, a.Name)
		}
	}
	for _, a := range current {
		if !want[a.Name] {
			removed = append(removed, a.Name)
		}
	}
	slices.Sort(added)
	slices.Sort(replaced)
	slices.Sort(removed)

	return added, replaced, removed
}

// TagChanges compares the digests of source and target tags. Tags missing on
// the target or pointing elsewhere are copied; signature and attestation tags
// ("sha256-…") come last, after the images they refer to.
func TagChanges(src, dst map[string]string) []TagChange {
	var out []TagChange
	for tag, digest := range src {
		if dst[tag] != digest {
			out = append(out, TagChange{Tag: tag, Old: dst[tag], New: digest})
		}
	}
	slices.SortFunc(out, func(a, b TagChange) int {
		return cmp.Or(
			cmp.Compare(btoi(strings.HasPrefix(a.Tag, "sha256-")), btoi(strings.HasPrefix(b.Tag, "sha256-"))),
			cmp.Compare(a.Tag, b.Tag),
		)
	})

	return out
}

func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
}
