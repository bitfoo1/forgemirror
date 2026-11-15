package forgejo

import "time"

// SetFailureBackoff shortens or disables the failure backoff for tests.
func SetFailureBackoff(l *Lazy, d time.Duration) { l.failureBackoff = d }

// SetClock replaces the clock the failure backoff is measured with.
func SetClock(l *Lazy, now func() time.Time) { l.now = now }
