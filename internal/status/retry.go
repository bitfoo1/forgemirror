package status

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/retry"
)

// Retries returns independent durable scheduling metadata for status and dispatch.
func (s *Store) Retries() []retry.Entry {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	return slices.Clone(s.retries)
}

// RecordRetry records one target outcome after all of its operations finish.
func (s *Store) RecordRetry(ctx context.Context, repo, target, kind string, automatic bool, result error) error {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	const maxRetries = 5
	if errors.Is(result, context.Canceled) {
		return nil
	}
	previous := s.retries
	index := slices.IndexFunc(previous, func(e retry.Entry) bool { return e.Repo == repo && e.Target == target })
	entry := retry.Entry{Repo: repo, Target: target, Kind: kind, Automatic: automatic}
	s.mu.Lock()
	if r, ok := s.repos[repo]; ok && r.attempt != nil {
		entry.AttemptID = r.attempt.ID
	}
	s.mu.Unlock()
	if index >= 0 {
		// Object-only repair must not cancel an outstanding publication retry.
		if kind == retry.KindRepair && previous[index].Kind != retry.KindRepair {
			return nil
		}
		entry.Attempts = previous[index].Attempts
	}
	next := slices.DeleteFunc(
		slices.Clone(previous),
		func(e retry.Entry) bool { return e.Repo == repo && e.Target == target },
	)
	if result != nil {
		entry.State = "needs intervention"
		switch {
		case errors.Is(result, ErrPaused):
			entry.State = "paused"
			entry.Next = s.now().Add(retry.Delay(1, 0))
		case retry.Retryable(result):
			entry.Attempts = min(entry.Attempts+1, maxRetries+1)
			entry.State = "waiting"
			if entry.Attempts > maxRetries {
				entry.State = "exhausted"
			} else {
				jitter := rand.Float64() //nolint:gosec // Scheduling jitter is not security-sensitive.
				entry.Next = maxTime(s.now().Add(retry.Delay(entry.Attempts, jitter)), retry.NotBefore(result))
			}
		}
		next = append(next, entry)
	}
	s.retries = next
	if err := s.saveControls(ctx, s.pauses); err != nil {
		s.retries = previous
		return err
	}
	return nil
}

func validRetries(entries []retry.Entry) bool {
	const maxFailures = 6
	seen := make(map[[2]string]bool, len(entries))
	for _, e := range entries {
		key := [2]string{e.Repo, e.Target}
		if e.Repo == "" || e.Target == "" || e.Attempts < 0 || e.Attempts > maxFailures || seen[key] {
			return false
		}
		if !slices.Contains([]string{retry.KindSync, retry.KindRepair, retry.KindDelete}, e.Kind) ||
			!slices.Contains([]string{"waiting", "paused", "exhausted", "needs intervention"}, e.State) {
			return false
		}
		seen[key] = true
	}
	return true
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
