// Package filter decides which push events lead to a mirror run.
package filter

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.bitfoo.de/forgemirror/internal/repo"
)

const branchRefPrefix = "refs/heads/"

// Rules is the user-facing filter configuration. All rules are optional; an
// empty Rules mirrors everything.
//
// Patterns are Go regular expressions (RE2 syntax) and must match the whole
// value: "app" matches only "app", not "my-app"; use "app.*" for prefixes.
// Implicit anchoring avoids the classic surprise of a pattern matching any
// name that merely contains it.
type Rules struct {
	// IncludeRepos: only repositories whose "owner/name" matches are mirrored.
	IncludeRepos string `koanf:"include_repos"`
	// ExcludeRepos: repositories whose "owner/name" matches are never
	// mirrored. Exclude wins over include.
	ExcludeRepos string `koanf:"exclude_repos"`
	// IncludeBranches: only pushes to matching branches trigger a mirror.
	// Tag pushes always trigger, because `git push --mirror` transfers all
	// refs and tags are part of the mirror by design.
	IncludeBranches []string `koanf:"include_branches"`
	// ExcludePrivate skips repositories that are private on Forgejo.
	ExcludePrivate bool `koanf:"exclude_private"`
	// AllowPrivate: private repositories whose "owner/name" matches pass
	// ExcludePrivate. Private repositories stay private on every target.
	AllowPrivate string `koanf:"allow_private"`
	// ExcludeForks skips forks.
	ExcludeForks bool `koanf:"exclude_forks"`
	// RequireTopic, if set, mirrors only repositories carrying this topic
	// (opt-in per repository).
	RequireTopic string `koanf:"require_topic"`
}

// Filter is a compiled, immutable set of [Rules], safe for concurrent use.
type Filter struct {
	rules        Rules
	include      *regexp.Regexp
	exclude      *regexp.Regexp
	allowPrivate *regexp.Regexp
	branches     []*regexp.Regexp
}

// Decision explains whether an event is mirrored and, if not, why.
type Decision struct {
	Mirror bool
	// Reason names the rule that rejected the event; empty if Mirror is true.
	Reason string
}

// New compiles the rules. Patterns are compiled once here instead of per
// event: compiling is far more expensive than matching, and invalid patterns
// surface at startup rather than on the first push.
func New(rules Rules) (*Filter, error) {
	f := &Filter{rules: rules}

	var errs []error
	compile := func(key, pattern string) *regexp.Regexp {
		if pattern == "" {
			return nil
		}
		re, err := regexp.Compile(`^(?:` + pattern + `)$`)
		if err != nil {
			errs = append(errs, fmt.Errorf("filter.%s: %w", key, err))
		}

		return re
	}

	f.include = compile("include_repos", rules.IncludeRepos)
	f.exclude = compile("exclude_repos", rules.ExcludeRepos)
	f.allowPrivate = compile("allow_private", rules.AllowPrivate)
	for i, pattern := range rules.IncludeBranches {
		if pattern == "" {
			errs = append(errs, fmt.Errorf("filter.include_branches[%d]: empty pattern", i))
			continue
		}
		f.branches = append(f.branches, compile(fmt.Sprintf("include_branches[%d]", i), pattern))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return f, nil
}

// Validate reports whether the rules compile.
func (r Rules) Validate() error {
	_, err := New(r)

	return err
}

// Decide applies the rules to a push of ref (e.g. "refs/heads/main") in r.
//
// Order: branch → topic → visibility → fork → include → exclude. The order
// does not change the outcome (every rule must pass); it determines which
// rule is reported, cheapest and most specific first.
func (f *Filter) Decide(r repo.Repository, ref string) Decision {
	switch {
	case !f.branchAllowed(ref):
		return reject("branch not in filter.include_branches")
	case f.rules.RequireTopic != "" && !slices.Contains(r.Topics, f.rules.RequireTopic):
		return reject(fmt.Sprintf("topic %q missing (filter.require_topic)", f.rules.RequireTopic))
	case f.rules.ExcludePrivate && r.Private &&
		(f.allowPrivate == nil || !f.allowPrivate.MatchString(r.FullName)):
		return reject("private repository (filter.exclude_private)")
	case f.rules.ExcludeForks && r.Fork:
		return reject("fork (filter.exclude_forks)")
	case f.include != nil && !f.include.MatchString(r.FullName):
		return reject("not matched by filter.include_repos")
	case f.exclude != nil && f.exclude.MatchString(r.FullName):
		return reject("matched by filter.exclude_repos")
	default:
		return Decision{Mirror: true}
	}
}

// Matches reports whether r passes all repository rules (branch rules are
// not considered).
func (f *Filter) Matches(r repo.Repository) bool {
	return f.Decide(r, "").Mirror
}

func (f *Filter) branchAllowed(ref string) bool {
	branch, isBranch := strings.CutPrefix(ref, branchRefPrefix)
	if !isBranch || len(f.branches) == 0 {
		return true
	}

	return slices.ContainsFunc(f.branches, func(re *regexp.Regexp) bool {
		return re.MatchString(branch)
	})
}

func reject(reason string) Decision {
	return Decision{Reason: reason}
}
