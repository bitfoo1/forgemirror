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
	errCrossHostRedirect = errors.New("refusing redirect to a different host")
	errTooManyRedirects  = errors.New("stopped after too many redirects")
)

// New returns a client that never follows redirects to another host. Forge
// libraries set custom auth headers (e.g. GitLab's PRIVATE-TOKEN) that Go
// does not strip on cross-host redirects, so following them would leak the
// token.
func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errTooManyRedirects
			}

			if req.URL.Host != via[0].URL.Host {
				return errCrossHostRedirect
			}

			return nil
		},
	}
}
