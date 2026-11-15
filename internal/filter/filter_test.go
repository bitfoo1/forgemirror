package filter_test

import (
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/repo"
)

func publicRepo() repo.Repository {
	return repo.Repository{
		Owner:    "acme",
		Name:     "app",
		FullName: "acme/app",
		Topics:   []string{"mirror", "go"},
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()

	private := publicRepo()
	private.Private = true
	fork := publicRepo()
	fork.Fork = true
	noTopics := publicRepo()
	noTopics.Topics = nil

	tests := []struct {
		name       string
		rules      filter.Rules
		repo       repo.Repository
		ref        string
		wantMirror bool
		wantReason string
	}{
		{name: "empty rules pass everything", repo: private, ref: "refs/heads/x", wantMirror: true},
		{
			name: "passes all rules",
			rules: filter.Rules{
				IncludeRepos: "acme/.*", ExcludeRepos: "acme/secret-.*", IncludeBranches: []string{"main"},
				ExcludePrivate: true, ExcludeForks: true, RequireTopic: "mirror",
			},
			repo: publicRepo(), ref: "refs/heads/main", wantMirror: true,
		},
		{
			name:  "branch not included",
			rules: filter.Rules{IncludeBranches: []string{"main", "release/.*"}},
			repo:  publicRepo(), ref: "refs/heads/feature", wantReason: "include_branches",
		},
		{
			name:  "branch pattern matches",
			rules: filter.Rules{IncludeBranches: []string{"main", "release/.*"}},
			repo:  publicRepo(), ref: "refs/heads/release/1.0", wantMirror: true,
		},
		{
			name:  "branch patterns are anchored",
			rules: filter.Rules{IncludeBranches: []string{"main"}},
			repo:  publicRepo(), ref: "refs/heads/not-main", wantReason: "include_branches",
		},
		{
			name:  "tag pushes ignore branch rules",
			rules: filter.Rules{IncludeBranches: []string{"main"}},
			repo:  publicRepo(), ref: "refs/tags/v1.0.0", wantMirror: true,
		},
		{
			name:  "missing topic",
			rules: filter.Rules{RequireTopic: "mirror"},
			repo:  noTopics, wantReason: "require_topic",
		},
		{
			name:  "private excluded",
			rules: filter.Rules{ExcludePrivate: true},
			repo:  private, wantReason: "exclude_private",
		},
		{
			name:  "fork excluded",
			rules: filter.Rules{ExcludeForks: true},
			repo:  fork, wantReason: "exclude_forks",
		},
		{
			name:  "not included",
			rules: filter.Rules{IncludeRepos: "other/.*"},
			repo:  publicRepo(), wantReason: "include_repos",
		},
		{
			name:  "include is anchored",
			rules: filter.Rules{IncludeRepos: "app"},
			repo:  publicRepo(), wantReason: "include_repos",
		},
		{
			name:  "excluded",
			rules: filter.Rules{ExcludeRepos: "acme/app"},
			repo:  publicRepo(), wantReason: "exclude_repos",
		},
		{
			// Documented precedence: a repository matched by both rules is
			// not mirrored, so exclude can carve exceptions out of include.
			name:  "exclude wins over include",
			rules: filter.Rules{IncludeRepos: "acme/.*", ExcludeRepos: "acme/app"},
			repo:  publicRepo(), wantReason: "exclude_repos",
		},
		{
			name:  "alternation is anchored as a whole",
			rules: filter.Rules{ExcludeRepos: "acme/app|acme/lib"},
			repo:  publicRepo(), wantReason: "exclude_repos",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := filter.New(tc.rules)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			got := f.Decide(tc.repo, tc.ref)
			if got.Mirror != tc.wantMirror {
				t.Errorf("Decide().Mirror = %v, want %v (reason %q)", got.Mirror, tc.wantMirror, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Decide().Reason = %q, want it to mention %q", got.Reason, tc.wantReason)
			}
			if tc.wantMirror && got.Reason != "" {
				t.Errorf("Decide().Reason = %q, want empty for mirrored events", got.Reason)
			}
		})
	}
}

func TestMatchesIgnoresBranches(t *testing.T) {
	t.Parallel()

	f, err := filter.New(filter.Rules{IncludeBranches: []string{"main"}, ExcludeForks: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Matches(publicRepo()) {
		t.Error("Matches(public repo) = false, want true")
	}

	fork := publicRepo()
	fork.Fork = true
	if f.Matches(fork) {
		t.Error("Matches(fork) = true, want false")
	}
}

func TestNewInvalidPatterns(t *testing.T) {
	t.Parallel()

	rules := filter.Rules{
		IncludeRepos:    "(",
		ExcludeRepos:    "[",
		IncludeBranches: []string{"ok", "", "*"},
	}

	_, err := filter.New(rules)
	if err == nil {
		t.Fatal("New() error = nil, want error")
	}
	for _, want := range []string{
		"filter.include_repos", "filter.exclude_repos",
		"filter.include_branches[1]: empty pattern", "filter.include_branches[2]",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("New() error = %v\nwant it to contain %q", err, want)
		}
	}

	if rules.Validate() == nil {
		t.Error("Validate() = nil, want error")
	}
	if emptyErr := (filter.Rules{}).Validate(); emptyErr != nil {
		t.Errorf("Validate() of empty rules = %v, want nil", emptyErr)
	}
}

func TestAllowPrivate(t *testing.T) {
	t.Parallel()

	f, err := filter.New(filter.Rules{ExcludePrivate: true, AllowPrivate: "acme/notes"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name       string
		fullName   string
		private    bool
		wantMirror bool
		wantReason string
	}{
		{name: "allowed private passes", fullName: "acme/notes", private: true, wantMirror: true},
		{name: "other private rejected", fullName: "acme/other", private: true, wantReason: "filter.exclude_private"},
		{name: "pattern is anchored", fullName: "acme/notes-2", private: true, wantReason: "filter.exclude_private"},
		{name: "public passes", fullName: "acme/other", wantMirror: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := repo.Repository{Owner: "acme", FullName: tc.fullName, Private: tc.private}
			got := f.Decide(r, "")
			if got.Mirror != tc.wantMirror || !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Decide() = %+v, want mirror=%v reason containing %q", got, tc.wantMirror, tc.wantReason)
			}
		})
	}
}

func TestAllowPrivateInvalidPattern(t *testing.T) {
	t.Parallel()

	_, err := filter.New(filter.Rules{AllowPrivate: "("})
	if err == nil || !strings.Contains(err.Error(), "filter.allow_private") {
		t.Errorf("New() error = %v, want filter.allow_private error", err)
	}
}
