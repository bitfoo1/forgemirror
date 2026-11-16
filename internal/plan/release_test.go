package plan_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
)

func day(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }

func tags(changes []plan.ReleaseChange) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Kind()+" "+c.Tag)
	}

	return out
}

func TestReleaseChanges(t *testing.T) {
	t.Parallel()

	caps := forge.ReleaseCapabilities{Prerelease: true}
	desired := []forge.Release{
		{
			Tag:       "v3",
			Name:      "three",
			CreatedAt: day(3),
			Assets:    []forge.Asset{{Name: "b", Size: 1}, {Name: "a", Size: 1}},
		},
		{Tag: "v1", Name: "one", Body: "notes\r\n", CreatedAt: day(1), Assets: []forge.Asset{{Name: "x", Size: 2}}},
		{Tag: "v2", Name: "two", CreatedAt: day(2), Prerelease: true, Assets: []forge.Asset{
			{Name: "keep", Size: 5}, {Name: "changed", Size: 6}, {Name: "new", Size: 7},
		}},
	}
	current := []forge.Release{
		{Tag: "v1", Name: "one", Body: "  notes\n", Assets: []forge.Asset{{Name: "x", Size: 2}}},
		{Tag: "v2", Name: "zwei", Assets: []forge.Asset{
			{Name: "keep", Size: 5}, {Name: "changed", Size: 1}, {Name: "gone", Size: 1},
		}},
		{Tag: "v0", Name: "zero"},
		{Tag: "draft-on-source", Name: "d"},
	}
	published := map[string]bool{"v1": true, "v2": true, "v3": true, "draft-on-source": false}

	got := plan.ReleaseChanges(desired, current, published, caps, false)
	if want := []string{"update v2", "create v3"}; !slices.Equal(tags(got), want) {
		t.Fatalf("changes = %v, want %v (oldest first, unchanged v1 left out)", tags(got), want)
	}

	upd := got[0]
	if len(upd.Fields) != 2 || upd.Fields[0].Field != "name" || upd.Fields[1].Field != "prerelease" ||
		upd.Fields[1].Old != "false" || upd.Fields[1].New != "true" {
		t.Errorf("update fields = %+v", upd.Fields)
	}
	if !slices.Equal(upd.AssetsAdded, []string{"new"}) || !slices.Equal(upd.AssetsReplaced, []string{"changed"}) ||
		!slices.Equal(upd.AssetsRemoved, []string{"gone"}) {
		t.Errorf("update assets = +%v ~%v -%v", upd.AssetsAdded, upd.AssetsReplaced, upd.AssetsRemoved)
	}
	if create := got[1]; !slices.Equal(create.AssetsAdded, []string{"a", "b"}) || create.Old != nil {
		t.Errorf("create = %+v", create)
	}

	pruned := plan.ReleaseChanges(desired, current, published, caps, true)
	if want := []string{
		"update v2",
		"create v3",
		"delete draft-on-source",
		"delete v0",
	}; !slices.Equal(
		tags(pruned),
		want,
	) {
		t.Errorf("pruned changes = %v, want %v", tags(pruned), want)
	}
}

func TestReleaseChangesWithoutPrereleaseFlag(t *testing.T) {
	t.Parallel()

	got := plan.ReleaseChanges(
		[]forge.Release{{Tag: "v1", Prerelease: true}},
		[]forge.Release{{Tag: "v1"}},
		map[string]bool{"v1": true},
		forge.ReleaseCapabilities{},
		false,
	)
	if len(got) != 0 {
		t.Errorf("changes = %+v, want none: the target cannot store prerelease", got)
	}
}

func TestReleaseBodySummary(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("word ", 40)
	got := plan.ReleaseChanges(
		[]forge.Release{{Tag: "v1", Body: long}},
		[]forge.Release{{Tag: "v1", Body: "old\nbody"}},
		map[string]bool{"v1": true},
		forge.ReleaseCapabilities{},
		false,
	)
	if len(got) != 1 || len(got[0].Fields) != 1 {
		t.Fatalf("changes = %+v, want one body change", got)
	}
	f := got[0].Fields[0]
	if f.Field != "body" || f.Old != "old body" || !strings.HasSuffix(f.New, "…") || len([]rune(f.New)) != 81 {
		t.Errorf("body change = %+v, want summarised values", f)
	}
	if got[0].New.Body != long {
		t.Error("the change must carry the full body for the update")
	}
}

func TestTagChanges(t *testing.T) {
	t.Parallel()

	got := plan.TagChanges(
		map[string]string{"latest": "d2", "1.0.0": "d1", "sha256-d1.sig": "s1", "0.9.0": "d0"},
		map[string]string{"latest": "old", "0.9.0": "d0"},
	)
	want := []plan.TagChange{
		{Tag: "1.0.0", New: "d1"},
		{Tag: "latest", Old: "old", New: "d2"},
		{Tag: "sha256-d1.sig", New: "s1"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("TagChanges() = %+v, want %+v", got, want)
	}
	if got[0].Kind() != plan.KindCreate || got[1].Kind() != plan.KindUpdate {
		t.Errorf("kinds = %s, %s", got[0].Kind(), got[1].Kind())
	}
}

func TestChangesAndTruncateCountPackagesAndReleases(t *testing.T) {
	t.Parallel()

	tgs := make([]plan.TagChange, 5)
	r := plan.Repo{Targets: []plan.Target{{
		Releases: []plan.ReleaseChange{{Tag: "v1"}},
		Packages: []plan.PackageChange{{Package: "app", Tags: tgs}},
	}}}
	if got := r.Changes(); got != 6 {
		t.Errorf("Changes() = %d, want 6", got)
	}

	cut := r.Truncate(2)
	p := cut.Targets[0].Packages[0]
	if len(p.Tags) != 2 || p.TagsOmitted != 3 || p.Count() != 5 || cut.Changes() != 6 {
		t.Errorf("truncated package = %d tags, %d omitted; changes %d", len(p.Tags), p.TagsOmitted, cut.Changes())
	}
	if len(r.Targets[0].Packages[0].Tags) != 5 {
		t.Error("Truncate changed the original plan")
	}
}
