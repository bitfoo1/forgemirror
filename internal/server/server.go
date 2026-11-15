// Package server runs the HTTP server: routing, timeouts, health endpoints
// and graceful shutdown of in-flight requests.
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
// open. Webhook requests are small and answered immediately (mirroring runs
// in the background), so generous upper bounds are enough.
const (
	readTimeout  = 30 * time.Second
	writeTimeout = 30 * time.Second
	idleTimeout  = 120 * time.Second
)

// Options configures a [Server].
type Options struct {
	ReadHeaderTimeout time.Duration
	// Webhook serves POST /webhook; nil disables the endpoint (404), as when
	// no webhook secret is configured.
	Webhook http.Handler
	// Metrics serves GET /metrics; nil disables the endpoint.
	Metrics http.Handler
	Logger  *slog.Logger
}

// Server is the forgemirror HTTP server.
type Server struct {
	opts  Options
	ready atomic.Bool
}

// New returns a [Server].
func New(opts Options) *Server {
	return &Server{opts: opts}
}

// Handler returns the routing table.
//
//   - POST /webhook: Forgejo push and repository events (if configured)
//   - GET /healthz: liveness; 200 while the process can serve HTTP at all
//   - GET /readyz: readiness; 503 before serving and once shutdown began,
//     so Kubernetes stops routing new webhooks to a terminating pod
//   - GET /metrics: Prometheus metrics (if configured)
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.opts.Webhook != nil {
		mux.Handle("POST /webhook", s.opts.Webhook)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.ready.Load() {
			writeText(w, http.StatusServiceUnavailable, "not ready")

			return
		}
		writeText(w, http.StatusOK, "ready")
	})
	if s.opts.Metrics != nil {
		mux.Handle("GET /metrics", s.opts.Metrics)
	}

	return mux
}

// Serve accepts connections on ln until ctx is cancelled, then shuts down
// gracefully: it stops accepting and lets in-flight requests finish until
// shutdownCtx ends. shutdownCtx is passed in, rather than a timeout, so the
// caller can share one deadline between HTTP and background work. The
// listener is passed in (instead of an address) so callers and tests can bind
// to port 0 and learn the address.
func (s *Server) Serve(ctx, shutdownCtx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(s.opts.Logger.Handler(), slog.LevelWarn),
		// Requests inherit ctx's values but not its cancellation: shutdown
		// cancels ctx, and in-flight requests must still be able to finish.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	s.ready.Store(true)
	s.opts.Logger.InfoContext(ctx, "http server listening", slog.String("address", ln.Addr().String()))

	select {
	case err := <-serveErr:
		s.ready.Store(false)

		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	s.ready.Store(false)
	s.opts.Logger.InfoContext(ctx, "http server shutting down")

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	return nil
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body+"\n")
}
