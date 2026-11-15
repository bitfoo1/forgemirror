// Package server runs the HTTP listeners: routing, timeouts, health endpoints
// and graceful shutdown of in-flight requests.
//
// forgemirror serves three audiences on separate listeners: Forgejo
// (webhook), the cluster (ops: probes and metrics) and operators (web UI).
// Each can then be exposed or locked down as a whole, without path rules.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Timeouts protect the server from slow or idle clients holding connections
// open. Requests are small and answered immediately (mirroring runs in the
// background), so generous upper bounds are enough.
const (
	readTimeout  = 30 * time.Second
	writeTimeout = 30 * time.Second
	idleTimeout  = 120 * time.Second
)

// Options configures a [Server].
type Options struct {
	// Name identifies the listener in logs ("webhook", "ops", "web").
	Name              string
	Handler           http.Handler
	ReadHeaderTimeout time.Duration
	Logger            *slog.Logger
}

// Server serves one handler on one listener.
type Server struct {
	opts Options
}

// New returns a [Server].
func New(opts Options) *Server {
	return &Server{opts: opts}
}

// Serve accepts connections on ln until ctx is cancelled, then shuts down
// gracefully: it stops accepting and lets in-flight requests finish until
// shutdownCtx ends. shutdownCtx is passed in, rather than a timeout, so the
// caller can share one deadline between HTTP and background work. The
// listener is passed in (instead of an address) so callers and tests can bind
// to port 0 and learn the address.
func (s *Server) Serve(ctx, shutdownCtx context.Context, ln net.Listener) error {
	logger := s.opts.Logger.With(slog.String("server", s.opts.Name))
	srv := &http.Server{
		Handler:           s.opts.Handler,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		// Requests inherit ctx's values but not its cancellation: shutdown
		// cancels ctx, and in-flight requests must still be able to finish.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	logger.InfoContext(ctx, "http server listening", slog.String("address", ln.Addr().String()))

	select {
	case err := <-serveErr:
		return fmt.Errorf("%s server: %w", s.opts.Name, err)
	case <-ctx.Done():
	}

	logger.InfoContext(ctx, "http server shutting down")

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("%s server shutdown: %w", s.opts.Name, err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s server: %w", s.opts.Name, err)
	}

	return nil
}

// Health tracks readiness for the ops listener.
type Health struct {
	ready atomic.Bool
}

// SetReady sets the readiness reported by /readyz.
func (h *Health) SetReady(ready bool) { h.ready.Store(ready) }

// OpsHandler returns the routing table of the ops listener.
//
//   - GET /healthz: liveness; 200 while the process can serve HTTP at all
//   - GET /readyz: readiness; 503 before serving and once shutdown began,
//     so Kubernetes stops routing new webhooks to a terminating pod
//   - GET /metrics: Prometheus metrics (if metrics is not nil)
func OpsHandler(health *Health, metrics http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !health.ready.Load() {
			writeText(w, http.StatusServiceUnavailable, "not ready")

			return
		}
		writeText(w, http.StatusOK, "ready")
	})
	if metrics != nil {
		mux.Handle("GET /metrics", metrics)
	}

	return mux
}

// WebhookHandler returns the routing table of the webhook listener:
// POST /webhook only. A nil webhook leaves every path at 404, as when no
// webhook secret is configured; the listener still runs, so the set of open
// ports does not depend on the configuration.
func WebhookHandler(webhook http.Handler) http.Handler {
	mux := http.NewServeMux()
	if webhook != nil {
		mux.Handle("POST /webhook", webhook)
	}

	return mux
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body+"\n")
}
