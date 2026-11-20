// Package retry classifies failures without coupling scheduling to tool diagnostics.
package retry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kinds preserve the operation authorized by the original event.
const (
	KindSync   = "sync"
	KindRepair = "repair"
	KindDelete = "delete"
)

// Entry is durable scheduling metadata, never an executable saved plan or URL.
type Entry struct {
	AttemptID uint64    `json:"attempt_id,omitempty"`
	Repo      string    `json:"repo"`
	Target    string    `json:"target"`
	Kind      string    `json:"kind"`
	Automatic bool      `json:"automatic"`
	Attempts  int       `json:"attempts"`
	Next      time.Time `json:"next"`
	State     string    `json:"state"`
}

type failure struct {
	cause     error
	retryable bool
	after     time.Time
	message   string
}

func (f *failure) Error() string {
	if f.message != "" {
		return f.message
	}
	return f.cause.Error()
}
func (f *failure) Unwrap() error { return f.cause }

// Retryable is conservative: unknown failures require intervention.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if f, ok := errors.AsType[*failure](err); ok {
		return f.retryable
	}
	if network, ok := errors.AsType[net.Error](err); ok && network.Timeout() {
		return true
	}
	_, ok := errors.AsType[*net.OpError](err)
	return ok
}

// NotBefore returns a server-requested lower bound for the next attempt.
//
//nolint:errorlint // Visit every unwrap-tree node, not only the first matching cause.
func NotBefore(err error) time.Time {
	var after time.Time
	if f, ok := err.(*failure); ok {
		after = f.after
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, cause := range wrapped.Unwrap() {
			if bound := NotBefore(cause); bound.After(after) {
				after = bound
			}
		}
	case interface{ Unwrap() error }:
		if bound := NotBefore(wrapped.Unwrap()); bound.After(after) {
			after = bound
		}
	}
	return after
}

// HTTP preserves status/rate-limit metadata before SDK wrappers discard responses.
func HTTP(err error, response *http.Response, now time.Time) error {
	if err == nil || response == nil {
		return err
	}
	after := time.Time{}
	header := response.Header.Get("Retry-After")
	if seconds, parseErr := strconv.ParseInt(header, 10, 32); parseErr == nil && seconds >= 0 {
		after = now.Add(time.Duration(seconds) * time.Second)
	} else if date, dateErr := http.ParseTime(header); dateErr == nil {
		after = date
	}
	limited := response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && (header != "" || response.Header.Get("X-Ratelimit-Remaining") == "0"))
	if reset, parseErr := strconv.ParseInt(
		response.Header.Get("X-Ratelimit-Reset"),
		10,
		64,
	); limited &&
		parseErr == nil {
		if resetAt := time.Unix(reset, 0); resetAt.After(after) {
			after = resetAt
		}
	}
	return &failure{
		cause:     err,
		retryable: limited || response.StatusCode >= http.StatusInternalServerError,
		after:     after,
	}
}

// Command recognizes only known transient connection/status diagnostics.
func Command(err error, diagnostics string) error {
	text := strings.ToLower(diagnostics)
	for _, marker := range []string{"could not resolve host", "failed to connect", "connection timed out", "connection reset", "returned error: 429", "returned error: 500", "returned error: 502", "returned error: 503", "returned error: 504"} {
		if strings.Contains(text, marker) {
			return &failure{cause: err, retryable: true}
		}
	}
	return err
}

// WithMessage hides untrusted tool output without discarding classification.
func WithMessage(err error, message string) error {
	return &failure{cause: err, retryable: Retryable(err), after: NotBefore(err), message: message}
}

// Delay returns capped exponential backoff with additive jitter in [0,20%].
func Delay(attempt int, jitter float64) time.Duration {
	const base = 30 * time.Second
	const capDelay = 15 * time.Minute
	const multiplier = 2
	const jitterDivisor = 5
	delay := base
	for i := 1; i < attempt && delay < capDelay; i++ {
		delay = min(delay*multiplier, capDelay)
	}
	return min(delay+time.Duration(float64(delay)*max(0, min(jitter, 1))/jitterDivisor), capDelay)
}
