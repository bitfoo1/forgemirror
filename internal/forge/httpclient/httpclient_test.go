package httpclient_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

func get(t *testing.T, c *http.Client, url string, hdr map[string]string) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	return c.Do(req)
}

// TestOwnTransport: idle-connection cleanup on the shared default transport
// must not break requests of forge clients.
func TestOwnTransport(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]*http.Client{
		"api":      httpclient.New(time.Second),
		"transfer": httpclient.NewTransfer(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if c.Transport == nil || c.Transport == http.DefaultTransport {
				t.Errorf("Transport = %v, want an own transport", c.Transport)
			}
		})
	}
}

func TestRefusesCrossHostRedirect(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	var leaked atomic.Bool

	b := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		if r.Header.Get("Private-Token") != "" {
			leaked.Store(true)
		}
	}))
	defer b.Close()

	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL, http.StatusFound)
	}))
	defer a.Close()

	resp, err := get(t, httpclient.New(time.Second), a.URL, map[string]string{"Private-Token": "s3cret"})
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error on cross-host redirect")
	}

	if hits.Load() != 0 || leaked.Load() {
		t.Fatalf("target B was contacted (hits=%d, leaked=%v)", hits.Load(), leaked.Load())
	}
}

func TestFollowsSameHostRedirect(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)

			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := get(t, httpclient.New(time.Second), srv.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestStopsAfterTenRedirects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()

	resp, err := get(t, httpclient.New(time.Second), srv.URL+"/loop", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error on redirect loop")
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	resp, err := get(t, httpclient.New(50*time.Millisecond), srv.URL, nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected timeout error")
	}
}

// A same-host redirect to another scheme cannot be served by one httptest
// server, so the redirect policy is called directly.
func TestRedirectPolicyScheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{name: "same scheme", from: "https://git.example/a", to: "https://git.example/b"},
		{name: "https to http", from: "https://git.example/a", to: "http://git.example/b", wantErr: true},
		{name: "http to https", from: "http://git.example/a", to: "https://git.example/b", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			from, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.from, nil)
			if err != nil {
				t.Fatal(err)
			}
			to, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.to, nil)
			if err != nil {
				t.Fatal(err)
			}

			err = httpclient.New(time.Second).CheckRedirect(to, []*http.Request{from})
			if (err != nil) != tc.wantErr {
				t.Errorf("CheckRedirect() error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
