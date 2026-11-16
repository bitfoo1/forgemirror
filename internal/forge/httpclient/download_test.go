package httpclient_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/forge/httpclient"
)

const auth = "token s3cret"

// downloadForge serves a forge that requires the token, with redirects to
// itself and to a separate object storage that must never see the token.
func downloadForge(t *testing.T) string {
	t.Helper()

	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "credentials leaked to object storage", http.StatusTeapot)

			return
		}
		_, _ = io.WriteString(w, "from storage")
	}))
	t.Cleanup(storage.Close)

	var forge *httptest.Server
	forge = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != auth {
			http.Error(w, "unauthorised", http.StatusUnauthorized)

			return
		}
		switch r.URL.Path {
		case "/direct":
			_, _ = io.WriteString(w, "direct")
		case "/same-host":
			http.Redirect(w, r, forge.URL+"/direct", http.StatusFound)
		case "/storage":
			http.Redirect(w, r, storage.URL+"/blob", http.StatusFound)
		case "/no-location":
			w.WriteHeader(http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(forge.Close)

	return forge.URL
}

func TestDownload(t *testing.T) {
	t.Parallel()

	base := downloadForge(t)
	tests := []struct {
		path    string
		want    string
		wantErr string
	}{
		{path: "/direct", want: "direct"},
		{path: "/same-host", want: "direct"},
		{path: "/storage", want: "from storage"},
		{path: "/missing", wantErr: "unexpected status 404"},
		{path: "/no-location", wantErr: "Location"},
		{path: "/loop", wantErr: "too many redirects"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			body, err := httpclient.Download(t.Context(), httpclient.NewTransfer(), base+tc.path, auth)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Download() error = %v, want %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), "s3cret") {
					t.Fatalf("error leaks the token: %v", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("Download() error = %v", err)
			}
			defer body.Close()
			got, _ := io.ReadAll(body)
			if string(got) != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDownloadRefusesDowngrade(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/blob", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := srv.Client()
	if _, err := httpclient.Download(t.Context(), c, srv.URL+"/x", auth); err == nil ||
		!strings.Contains(err.Error(), "https to http") {
		t.Fatalf("Download() error = %v, want downgrade refusal", err)
	}
}

func TestNewTransferHasNoTimeout(t *testing.T) {
	t.Parallel()

	if c := httpclient.NewTransfer(); c.Timeout != 0 || c.CheckRedirect == nil {
		t.Errorf("NewTransfer() = timeout %v, redirect rule set %v; want 0, true", c.Timeout, c.CheckRedirect != nil)
	}
}
