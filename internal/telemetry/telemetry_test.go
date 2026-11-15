package telemetry_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/telemetry"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func scrape(t *testing.T, m *telemetry.Metrics) string {
	t.Helper()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func TestMetrics(t *testing.T) {
	t.Parallel()

	m, err := telemetry.New("test")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if shutdownErr := m.Shutdown(t.Context()); shutdownErr != nil {
			t.Errorf("Shutdown() error = %v", shutdownErr)
		}
	})

	ctx := t.Context()
	m.RunStarted(ctx)
	m.RunStarted(ctx)
	m.RunFinished(ctx)
	m.TargetFinished(ctx, "github", 3*time.Second, nil)
	m.TargetFinished(ctx, "github", time.Second, nil)
	m.TargetFinished(ctx, "codeberg", 2*time.Second, errors.New("push failed"))
	m.ObserveWebhook(ctx, webhook.OutcomeAccepted)
	m.ObserveWebhook(ctx, webhook.OutcomeUnauthorized)
	m.ObserveWebhook(ctx, webhook.OutcomeUnauthorized)

	body := scrape(t, m)

	tests := []struct {
		name string
		want string
	}{
		{name: "success counter", want: `forge_mirror_operations_total{status="success",target="github"} 2`},
		{name: "failure counter", want: `forge_mirror_operations_total{status="failure",target="codeberg"} 1`},
		{name: "histogram count", want: `forge_mirror_duration_seconds_count{target="github"} 2`},
		{name: "histogram sum", want: `forge_mirror_duration_seconds_sum{target="github"} 4`},
		{name: "histogram bucket", want: `forge_mirror_duration_seconds_bucket{target="github",le="2.5"} 1`},
		{name: "webhook accepted", want: `forge_mirror_webhook_requests_total{outcome="accepted"} 1`},
		{name: "webhook unauthorized", want: `forge_mirror_webhook_requests_total{outcome="unauthorized"} 2`},
		{name: "active gauge", want: `forge_mirror_active_operations 1`},
		{name: "go runtime", want: `go_goroutines `},
		{name: "service", want: `service_name="forgemirror",service_version="test"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(body, tc.want) {
				t.Errorf("metrics missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// TestIndependentRegistries guards against package-level state: two
// instances must not see each other's measurements.
func TestIndependentRegistries(t *testing.T) {
	t.Parallel()

	a, err := telemetry.New("test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := telemetry.New("test")
	if err != nil {
		t.Fatal(err)
	}

	a.ObserveWebhook(t.Context(), webhook.OutcomeAccepted)

	if body := scrape(t, b); strings.Contains(body, "forge_mirror_webhook_requests_total") {
		t.Errorf("second instance sees first instance's metrics:\n%s", body)
	}
}

func TestSyncFinishedCounts(t *testing.T) {
	t.Parallel()

	m, err := telemetry.New("test")
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	m.SyncFinished(ctx, "github", "create", nil)
	m.SyncFinished(ctx, "github", "labels", errors.New("forbidden"))
	m.SyncFinished(ctx, "github", "labels", errors.New("forbidden"))

	body := scrape(t, m)

	tests := []struct {
		name string
		want string
	}{
		{
			name: "create success",
			want: `forgemirror_sync_operations_total{operation="create",status="success",target="github"} 1`,
		},
		{
			name: "labels failure",
			want: `forgemirror_sync_operations_total{operation="labels",status="failure",target="github"} 2`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(body, tc.want) {
				t.Errorf("metrics missing %q:\n%s", tc.want, body)
			}
		})
	}
}

func TestScheduledRunFinishedCounts(t *testing.T) {
	t.Parallel()

	m, err := telemetry.New("test")
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	m.ScheduledRunFinished(ctx, 3, nil)
	m.ScheduledRunFinished(ctx, 0, errors.New("forgejo down"))
	m.ScheduledRunFinished(ctx, 2, nil)

	body := scrape(t, m)

	tests := []struct {
		name string
		want string
	}{
		{name: "success", want: `forgemirror_scheduled_runs_total{status="success"} 2`},
		{name: "failure", want: `forgemirror_scheduled_runs_total{status="failure"} 1`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(body, tc.want) {
				t.Errorf("metrics missing %q:\n%s", tc.want, body)
			}
		})
	}
}
