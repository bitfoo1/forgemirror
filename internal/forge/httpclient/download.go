package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

var (
	errDowngrade  = errors.New("refusing redirect from https to http")
	errNoLocation = errors.New("redirect without a usable Location")
)

// NewTransfer returns a client for attachment uploads and downloads. It
// applies the redirect rule of [New] but no overall timeout: a large file
// legitimately takes longer than one API request, so the caller's context
// (bounded by mirror.timeout) limits it instead.
func NewTransfer() *http.Client {
	c := New(0)
	c.Timeout = 0

	return c
}

// Download fetches rawURL with the Authorization header set to authorization
// and returns the body. The caller has checked that rawURL belongs to the
// forge the credentials are for.
//
// Forges may redirect downloads to object storage on another host. Those
// redirects are followed without the Authorization header (the presigned URL
// authorises itself), which the client's own redirect rule would refuse
// outright; a downgrade from https to http is refused either way.
func Download(ctx context.Context, c *http.Client, rawURL, authorization string) (io.ReadCloser, error) {
	manual := *c
	manual.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("download URL is not parseable")
	}
	origin := target.Host

	for range maxRedirects {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if reqErr != nil {
			return nil, fmt.Errorf("building download request: %w", reqErr)
		}
		if target.Host == origin && authorization != "" {
			req.Header.Set("Authorization", authorization)
		}

		resp, doErr := manual.Do(req)
		if doErr != nil {
			return nil, fmt.Errorf("downloading %s: %w", target.Redacted(), doErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.Body, nil
		}
		_ = resp.Body.Close()

		next, hopErr := nextHop(target, resp)
		if hopErr != nil {
			return nil, hopErr
		}
		target = next
	}

	return nil, errTooManyRedirects
}

// nextHop returns the redirect target of a non-2xx response, or the error
// that ends the download.
func nextHop(current *url.URL, resp *http.Response) (*url.URL, error) {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("downloading %s: unexpected status %s", current.Redacted(), resp.Status)
	}

	location := resp.Header.Get("Location")
	next, err := current.Parse(location)
	if err != nil || location == "" {
		return nil, errNoLocation
	}
	if current.Scheme == "https" && next.Scheme != "https" {
		return nil, errDowngrade
	}

	return next, nil
}
