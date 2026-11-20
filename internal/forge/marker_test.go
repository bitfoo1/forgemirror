package forge_test

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
)

func TestPublicMirrorIdentity(t *testing.T) {
	t.Parallel()
	marker := forge.ResolveMarker(
		forge.Settings{MirrorURL: new("https://public.example/{{ .FullName }}"), MirrorDescriptionLimit: new(100)},
	)
	source := forge.Repo{Description: strings.Repeat("é", 100)}
	got, err := marker.Apply(source, repo.Repository{FullName: "acme/app"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Website != "https://public.example/acme/app" || len(got.Description) > 100 ||
		!strings.Contains(got.Description, "Contribute: https://public.example/acme/app") ||
		!utf8.ValidString(got.Description) {
		t.Fatalf("identity=%+v", got)
	}
	if diff := forge.Diff(
		source,
		got,
		forge.Capabilities{Website: false},
	); diff.Website != nil ||
		diff.Description == nil {
		t.Fatal("unsupported website lost contribution description or was sent anyway")
	}
	again, err := marker.Apply(got, repo.Repository{FullName: "acme/app"})
	if err != nil || again.Description != got.Description {
		t.Fatalf("identity changed on repeat: %+v %v", again, err)
	}
}

func TestMirrorIdentityOverrides(t *testing.T) {
	t.Parallel()
	marker := forge.ResolveMarker(
		forge.Settings{MirrorURL: new("https://public.example/")},
		forge.Settings{MirrorURL: new(""), MirrorNote: new("")},
	)
	result, err := marker.Apply(
		forge.Repo{Website: "https://project.example/", Description: "project"},
		repo.Repository{},
	)
	if err != nil || result.Website != "https://project.example/" || result.Description != "project" {
		t.Fatalf("override=%+v %v", result, err)
	}
	if _, err = (forge.Marker{Note: "A marker that cannot fit", DescriptionLimit: 5}).Apply(
		forge.Repo{},
		repo.Repository{},
	); err == nil {
		t.Fatal("oversized marker accepted")
	}
}

func TestMirrorURLValidation(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"javascript:alert(1)", "https://user:secret@example.com/", "{{.Unknown}}", "https://example.com/\nnext"} {
		if err := (forge.Settings{MirrorURL: &url}).Validate(); err == nil {
			t.Errorf("invalid mirror URL accepted: %q", url)
		}
	}
}

func TestResolveMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		layers []forge.Settings
		want   forge.Marker
	}{
		{
			name: "defaults",
			want: forge.Marker{Note: forge.DefaultMirrorNote, Topic: forge.DefaultMirrorTopic},
		},
		{
			name:   "disabled",
			layers: []forge.Settings{{MirrorNote: new(""), MirrorTopic: new("")}},
			want:   forge.Marker{},
		},
		{
			name: "last layer wins",
			layers: []forge.Settings{
				{MirrorNote: new(""), MirrorTopic: new("mirror")},
				{MirrorNote: new("custom")},
			},
			want: forge.Marker{Note: "custom", Topic: "mirror"},
		},
		{
			name:   "unrelated layer keeps defaults",
			layers: []forge.Settings{{HasWiki: new(false)}},
			want:   forge.Marker{Note: forge.DefaultMirrorNote, Topic: forge.DefaultMirrorTopic},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := forge.ResolveMarker(tc.layers...); got != tc.want {
				t.Errorf("ResolveMarker = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMarkerApply(t *testing.T) {
	t.Parallel()

	src := repo.Repository{Owner: "acme", Name: "app", FullName: "acme/app"}
	tests := []struct {
		name       string
		marker     forge.Marker
		repo       forge.Repo
		wantDesc   string
		wantTopics []string
	}{
		{
			name:       "appends note and topic",
			marker:     forge.Marker{Note: "Mirror.", Topic: "forgemirror"},
			repo:       forge.Repo{Description: "the app", Topics: []string{"go"}},
			wantDesc:   "the app · Mirror.",
			wantTopics: []string{"forgemirror", "go"},
		},
		{
			name:       "empty description gets the note alone",
			marker:     forge.Marker{Note: "Mirror."},
			repo:       forge.Repo{Description: "  "},
			wantDesc:   "Mirror.",
			wantTopics: nil,
		},
		{
			name:       "note already present is not repeated",
			marker:     forge.Marker{Note: "Mirror.", Topic: "forgemirror"},
			repo:       forge.Repo{Description: "the app · Mirror.", Topics: []string{"forgemirror"}},
			wantDesc:   "the app · Mirror.",
			wantTopics: []string{"forgemirror"},
		},
		{
			name:       "template renders repository fields",
			marker:     forge.Marker{Note: "Mirror of {{ .FullName }} ({{ .Owner }}/{{ .Name }})"},
			repo:       forge.Repo{},
			wantDesc:   "Mirror of acme/app (acme/app)",
			wantTopics: nil,
		},
		{
			name:       "topic is lower-cased",
			marker:     forge.Marker{Topic: "Mirror"},
			repo:       forge.Repo{Description: "the app", Topics: []string{"go"}},
			wantDesc:   "the app",
			wantTopics: []string{"go", "mirror"},
		},
		{
			name:       "disabled marker changes nothing",
			marker:     forge.Marker{},
			repo:       forge.Repo{Description: "the app ", Topics: []string{"go"}},
			wantDesc:   "the app ",
			wantTopics: []string{"go"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := tc.repo
			inTopics := slices.Clone(in.Topics)
			got, err := tc.marker.Apply(in, src)
			if err != nil {
				t.Fatalf("Apply error = %v", err)
			}
			if got.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tc.wantDesc)
			}
			if !slices.Equal(got.Topics, tc.wantTopics) {
				t.Errorf("Topics = %v, want %v", got.Topics, tc.wantTopics)
			}
			if !slices.Equal(in.Topics, inTopics) {
				t.Errorf("input topics modified: %v, want %v", in.Topics, inTopics)
			}
		})
	}
}

func TestMarkerApplyErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		note string
		want string
	}{
		{name: "parse error", note: "{{ .Name", want: "mirror_note"},
		{name: "unknown field", note: "{{ .Nope }}", want: "Nope"},
		{name: "multi-line", note: "a\nb", want: "single line"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := forge.Marker{Note: tc.note}.Apply(forge.Repo{}, repo.Repository{Name: "app"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Apply error = %v, want one containing %q", err, tc.want)
			}
			if verr := (forge.Settings{MirrorNote: &tc.note}).Validate(); verr == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

func TestSettingsValidateMirrorNote(t *testing.T) {
	t.Parallel()

	for _, note := range []string{"", forge.DefaultMirrorNote, "Mirror of {{ .FullName }}"} {
		if err := (forge.Settings{MirrorNote: &note}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", note, err)
		}
	}
}
