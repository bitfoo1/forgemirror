package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/retry"
)

// ErrPaused distinguishes an operator hold from a failed transfer.
var ErrPaused = errors.New("publication paused")

// Pause holds a whole target (empty Repo) or one repository on that target.
type Pause struct {
	Repo   string    `json:"repo"`
	Target string    `json:"target"`
	Reason string    `json:"reason"`
	Until  time.Time `json:"until"`
}

const controlVersion = 2

type controlRecord struct {
	Version int           `json:"version"`
	Pauses  []Pause       `json:"pauses"`
	Retries []retry.Entry `json:"retries,omitempty"`
}

// BeginMutation keeps pause acknowledgement behind operations already started.
// Callers release the permit at the end of one bounded remote operation.
func (s *Store) BeginMutation(repo, target string) (func(), error) {
	s.controlMu.RLock()
	if _, ok := s.pauseLocked(repo, target); ok {
		s.controlMu.RUnlock()
		return nil, ErrPaused
	}
	return s.controlMu.RUnlock, nil
}

func (s *Store) pauseLocked(repo, target string) (Pause, bool) {
	for _, p := range s.pauses {
		if p.Target == target && (p.Repo == "" || p.Repo == repo) && (p.Until.IsZero() || s.now().Before(p.Until)) {
			return p, true
		}
	}
	return Pause{}, false
}

// Paused returns the active hold, including a whole-target hold when applicable.
func (s *Store) Paused(repo, target string) (Pause, bool) {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	return s.pauseLocked(repo, target)
}

// Pauses returns active holds for operator visibility.
func (s *Store) Pauses() []Pause {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	return slices.DeleteFunc(
		slices.Clone(s.pauses),
		func(p Pause) bool { return !p.Until.IsZero() && !s.now().Before(p.Until) },
	)
}

// Pause acknowledges only after active mutations finish and persistence succeeds.
func (s *Store) Pause(ctx context.Context, p Pause) error {
	if p.Target == "" || (!p.Until.IsZero() && !p.Until.After(s.now())) {
		return errors.New("pause requires a target and a future expiry")
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	next := slices.DeleteFunc(
		slices.Clone(s.pauses),
		func(old Pause) bool { return old.Repo == p.Repo && old.Target == p.Target },
	)
	return s.saveControls(ctx, append(next, p))
}

// Resume removes only the requested scope; a broader hold still applies.
func (s *Store) Resume(ctx context.Context, repo, target string) error {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	next := slices.DeleteFunc(
		slices.Clone(s.pauses),
		func(p Pause) bool { return p.Repo == repo && p.Target == target },
	)
	return s.saveControls(ctx, next)
}

func (s *Store) saveControls(ctx context.Context, pauses []Pause) error {
	data, err := json.Marshal(controlRecord{Version: controlVersion, Pauses: pauses, Retries: s.retries})
	if err != nil {
		return fmt.Errorf("encoding controls: %w", err)
	}
	if s.redact != nil {
		data, err = s.redact(data)
		if err != nil {
			return fmt.Errorf("redacting controls: %w", err)
		}
	}
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
	if data, ok := records["controls"]; ok {
		var record controlRecord
		if err = json.Unmarshal(
			data,
			&record,
		); err != nil ||
			(record.Version != 1 && record.Version != controlVersion) {
			return errors.New("invalid publication controls")
		}
		if !validRetries(record.Retries) {
			return errors.New("invalid persisted retries")
		}
		for _, p := range record.Pauses {
			if p.Target == "" {
				return errors.New("invalid persisted pause")
			}
		}
		s.controlMu.Lock()
		s.pauses = record.Pauses
		s.retries = record.Retries
		s.controlMu.Unlock()
	}
	return nil
}
