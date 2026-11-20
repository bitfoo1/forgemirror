package status

import (
	"slices"
)

func (s *Store) pruneHistory(r *repoState) {
	cutoff := s.now().Add(-s.historyRetention)
	r.history = slices.DeleteFunc(
		r.history,
		func(a Attempt) bool { return !a.Finished.IsZero() && a.Finished.Before(cutoff) },
	)
	if len(r.history) > s.historyLimit {
		r.history = slices.Clone(r.history[len(r.history)-s.historyLimit:])
	}
}

func (s *Store) retainAttempt(r *repoState) {
	if r.attempt == nil {
		return
	}
	copyOf := *cloneAttempt(r.attempt)
	index := slices.IndexFunc(r.history, func(a Attempt) bool { return a.ID == copyOf.ID })
	if index >= 0 {
		r.history[index] = copyOf
	} else {
		r.history = append(r.history, copyOf)
	}
	s.pruneHistory(r)
}

func (s *Store) restoreHistory(r *repoState) {
	for _, a := range r.history {
		r.sequence = max(r.sequence, a.ID)
	}
	if r.attempt != nil {
		if r.attempt.ID == 0 {
			r.sequence++
			r.attempt.ID = r.sequence
		}
		r.sequence = max(r.sequence, r.attempt.ID)
		if r.attempt.Status != attemptRunning {
			s.retainAttempt(r)
		}
	}
	s.pruneHistory(r)
}

// History returns a bounded newest-first page and its current retained total.
// Like plan snapshots, plan values inside attempts must not be modified.
func (s *Store) History(repo string, offset, limit int) ([]Attempt, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return nil, 0
	}
	s.pruneHistory(r)
	total := len(r.history)
	if offset < 0 || limit <= 0 || offset >= total {
		return nil, total
	}
	count := min(limit, total-offset)
	page := make([]Attempt, 0, count)
	for i := range count {
		a := r.history[total-1-offset-i]
		page = append(page, *cloneAttempt(&a))
	}
	return page, total
}
