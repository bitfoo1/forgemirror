package status

import (
	"slices"
	"time"
)

// LastSuccessfulSync uses completed per-target evidence, not the current ref map.
// A zero result means no success is retained, not that publication never happened.
func (s *Store) LastSuccessfulSync(repo, target string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return time.Time{}
	}
	s.pruneHistory(r)
	for _, attempt := range slices.Backward(r.history) {
		if attempt.Status == attemptInterrupted || attempt.Status == attemptRunning {
			continue
		}
		for _, result := range attempt.Targets {
			if result.Name == target && successfulTarget(result) {
				return attempt.Finished
			}
		}
		if successfulNoOp(attempt, target) {
			return attempt.Finished
		}
	}
	return time.Time{}
}

func successfulNoOp(attempt Attempt, target string) bool {
	if attempt.Status != "succeeded" || attempt.Error != "" {
		return false
	}
	for _, planned := range attempt.Plan.Targets {
		if planned.Name == target && planned.Error == "" && !planned.ApprovalRequired && planned.Changes() == 0 {
			return true
		}
	}
	return false
}

func successfulTarget(target Target) bool {
	return target.Error == "" && (!target.LastRun.IsZero() || len(target.Operations) > 0) &&
		!slices.ContainsFunc(target.Operations, func(op Operation) bool { return op.Error != "" })
}
