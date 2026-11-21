package status

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.bitfoo.de/forgemirror/internal/hold"
)

// ErrUnknownHold rejects stale or invented IDs without affecting other holds.
var ErrUnknownHold = errors.New("publication hold no longer exists")

func legacyPause(p Pause) Pause {
	p.ID = fmt.Sprintf("legacy-%x", sha256.Sum256([]byte(p.Repo+"\x00"+p.Target)))
	p.Scope = hold.Scope{Targets: []string{p.Target}, AllRepositories: p.Repo == ""}
	if p.Repo != "" {
		p.Scope.Repositories = []string{p.Repo}
	}
	return p
}

func validHoldID(id string) bool {
	return len(id) > 0 && len(id) <= 80 && strings.IndexFunc(id, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) < 0
}

func normalizePauses(record *controlRecord) error {
	seen := make(map[string]bool, len(record.Pauses))
	for i, p := range record.Pauses {
		if record.Version < controlVersion {
			if p.Target == "" {
				return errors.New("invalid persisted pause")
			}
			p = legacyPause(p)
		}
		if !validHoldID(p.ID) || seen[p.ID] || p.Scope.Validate() != nil {
			return errors.New("invalid persisted pause")
		}
		seen[p.ID] = true
		record.Pauses[i] = p
	}
	return nil
}

// AddHold publishes one complete scope only after the operation barrier and
// durable save succeed; overlapping holds intentionally retain separate IDs.
func (s *Store) AddHold(ctx context.Context, scope hold.Scope, reason string, until time.Time) (Pause, error) {
	scope = scope.Clone()
	if err := scope.Validate(); err != nil {
		return Pause{}, err
	}
	if len(reason) > hold.MaxReasonBytes || (!until.IsZero() && !until.After(s.now())) {
		return Pause{}, fmt.Errorf("%w: reason or expiry is invalid", hold.ErrInvalid)
	}
	clean, err := s.redactControlReason(reason)
	if err != nil {
		return Pause{}, err
	}
	p := Pause{ID: rand.Text(), Scope: scope, Reason: clean, Until: until}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if !until.IsZero() && !until.After(s.now()) {
		return Pause{}, hold.ErrInvalid
	}
	next := s.activePausesLocked()
	if len(next) >= hold.MaxHolds {
		return Pause{}, fmt.Errorf("%w: too many current holds; resume an existing hold first", hold.ErrInvalid)
	}
	if err = s.saveControls(ctx, append(next, p)); err != nil {
		return Pause{}, err
	}
	p.Scope = p.Scope.Clone()
	return p, nil
}

func (s *Store) activePausesLocked() []Pause {
	return slices.DeleteFunc(
		slices.Clone(s.pauses),
		func(p Pause) bool { return !p.Until.IsZero() && !s.now().Before(p.Until) },
	)
}

// Hold returns a detached scope for preflight checks before an ID-based resume.
func (s *Store) Hold(id string) (Pause, bool) {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	for _, p := range s.pauses {
		if p.ID == id {
			p.Scope = p.Scope.Clone()
			return p, true
		}
	}
	return Pause{}, false
}

// ResumeHold removes only this identity, returning its scope for a fresh sync.
func (s *Store) ResumeHold(ctx context.Context, id string) (Pause, error) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	index := slices.IndexFunc(s.pauses, func(p Pause) bool { return p.ID == id })
	if index < 0 {
		return Pause{}, ErrUnknownHold
	}
	p := s.pauses[index]
	next := slices.Delete(slices.Clone(s.pauses), index, index+1)
	if err := s.saveControls(ctx, next); err != nil {
		return Pause{}, err
	}
	p.Scope = p.Scope.Clone()
	return p, nil
}
