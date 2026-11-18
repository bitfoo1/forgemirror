// Package httpclient provides the single [http.Client] handed to every forge
// API library.
package httpclient

import (
	"errors"
	"net/http"
	"time"
)

const maxRedirects = 10

var (
	errCrossHostRedirect   = errors.New("refusing redirect to a different host")
	errCrossSchemeRedirect = errors.New("refusing redirect to a different scheme")
	errTooManyRedirects    = errors.New("stopped after too many redirects")
)

// New returns a client that never follows redirects to another host or
// scheme. Forge libraries set custom auth headers (e.g. GitLab's
// PRIVATE-TOKEN) that Go does not strip on cross-host redirects, so following
// them would leak the token; Go keeps even Authorization on a same-host
// https to http redirect, which would send it in clear text.
func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: newTransport(),
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errTooManyRedirects
			}

			if req.URL.Host != via[0].URL.Host {
				return errCrossHostRedirect
			}

			if req.URL.Scheme != via[0].URL.Scheme {
				return errCrossSchemeRedirect
			}

			return nil
		},
	}
}

// newTransport clones the default transport so that the client owns its
// connection pool. On the shared [http.DefaultTransport], any
// CloseIdleConnections call elsewhere (httptest.Server.Close does one) can
// fail a request: a response without a body (e.g. 204 on DELETE) returns the
// connection to the idle pool before the caller receives it, and closing it
// in that window yields "transport connection broken", which Go does not
// retry.
func newTransport() http.RoundTripper {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}

	return http.DefaultTransport
}
