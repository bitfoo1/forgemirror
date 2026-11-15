package forge_test

import (
	"slices"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge"
)

func TestResolveVisibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    forge.Repo
		layers []forge.Settings
		want   bool
	}{
		{"private source, no layers", forge.Repo{Private: true}, nil, true},
		{
			"private source beats private=false",
			forge.Repo{Private: true},
			[]forge.Settings{{Private: new(false)}},
			true,
		},
		{"public source, private layer", forge.Repo{}, []forge.Settings{{Private: new(true)}}, true},
		{"public source, no layers", forge.Repo{}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, _ := forge.Resolve(tc.src, tc.layers...)
			if got.Private != tc.want {
				t.Fatalf("Private = %v, want %v", got.Private, tc.want)
			}
		})
	}
}

func TestResolveLayersLastWins(t *testing.T) {
	t.Parallel()

	src := forge.Repo{HasIssues: true, HasWiki: true, Description: "a", Website: "https://w"}
	got, _ := forge.Resolve(src,
		forge.Settings{HasIssues: new(false), Description: new("b")},
		forge.Settings{HasIssues: new(true)},
	)

	if !got.HasIssues {
		t.Error("HasIssues = false, want true")
	}
	if got.Description != "b" {
		t.Errorf("Description = %q, want b", got.Description)
	}
	if !got.HasWiki || got.Website != "https://w" {
		t.Errorf("untouched fields changed: %+v", got)
	}
}

func TestResolveTopicsNormalized(t *testing.T) {
	t.Parallel()

	got, _ := forge.Resolve(forge.Repo{Topics: []string{"Go", "go", "Mirror"}})
	if want := []string{"go", "mirror"}; !slices.Equal(got.Topics, want) {
		t.Fatalf("Topics = %v, want %v", got.Topics, want)
	}

	got, _ = forge.Resolve(forge.Repo{Topics: []string{"x"}}, forge.Settings{Topics: &[]string{"B", "a", "b"}})
	if want := []string{"a", "b"}; !slices.Equal(got.Topics, want) {
		t.Fatalf("layer Topics = %v, want %v", got.Topics, want)
	}
}

func TestResolveSyncLabels(t *testing.T) {
	t.Parallel()

	if _, sync := forge.Resolve(forge.Repo{}); !sync {
		t.Error("default syncLabels = false, want true")
	}
	if _, sync := forge.Resolve(forge.Repo{}, forge.Settings{Labels: new(false)}); sync {
		t.Error("syncLabels = true with labels=false")
	}
}

func TestResolveAllFields(t *testing.T) {
	t.Parallel()

	got, _ := forge.Resolve(forge.Repo{}, forge.Settings{
		Website:         new("https://x"),
		DefaultBranch:   new("main"),
		HasWiki:         new(true),
		HasPullRequests: new(true),
	})
	want := forge.Repo{
		Website: "https://x", DefaultBranch: "main", HasWiki: true, HasPullRequests: true,
	}
	if got.Website != want.Website || got.DefaultBranch != want.DefaultBranch ||
		got.HasWiki != want.HasWiki || got.HasPullRequests != want.HasPullRequests {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func diffBase() forge.Repo {
	return forge.Repo{Description: "d", Website: "w", Topics: []string{"a", "b"}, DefaultBranch: "main"}
}

func TestDiffEqual(t *testing.T) {
	t.Parallel()

	base := diffBase()

	if d := forge.Diff(base, base, forge.Capabilities{Website: true, PullRequests: true}); !d.IsZero() {
		t.Fatalf("diff not zero: %+v", d)
	}
}

func TestDiffWebsiteCapability(t *testing.T) {
	t.Parallel()

	base := diffBase()

	want := base
	want.Website = "other"
	if d := forge.Diff(base, want, forge.Capabilities{}); d.Website != nil {
		t.Fatalf("Website = %v, want nil", *d.Website)
	}
	if d := forge.Diff(base, want, forge.Capabilities{Website: true}); d.Website == nil || *d.Website != "other" {
		t.Fatalf("Website diff missing with capability: %+v", d)
	}
}

func TestDiffPullRequestsCapability(t *testing.T) {
	t.Parallel()

	base := diffBase()

	want := base
	want.HasPullRequests = true
	if d := forge.Diff(base, want, forge.Capabilities{}); d.HasPullRequests != nil {
		t.Fatal("HasPullRequests set without capability")
	}
	if d := forge.Diff(
		base,
		want,
		forge.Capabilities{PullRequests: true},
	); d.HasPullRequests == nil ||
		!*d.HasPullRequests {
		t.Fatal("HasPullRequests diff missing with capability")
	}
}

func TestDiffTopicsAsSets(t *testing.T) {
	t.Parallel()

	base := diffBase()

	want := base
	want.Topics = []string{"b", "a"}
	if d := forge.Diff(base, want, forge.Capabilities{}); d.Topics != nil {
		t.Fatalf("Topics = %v, want nil", *d.Topics)
	}
	want.Topics = []string{"a"}
	if d := forge.Diff(base, want, forge.Capabilities{}); d.Topics == nil {
		t.Fatal("Topics diff missing")
	}
}

func TestDiffScalarFields(t *testing.T) {
	t.Parallel()

	base := diffBase()

	want := forge.Repo{
		Description: "n", Website: "w", Topics: []string{"a", "b"}, DefaultBranch: "main",
		Private: true, HasIssues: true, HasWiki: true,
	}
	d := forge.Diff(base, want, forge.Capabilities{})
	if d.DefaultBranch != nil || d.Description == nil || *d.Description != "n" {
		t.Fatalf("unexpected diff: %+v", d)
	}
	if d.Private == nil || d.HasIssues == nil || d.HasWiki == nil || d.Labels != nil {
		t.Fatalf("unexpected diff: %+v", d)
	}
}

func TestDiffDefaultBranch(t *testing.T) {
	t.Parallel()

	base := diffBase()

	cur := base
	cur.DefaultBranch = "master"
	d := forge.Diff(cur, base, forge.Capabilities{})
	if d.DefaultBranch == nil || *d.DefaultBranch != "main" {
		t.Fatalf("DefaultBranch = %v, want main", d.DefaultBranch)
	}
}

func TestLabelChanges(t *testing.T) {
	t.Parallel()

	cur := []forge.Label{{Name: "bug", Color: "#D73A4A", Description: "x"}}

	tests := []struct {
		name                   string
		desired                []forge.Label
		prune                  bool
		wantCreate, wantUpdate int
		wantRemove             int
	}{
		{"equal after normalisation", []forge.Label{{Name: "Bug", Color: "d73a4a", Description: "x"}}, false, 0, 0, 0},
		{
			"new label",
			[]forge.Label{{Name: "bug", Color: "d73a4a", Description: "x"}, {Name: "new", Color: "000000"}},
			false,
			1,
			0,
			0,
		},
		{"color change", []forge.Label{{Name: "bug", Color: "ffffff", Description: "x"}}, false, 0, 1, 0},
		{"description change", []forge.Label{{Name: "bug", Color: "d73a4a", Description: "y"}}, false, 0, 1, 0},
		{"extra current, no prune", nil, false, 0, 0, 0},
		{"extra current, prune", nil, true, 0, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, u, r := forge.LabelChanges(cur, tc.desired, tc.prune)
			if len(c) != tc.wantCreate || len(u) != tc.wantUpdate || len(r) != tc.wantRemove {
				t.Fatalf("create/update/remove = %d/%d/%d, want %d/%d/%d",
					len(c), len(u), len(r), tc.wantCreate, tc.wantUpdate, tc.wantRemove)
			}
		})
	}
}

func TestNormalizeColor(t *testing.T) {
	t.Parallel()

	if got := forge.NormalizeColor("#D73A4A"); got != "d73a4a" {
		t.Fatalf("got %q", got)
	}
}

func TestSettingsValidate(t *testing.T) {
	t.Parallel()

	if err := (forge.Settings{Private: new(false)}).Validate(); err == nil ||
		!strings.Contains(err.Error(), "private: only true is allowed") {
		t.Fatalf("err = %v", err)
	}
	if err := (forge.Settings{Private: new(true)}).Validate(); err != nil {
		t.Fatalf("err = %v", err)
	}
	if err := (forge.Settings{}).Validate(); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSettingsIsZero(t *testing.T) {
	t.Parallel()

	if !(forge.Settings{}).IsZero() {
		t.Error("empty settings not zero")
	}
	if (forge.Settings{Labels: new(true)}).IsZero() {
		t.Error("labels-only settings reported zero")
	}
}
