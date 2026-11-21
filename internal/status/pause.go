package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/retry"
)

// ErrPaused distinguishes an operator hold from a failed transfer.
var ErrPaused = errors.New("publication paused")

// Pause is an independently resumable hold. Repo/Target preserve legacy controls.
type Pause struct {
	ID     string     `json:"id,omitempty"`
	Scope  hold.Scope `json:"scope"`
	Repo   string     `json:"repo"`
	Target string     `json:"target"`
	Reason string     `json:"reason"`
	Until  time.Time  `json:"until"`
}

const controlVersion = 4

type controlRecord struct {
	Version   int           `json:"version"`
	Pauses    []Pause       `json:"pauses"`
	Retries   []retry.Entry `json:"retries,omitempty"`
	Approvals []Approval    `json:"approvals,omitempty"`
}

// BeginMutation keeps pause acknowledgement behind operations already started.
// Callers release the permit at the end of one bounded remote operation.
func (s *Store) BeginMutation(repo, target string) (func(), error) {
	return s.BeginPublication(repo, target, "", false)
}

func (s *Store) pauseLocked(repo, target string) (Pause, bool) {
	for _, p := range s.pauses {
		if p.Scope.Matches(repo, target) && (p.Until.IsZero() || s.now().Before(p.Until)) {
			return p, true
		}
	}
	return Pause{}, false
}

// Paused returns the active hold, including a whole-target hold when applicable.
func (s *Store) Paused(repo, target string) (Pause, bool) {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	p, ok := s.pauseLocked(repo, target)
	p.Scope = p.Scope.Clone()
	return p, ok
}

// Pauses returns active holds for operator visibility.
func (s *Store) Pauses() []Pause {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	active := slices.DeleteFunc(
		slices.Clone(s.pauses),
		func(p Pause) bool { return !p.Until.IsZero() && !s.now().Before(p.Until) },
	)
	for i := range active {
		active[i].Scope = active[i].Scope.Clone()
	}
	return active
}

// Pause acknowledges only after active mutations finish and persistence succeeds.
func (s *Store) Pause(ctx context.Context, p Pause) error {
	var redactErr error
	p.Reason, redactErr = s.redactControlReason(p.Reason)
	if redactErr != nil {
		return redactErr
	}
	if p.Target == "" || (!p.Until.IsZero() && !p.Until.After(s.now())) {
		return errors.New("pause requires a target and a future expiry")
	}
	p = legacyPause(p)
	if err := p.Scope.Validate(); err != nil {
		return err
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	next := slices.DeleteFunc(
		s.activePausesLocked(),
		func(old Pause) bool { return old.Target != "" && old.Repo == p.Repo && old.Target == p.Target },
	)
	if len(next) >= hold.MaxHolds || (!p.Until.IsZero() && !p.Until.After(s.now())) {
		return hold.ErrInvalid
	}
	return s.saveControls(ctx, append(next, p))
}

// Resume removes only the requested scope; a broader hold still applies.
func (s *Store) Resume(ctx context.Context, repo, target string) error {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	next := slices.DeleteFunc(
		slices.Clone(s.pauses),
		func(p Pause) bool { return p.Target != "" && p.Repo == repo && p.Target == target },
	)
	return s.saveControls(ctx, next)
}

func (s *Store) redactControlReason(reason string) (string, error) {
	if s.redact == nil {
		return reason, nil
	}
	raw, err := json.Marshal(reason)
	if err != nil {
		return "", fmt.Errorf("encoding pause reason: %w", err)
	}
	raw, err = s.redact(raw)
	if err != nil {
		return "", fmt.Errorf("redacting pause reason: %w", err)
	}
	var clean string
	if err = json.Unmarshal(raw, &clean); err != nil {
		return "", fmt.Errorf("decoding pause reason: %w", err)
	}
	return clean, nil
}

func (s *Store) saveControls(ctx context.Context, pauses []Pause) error {
	data, err := json.Marshal(
		controlRecord{Version: controlVersion, Pauses: pauses, Retries: s.retries, Approvals: s.approvals},
	)
	if err != nil {
		return fmt.Errorf("encoding controls: %w", err)
	}
	// Functional identifiers and static reason codes are not credential copies.
	// Redact free-text pause reasons at admission, not hashes/state metadata.
	var record controlRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("decoding controls: %w", err)
	}
	if s.controls != nil {
		if err = s.controls.Save(ctx, "controls", data); err != nil {
			return errors.New("control change could not be saved; previous state retained")
		}
	}
	s.pauses = record.Pauses
	s.retries = record.Retries
	s.approvals = record.Approvals
	return nil
}

func (s *Store) restoreControls(ctx context.Context) error {
	if s.controls == nil {
		return nil
	}
	records, err := s.controls.Load(ctx)
	if err != nil {
		return errors.New("could not restore publication controls")
	}
	data, ok := records["controls"]
	if !ok {
		return nil
	}
	var record controlRecord
	if err = json.Unmarshal(data, &record); err != nil || record.Version < 1 || record.Version > controlVersion {
		return errors.New("invalid publication controls")
	}
	if !validApprovals(record.Approvals) {
		return errors.New("invalid persisted approvals")
	}
	if !validRetries(record.Retries) {
		return errors.New("invalid persisted retries")
	}
	if err = normalizePauses(&record); err != nil {
		return err
	}
	s.controlMu.Lock()
	s.pauses = record.Pauses
	s.retries = record.Retries
	s.approvals = record.Approvals
	s.controlMu.Unlock()
	return nil
}
