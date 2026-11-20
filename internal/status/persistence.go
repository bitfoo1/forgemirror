package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
)

// Persistence stores one atomic record per repository without depending on its representation.
type Persistence interface {
	Load(context.Context) (map[string][]byte, error)
	Save(context.Context, string, []byte) error
}

// Attempt retains the intended changes and only the outcomes of this application.
// A published attempt is copied so later refreshes cannot alter its results.
type Attempt struct {
	Plan     plan.Repo `json:"plan"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Status   string    `json:"status"`
	Error    string    `json:"error"`
	Targets  []Target  `json:"targets"`
}

const (
	persistTimeout     = 5 * time.Second
	attemptInterrupted = "interrupted"
)

type record struct {
	Version int  `json:"version"`
	Repo    Repo `json:"repo"`
}

func cloneAttempt(a *Attempt) *Attempt {
	if a == nil {
		return nil
	}
	cloned := *a
	cloned.Targets = cloneTargets(a.Targets)
	return &cloned
}

func cloneTargets(targets []Target) []Target {
	out := slices.Clone(targets)
	for i := range out {
		out[i].Operations = slices.Clone(out[i].Operations)
	}
	return out
}

func (a *Attempt) target(name string) *Target {
	for i := range a.Targets {
		if a.Targets[i].Name == name {
			return &a.Targets[i]
		}
	}
	a.Targets = append(a.Targets, Target{Name: name})
	return &a.Targets[len(a.Targets)-1]
}

func (a *Attempt) recordTarget(t Target) {
	target := a.target(t.Name)
	ops := target.Operations
	*target = t
	target.Operations = ops
}

func (a *Attempt) recordOperation(name string, op Operation) {
	target := a.target(name)
	for i := range target.Operations {
		if target.Operations[i].Name == op.Name {
			target.Operations[i] = op
			return
		}
	}
	target.Operations = append(target.Operations, op)
}

func attemptStatus(a *Attempt) string {
	failed, succeeded := a.Plan.Failed() || a.Error != "", false
	for _, t := range a.Targets {
		if !t.LastRun.IsZero() {
			failed = failed || t.Error != ""
			succeeded = succeeded || t.Error == ""
		}
		for _, op := range t.Operations {
			failed = failed || op.Error != ""
			succeeded = succeeded || op.Error == ""
		}
	}
	switch {
	case failed && succeeded:
		return "partially failed"
	case failed:
		return "failed"
	default:
		return "succeeded"
	}
}

// Checkpoint is the dispatcher's pre-application durability barrier. A failed
// write blocks remote changes; void observer callbacks alone cannot do that.
func (s *Store) Checkpoint(ctx context.Context, p plan.Repo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.persist(ctx, p.Name); err != nil {
		if a := s.repo(p.Name).attempt; a != nil {
			a.Error = "State could not be saved; sync was not applied."
		}
		return err
	}
	return nil
}

func (s *Store) persist(ctx context.Context, name string) error {
	if s.persistence == nil {
		return nil
	}
	r := s.repo(name)
	targets := make([]Target, 0, len(r.targets))
	for _, t := range r.targets {
		targets = append(targets, *t)
	}
	data, err := json.Marshal(record{Version: 1, Repo: Repo{
		Name: name, Plan: r.plan, Current: r.current, LastAttempt: r.attempt, Stale: true,
		LastStarted: r.lastStarted, LastFinished: r.lastFinished, Targets: targets,
	}})
	if err == nil && s.redact != nil {
		data, err = s.redact(data)
	}
	if err == nil {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
		defer cancel()
		err = s.persistence.Save(writeCtx, name, data)
	}
	if err != nil {
		r.stale = true
		s.persistErr = "Persistent state could not be saved; new syncs require a successful checkpoint."
		if s.onError != nil {
			s.onError()
		}
		return fmt.Errorf("saving persistent status: %w", err)
	}
	s.persistErr = ""
	return nil
}

// Restore loads history, never running work. Last comparisons must be verified
// against the current configuration before they can be considered authoritative.
func (s *Store) Restore(ctx context.Context) error {
	if s.persistence == nil {
		return nil
	}
	records, err := s.persistence.Load(ctx)
	if err != nil {
		return fmt.Errorf("loading persistent status: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, data := range records {
		var saved record
		if err = json.Unmarshal(data, &saved); err != nil {
			return errors.New("invalid persistent status record")
		}
		if saved.Version != 1 || saved.Repo.Name != name {
			return errors.New("unsupported persistent status record")
		}
		r := s.repo(name)
		r.plan, r.current, r.attempt, r.stale = saved.Repo.Plan, saved.Repo.Current, saved.Repo.LastAttempt, true
		r.lastStarted, r.lastFinished = saved.Repo.LastStarted, saved.Repo.LastFinished
		for _, t := range saved.Repo.Targets {
			r.targets[t.Name] = &t
		}
		if r.attempt != nil && r.attempt.Status == "running" {
			r.attempt.Status = attemptInterrupted
			r.attempt.Finished = s.now()
			if err = s.persist(ctx, name); err != nil {
				return err
			}
		}
	}
	return nil
}
