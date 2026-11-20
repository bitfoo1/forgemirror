package retry_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/retry"
)

func TestHTTPFailurePolicy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		code  int
		retry bool
	}{
		{http.StatusTooManyRequests, true}, {http.StatusServiceUnavailable, true}, {http.StatusBadRequest, false}, {http.StatusUnauthorized, false}, {http.StatusForbidden, true},
	} {
		err := retry.HTTP(
			errors.New("request failed"),
			&http.Response{StatusCode: tt.code, Header: http.Header{"Retry-After": []string{"120"}}},
			now,
		)
		if got := retry.Retryable(err); got != tt.retry {
			t.Errorf("status %d retryable=%v", tt.code, got)
		}
		if tt.retry && retry.NotBefore(err) != now.Add(2*time.Minute) {
			t.Error("Retry-After lost")
		}
	}
}

func TestJoinedFailuresRespectLatestRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	first := retry.HTTP(
		errors.New("unavailable"),
		&http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Retry-After": []string{"120"}}},
		now,
	)
	second := retry.HTTP(
		errors.New("limited"),
		&http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"300"}}},
		now,
	)
	if got := retry.NotBefore(errors.Join(first, second)); got != now.Add(5*time.Minute) {
		t.Fatalf("retry bound=%s", got)
	}
}

func TestCommandClassificationAndRedaction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		text string
		want bool
	}{
		{"fatal: Could not resolve host: example.org", true},
		{"returned error: 503", true},
		{"authentication failed", false},
		{"invalid refspec", false},
	} {
		err := retry.WithMessage(retry.Command(errors.New(tt.text), tt.text), "transfer failed")
		if err.Error() != "transfer failed" || retry.Retryable(err) != tt.want {
			t.Errorf("classification=%v error=%s", retry.Retryable(err), err)
		}
	}
	if retry.Retryable(nil) {
		t.Fatal("nil is retryable")
	}
}

func TestBackoffIsBounded(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		attempt int
		want    time.Duration
	}{{1, 30 * time.Second}, {2, time.Minute}, {5, 8 * time.Minute}, {10, 15 * time.Minute}} {
		if got := retry.Delay(tt.attempt, 0); got != tt.want {
			t.Errorf("attempt %d delay=%s", tt.attempt, got)
		}
	}
}
