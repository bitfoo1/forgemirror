// Package telemetry records forgemirror's metrics with OpenTelemetry and
// serves them in the Prometheus exposition format.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/schedule"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const (
	meterName   = "go.bitfoo.de/forgemirror"
	serviceName = "forgemirror"
)

// Status label values of forge_mirror_operations_total,
// forgemirror_sync_operations_total and forgemirror_scheduled_runs_total.
const (
	statusSuccess = "success"
	statusFailure = "failure"
)

// durationBuckets fit git clone/push times: sub-second for tiny repositories
// up to the default 10 minute mirror timeout. The OTel default buckets are
// tuned for milliseconds and would put nearly every push into one bucket.
func durationBuckets() []float64 {
	return []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}
}

// Metrics implements [dispatch.Observer], [webhook.Observer] and
// [schedule.Observer].
type Metrics struct {
	provider *sdkmetric.MeterProvider
	registry *prometheus.Registry

	operations metric.Int64Counter
	duration   metric.Float64Histogram
	webhooks   metric.Int64Counter
	active     metric.Int64UpDownCounter
	syncOps    metric.Int64Counter
	scheduled  metric.Int64Counter
}

var (
	_ dispatch.Observer = (*Metrics)(nil)
	_ webhook.Observer  = (*Metrics)(nil)
	_ schedule.Observer = (*Metrics)(nil)
)

// New returns [Metrics] backed by their own Prometheus registry. A private
// registry (instead of prometheus.DefaultRegisterer) keeps tests independent
// and avoids package-level state. serviceVersion is exported in target_info.
func New(serviceVersion string) (*Metrics, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, fmt.Errorf("registering go collector: %w", err)
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, fmt.Errorf("registering process collector: %w", err)
	}

	exporter, err := otelprom.New(otelprom.WithRegisterer(registry), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, fmt.Errorf("creating prometheus exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", serviceVersion),
		)),
	)
	meter := provider.Meter(meterName)

	m := &Metrics{provider: provider, registry: registry}
	if instErr := m.instruments(meter); instErr != nil {
		return nil, errors.Join(instErr, provider.Shutdown(context.Background()))
	}

	return m, nil
}

func (m *Metrics) instruments(meter metric.Meter) error {
	var errs [6]error
	// The exporter appends _total to counters and _seconds for unit "s".
	m.operations, errs[0] = meter.Int64Counter("forge_mirror_operations",
		metric.WithDescription("Mirror operations per target, by status."))
	m.duration, errs[1] = meter.Float64Histogram("forge_mirror_duration",
		metric.WithDescription("Duration of mirror operations per target."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(durationBuckets()...))
	m.webhooks, errs[2] = meter.Int64Counter("forge_mirror_webhook_requests",
		metric.WithDescription("Webhook requests, by outcome."))
	m.active, errs[3] = meter.Int64UpDownCounter("forge_mirror_active_operations",
		metric.WithDescription("Mirror runs currently in progress."))
	m.syncOps, errs[4] = meter.Int64Counter("forgemirror_sync_operations",
		metric.WithDescription("Forge API operations (create, settings, labels, delete) per target, by status."))
	m.scheduled, errs[5] = meter.Int64Counter(
		"forgemirror_scheduled_runs",
		metric.WithDescription(
			"Scheduled sync runs, by status; a failure means the source repositories could not be listed.",
		),
	)

	if err := errors.Join(errs[:]...); err != nil {
		return fmt.Errorf("creating instruments: %w", err)
	}

	return nil
}

// Handler serves the metrics in the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Shutdown releases the meter provider.
func (m *Metrics) Shutdown(ctx context.Context) error {
	if err := m.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down meter provider: %w", err)
	}

	return nil
}

// RunStarted implements [dispatch.Observer].
func (m *Metrics) RunStarted(ctx context.Context) {
	m.active.Add(ctx, 1)
}

// RunFinished implements [dispatch.Observer].
func (m *Metrics) RunFinished(ctx context.Context) {
	m.active.Add(ctx, -1)
}

// TargetFinished implements [dispatch.Observer].
func (m *Metrics) TargetFinished(ctx context.Context, target string, duration time.Duration, err error) {
	targetAttr := attribute.String("target", target)

	m.operations.Add(ctx, 1, metric.WithAttributes(targetAttr, attribute.String("status", status(err))))
	m.duration.Record(ctx, duration.Seconds(), metric.WithAttributes(targetAttr))
}

// SyncFinished implements [dispatch.Observer].
func (m *Metrics) SyncFinished(ctx context.Context, target, operation string, err error) {
	m.syncOps.Add(ctx, 1, metric.WithAttributes(
		attribute.String("target", target),
		attribute.String("operation", operation),
		attribute.String("status", status(err)),
	))
}

// ScheduledRunFinished implements [schedule.Observer].
func (m *Metrics) ScheduledRunFinished(ctx context.Context, _ int, err error) {
	m.scheduled.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status(err))))
}

// ObserveWebhook implements [webhook.Observer].
func (m *Metrics) ObserveWebhook(ctx context.Context, outcome webhook.Outcome) {
	m.webhooks.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", string(outcome))))
}

func status(err error) string {
	if err != nil {
		return statusFailure
	}

	return statusSuccess
}
