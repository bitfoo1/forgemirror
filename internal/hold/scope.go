// Package hold validates and matches bounded publication-control scopes.
package hold

import (
	"errors"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid rejects ambiguous or malformed scopes without echoing user input.
var ErrInvalid = errors.New(
	"invalid hold scope: choose targets and repositories; repository globs support only * and ?",
)

// MaxSelections bounds persisted scopes and form processing.
const MaxSelections = 64

// MaxHolds bounds independently resumable controls retained by the store.
const MaxHolds = 256

// MaxReasonBytes bounds free text before redaction or persistence.
const MaxReasonBytes = 256

// Scope keeps explicit all-flags distinct from an accidentally empty selection.
type Scope struct {
	AllTargets      bool     `json:"all_targets"`
	AllRepositories bool     `json:"all_repositories"`
	Targets         []string `json:"targets,omitempty"`
	Repositories    []string `json:"repositories,omitempty"`
}

// Clone keeps callers from mutating a persisted scope through returned slices.
func (s Scope) Clone() Scope {
	s.Targets = slices.Clone(s.Targets)
	s.Repositories = slices.Clone(s.Repositories)
	return s
}

// Validate checks syntax only; controllers separately enforce source admission.
func (s Scope) Validate() error {
	if s.AllTargets == (len(s.Targets) > 0) || s.AllRepositories == (len(s.Repositories) > 0) ||
		!validSelections(s.Targets) ||
		!validSelections(s.Repositories) {
		return ErrInvalid
	}
	for _, pattern := range s.Repositories {
		owner, name, ok := strings.Cut(pattern, "/")
		if !ok || owner == "" || name == "" || owner == "." || owner == ".." || name == "." || name == ".." ||
			strings.Contains(name, "/") ||
			strings.ContainsAny(pattern, `[]{}!\`) {
			return ErrInvalid
		}
		if _, err := path.Match(pattern, "owner/name"); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

func validSelections(values []string) bool {
	if len(values) > MaxSelections {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || len(value) > 256 || !utf8.ValidString(value) ||
			strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 ||
			seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

// Matches applies globs to owner/name components, including future admissions.
func (s Scope) Matches(repo, target string) bool {
	if !s.AllTargets && !slices.Contains(s.Targets, target) {
		return false
	}
	if s.AllRepositories {
		return true
	}
	return slices.ContainsFunc(
		s.Repositories,
		func(pattern string) bool { matched, err := path.Match(pattern, repo); return err == nil && matched },
	)
}
