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

func newServer(metrics http.Handler) *server.Server {
	return server.New(server.Options{
		ReadHeaderTimeout: time.Second,
		Webhook: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}),
		Metrics: metrics,
		Logger:  slog.New(slog.DiscardHandler),
	})
}

func TestRoutes(t *testing.T) {
	t.Parallel()

	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "metric 1\n")
	})

	tests := []struct {
		name       string
		metrics    http.Handler
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "webhook", method: http.MethodPost, path: "/webhook", wantStatus: http.StatusAccepted},
		{
			name:       "webhook GET not allowed",
			method:     http.MethodGet,
			path:       "/webhook",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{name: "healthz", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantBody: "ok"},
		{
			name: "readyz before serving", method: http.MethodGet, path: "/readyz",
			wantStatus: http.StatusServiceUnavailable, wantBody: "not ready",
		},
		{
			name: "metrics", metrics: metrics, method: http.MethodGet, path: "/metrics",
			wantStatus: http.StatusOK, wantBody: "metric 1",
		},
		{name: "metrics disabled", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusNotFound},
		{name: "unknown", method: http.MethodGet, path: "/nope", wantStatus: http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			newServer(tc.metrics).Handler().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
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
	srv := newServer(nil)
	done := make(chan error, 1)
	shutdownCtx, cancelShutdown := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancelShutdown()
	go func() { done <- srv.Serve(ctx, shutdownCtx, ln) }()

	// Readiness flips to true once Serve runs.
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := get(t, base+"/readyz")
		if status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz never became ready, last status %d", status)
		}
		time.Sleep(10 * time.Millisecond)
	}

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

	if serveErr := newServer(nil).Serve(t.Context(), t.Context(), ln); serveErr == nil {
		t.Error("Serve() on a closed listener: error = nil, want error")
	}
}
