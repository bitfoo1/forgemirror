package hold_test

import (
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/hold"
)

func TestScopeValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		scope hold.Scope
		valid bool
	}{
		{"all", hold.Scope{AllTargets: true, AllRepositories: true}, true},
		{"selected", hold.Scope{Targets: []string{"github", "codeberg"}, Repositories: []string{"acme/app", "tools/*"}}, true},
		{"empty", hold.Scope{}, false},
		{"too many selections", hold.Scope{Targets: make([]string, hold.MaxSelections+1), AllRepositories: true}, false},
		{"oversized selection", hold.Scope{Targets: []string{strings.Repeat("a", 257)}, AllRepositories: true}, false},
		{"whitespace", hold.Scope{Targets: []string{"not a target"}, AllRepositories: true}, false},
		{"empty targets", hold.Scope{AllRepositories: true}, false},
		{"empty repos", hold.Scope{AllTargets: true}, false},
		{"ambiguous targets", hold.Scope{AllTargets: true, Targets: []string{"github"}, AllRepositories: true}, false},
		{"ambiguous repos", hold.Scope{AllTargets: true, AllRepositories: true, Repositories: []string{"acme/app"}}, false},
		{"malformed glob", hold.Scope{AllTargets: true, Repositories: []string{"acme/[a-z]"}}, false},
		{"empty component", hold.Scope{AllTargets: true, Repositories: []string{"acme/"}}, false},
		{"no owner", hold.Scope{AllTargets: true, Repositories: []string{"*"}}, false},
		{"duplicate", hold.Scope{Targets: []string{"github", "github"}, AllRepositories: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.scope.Validate(); (err == nil) != tc.valid {
				t.Errorf("Validate=%v; valid=%v", err, tc.valid)
			}
		})
	}
}

func TestScopeClone(t *testing.T) {
	t.Parallel()
	original := hold.Scope{Targets: []string{"github"}, Repositories: []string{"acme/*"}}
	clone := original.Clone()
	clone.Targets[0] = "gitlab"
	clone.Repositories[0] = "other/*"
	if original.Targets[0] != "github" || original.Repositories[0] != "acme/*" {
		t.Fatal("clone aliases original slices")
	}
}

func TestScopeMatches(t *testing.T) {
	t.Parallel()
	scope := hold.Scope{
		Targets:      []string{"github", "codeberg"},
		Repositories: []string{"acme/app", "tools/release-?ot"},
	}
	for _, tc := range []struct {
		repo, target string
		want         bool
	}{
		{"acme/app", "github", true}, {"acme/app", "codeberg", true}, {"acme/app", "gitlab", false}, {"acme/docs", "github", false}, {"tools/release-bot", "github", true}, {"tools/release-robot", "github", false},
	} {
		t.Run(tc.repo+tc.target, func(t *testing.T) {
			t.Parallel()
			if got := scope.Matches(tc.repo, tc.target); got != tc.want {
				t.Errorf("Matches=%v", got)
			}
		})
	}
	if !(hold.Scope{AllTargets: true, Repositories: []string{"acme/*"}}).Matches("acme/future", "new-target") {
		t.Fatal("future matches excluded")
	}
	if (hold.Scope{}).Matches("acme/app", "github") {
		t.Fatal("empty scope became all")
	}
}
