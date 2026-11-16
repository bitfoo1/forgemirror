package plan_test

import (
	"reflect"
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/plan"
)

func TestRefChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src, dst map[string]string
		want     []plan.RefChange
	}{
		{name: "equal", src: map[string]string{"refs/heads/main": "a"}, dst: map[string]string{"refs/heads/main": "a"}},
		{name: "both empty"},
		{
			name: "empty target gets everything",
			src:  map[string]string{"refs/tags/v1": "t", "refs/heads/main": "a"},
			want: []plan.RefChange{{Ref: "refs/heads/main", New: "a"}, {Ref: "refs/tags/v1", New: "t"}},
		},
		{
			name: "create update delete sorted",
			src:  map[string]string{"refs/heads/main": "b", "refs/heads/new": "n"},
			dst:  map[string]string{"refs/heads/main": "a", "refs/heads/gone": "g"},
			want: []plan.RefChange{
				{Ref: "refs/heads/gone", Old: "g"},
				{Ref: "refs/heads/main", Old: "a", New: "b"},
				{Ref: "refs/heads/new", New: "n"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := plan.RefChanges(tt.src, tt.dst); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RefChanges = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRefChangeKind(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		c    plan.RefChange
		want string
	}{
		{plan.RefChange{New: "a"}, "create"},
		{plan.RefChange{Old: "a", New: "b"}, "update"},
		{plan.RefChange{Old: "a"}, "delete"},
	} {
		if got := tt.c.Kind(); got != tt.want {
			t.Errorf("%+v.Kind() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestSettingsChanges(t *testing.T) {
	t.Parallel()

	current := forge.Repo{
		Description: "old", Website: "https://old", Topics: []string{"a", "b"},
		DefaultBranch: "master", HasIssues: true,
	}
	desired := forge.Repo{
		Description: "new", Website: "https://new", Topics: []string{"c"},
		DefaultBranch: "main", Private: true, HasWiki: true, HasPullRequests: true,
	}
	diff := forge.Diff(current, desired, forge.Capabilities{Website: true, PullRequests: true})

	want := []plan.FieldChange{
		{Field: "description", Old: "old", New: "new"},
		{Field: "website", Old: "https://old", New: "https://new"},
		{Field: "topics", Old: "a, b", New: "c"},
		{Field: "default_branch", Old: "master", New: "main"},
		{Field: "private", Old: "false", New: "true"},
		{Field: "has_issues", Old: "true", New: "false"},
		{Field: "has_wiki", Old: "false", New: "true"},
		{Field: "has_pull_requests", Old: "false", New: "true"},
	}
	if got := plan.SettingsChanges(current, diff); !reflect.DeepEqual(got, want) {
		t.Errorf("SettingsChanges =\n%#v\nwant\n%#v", got, want)
	}

	if got := plan.SettingsChanges(current, forge.Settings{}); got != nil {
		t.Errorf("SettingsChanges(no diff) = %#v, want nil", got)
	}
}

func TestLabelChanges(t *testing.T) {
	t.Parallel()

	current := []forge.Label{{Name: "Bug", Color: "ff0000"}, {Name: "old", Color: "000000"}}
	desired := []forge.Label{{Name: "bug", Color: "00ff00"}, {Name: "feature", Color: "0000ff"}}
	create, update, remove := forge.LabelChanges(current, desired, true)

	got := plan.LabelChanges(create, update, remove, current)
	want := []plan.LabelChange{
		{Name: "bug", Old: &forge.Label{Name: "Bug", Color: "ff0000"}, New: &forge.Label{Name: "bug", Color: "00ff00"}},
		{Name: "feature", New: &forge.Label{Name: "feature", Color: "0000ff"}},
		{Name: "old", Old: &forge.Label{Name: "old", Color: "000000"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LabelChanges =\n%#v\nwant\n%#v", got, want)
	}
	kinds := []string{got[0].Kind(), got[1].Kind(), got[2].Kind()}
	if !reflect.DeepEqual(kinds, []string{"update", "create", "delete"}) {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestChanges(t *testing.T) {
	t.Parallel()

	repo := plan.Repo{Targets: []plan.Target{
		{
			Name: "a", Create: true, Privatise: true,
			Refs:     []plan.RefChange{{Ref: "r", New: "x"}},
			Settings: []plan.FieldChange{{Field: "description"}},
			Labels:   []plan.LabelChange{{Name: "bug"}},
		},
		{Name: "b", RefsUnknown: true},
		{Name: "c", Delete: true},
		{Name: "d"},
	}}
	if got := repo.Changes(); got != 7 {
		t.Errorf("Changes = %d, want 7", got)
	}
	if repo.Failed() {
		t.Error("Failed = true without errors")
	}

	needsPush := []bool{true, true, false, false}
	for i, tg := range repo.Targets {
		if tg.NeedsPush() != needsPush[i] {
			t.Errorf("target %s NeedsPush = %v", tg.Name, tg.NeedsPush())
		}
	}

	failed := plan.Repo{Targets: []plan.Target{{Name: "a", Error: "boom", Refs: []plan.RefChange{{Ref: "r"}}}}}
	if !failed.Failed() || failed.Targets[0].NeedsPush() {
		t.Error("a target error must fail the plan and block the push")
	}
	if !(plan.Repo{Error: "x"}).Failed() {
		t.Error("a repository error must fail the plan")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	refs := []plan.RefChange{{Ref: "a", New: "1"}, {Ref: "b", New: "2"}, {Ref: "c", New: "3"}}
	orig := plan.Repo{Targets: []plan.Target{{Name: "t", Refs: refs}, {Name: "u"}}}

	got := orig.Truncate(2)
	if len(got.Targets[0].Refs) != 2 || got.Targets[0].RefsOmitted != 1 {
		t.Errorf("Truncate = %+v, want 2 refs and 1 omitted", got.Targets[0])
	}
	if got.Changes() != 3 || !got.Targets[0].NeedsPush() {
		t.Errorf("omitted refs must still count: changes %d", got.Changes())
	}
	if len(orig.Targets[0].Refs) != 3 || orig.Targets[0].RefsOmitted != 0 {
		t.Error("Truncate modified the original")
	}
}
