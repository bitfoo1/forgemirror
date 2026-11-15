package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/server"
)

func TestOpsRoutes(t *testing.T) {
	t.Parallel()

	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "metric 1\n")
	})

	tests := []struct {
		name       string
		metrics    http.Handler
		ready      bool
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "healthz", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantBody: "ok"},
		{
			name: "readyz before serving", method: http.MethodGet, path: "/readyz",
			wantStatus: http.StatusServiceUnavailable, wantBody: "not ready",
		},
		{
			name: "readyz when ready", ready: true, method: http.MethodGet, path: "/readyz",
			wantStatus: http.StatusOK, wantBody: "ready",
		},
		{
			name: "metrics", metrics: metrics, method: http.MethodGet, path: "/metrics",
			wantStatus: http.StatusOK, wantBody: "metric 1",
		},
		{name: "metrics disabled", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusNotFound},
		{name: "no webhook on ops", method: http.MethodPost, path: "/webhook", wantStatus: http.StatusNotFound},
		{name: "unknown", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			health := &server.Health{}
			health.SetReady(tc.ready)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			server.OpsHandler(health, tc.metrics).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestWebhookRoutes(t *testing.T) {
	t.Parallel()

	accept := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	tests := []struct {
		name       string
		webhook    http.Handler
		method     string
		path       string
		wantStatus int
	}{
		{name: "webhook", webhook: accept, method: http.MethodPost, path: "/webhook", wantStatus: http.StatusAccepted},
		{
			name: "webhook GET not allowed", webhook: accept, method: http.MethodGet, path: "/webhook",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{name: "disabled without secret", method: http.MethodPost, path: "/webhook", wantStatus: http.StatusNotFound},
		// Probes and metrics must not be reachable through a webhook ingress.
		{
			name:       "no healthz",
			webhook:    accept,
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "no metrics",
			webhook:    accept,
			method:     http.MethodGet,
			path:       "/metrics",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			server.WebhookHandler(tc.webhook).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func newServer(handler http.Handler) *server.Server {
	return server.New(server.Options{
		Name:              "test",
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
		Logger:            slog.New(slog.DiscardHandler),
	})
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, strings.TrimSpace(string(body))
}

func TestServeLifecycle(t *testing.T) {
	t.Parallel()

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	srv := newServer(server.OpsHandler(&server.Health{}, nil))
	done := make(chan error, 1)
	shutdownCtx, cancelShutdown := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelShutdown()
	go func() { done <- srv.Serve(ctx, shutdownCtx, ln) }()

	if status, body := get(t, base+"/healthz"); status != http.StatusOK || body != "ok" {
		t.Errorf("/healthz = %d %q", status, body)
	}

	cancel()
	select {
	case serveErr := <-done:
		if serveErr != nil {
			t.Errorf("Serve() error = %v, want nil after graceful shutdown", serveErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not return after cancellation")
	}
}

func TestServeListenerError(t *testing.T) {
	t.Parallel()

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // Serve fails immediately on a closed listener.

	serveErr := newServer(http.NotFoundHandler()).Serve(t.Context(), t.Context(), ln)
	if serveErr == nil || !strings.Contains(serveErr.Error(), "test server") {
		t.Errorf("Serve() on a closed listener: error = %v, want a named error", serveErr)
	}
}
