package webhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const (
	secret = "webhook-secret"

	pushPayload = `{
  "ref": "refs/heads/main",
  "before": "0000000000000000000000000000000000000000",
  "after": "1111111111111111111111111111111111111111",
  "repository": {
    "name": "app",
    "full_name": "acme/app",
    "owner": {"login": "acme", "username": "acme"},
    "private": true,
    "fork": false,
    "clone_url": "https://git.example/acme/app.git",
    "topics": ["mirror"]
  }
}`
)

type recordingDispatcher struct {
	mu     sync.Mutex
	events []webhook.Event
	ctxErr []error
}

func (d *recordingDispatcher) Dispatch(ctx context.Context, e webhook.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, e)
	d.ctxErr = append(d.ctxErr, ctx.Err())
}

type staticFilter struct{ decision filter.Decision }

func (f staticFilter) Decide(repo.Repository, string) filter.Decision { return f.decision }

type recordingObserver struct {
	mu       sync.Mutex
	outcomes []webhook.Outcome
}

func (o *recordingObserver) ObserveWebhook(_ context.Context, outcome webhook.Outcome) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outcomes = append(o.outcomes, outcome)
}

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))

	return hex.EncodeToString(mac.Sum(nil))
}

type request struct {
	body        string
	headers     map[string]string
	contentType string
}

func validRequest(body string) request {
	return request{
		body:        body,
		contentType: "application/json",
		headers: map[string]string{
			"X-Forgejo-Event":     "push",
			"X-Forgejo-Signature": sign(body),
			"X-Forgejo-Delivery":  "delivery-1",
		},
	}
}

func (r request) with(header, value string) request {
	headers := make(map[string]string, len(r.headers)+1)
	maps.Copy(headers, r.headers)
	if value == "" {
		delete(headers, header)
	} else {
		headers[header] = value
	}
	r.headers = headers

	return r
}

func TestHandler(t *testing.T) {
	t.Parallel()

	mirrorAll := staticFilter{decision: filter.Decision{Mirror: true}}

	tests := []struct {
		name         string
		req          request
		filter       webhook.Filter
		maxBody      int64
		wantStatus   int
		wantOutcome  webhook.Outcome
		wantDispatch bool
	}{
		{
			name: "valid push", req: validRequest(pushPayload), filter: mirrorAll,
			wantStatus: http.StatusAccepted, wantOutcome: webhook.OutcomeAccepted, wantDispatch: true,
		},
		{
			name: "gitea headers",
			req: validRequest(pushPayload).
				with("X-Forgejo-Event", "").with("X-Forgejo-Signature", "").
				with("X-Gitea-Event", "push").with("X-Gitea-Signature", sign(pushPayload)),
			filter:     mirrorAll,
			wantStatus: http.StatusAccepted, wantOutcome: webhook.OutcomeAccepted, wantDispatch: true,
		},
		{
			name: "content type with charset", req: func() request {
				r := validRequest(pushPayload)
				r.contentType = "application/json; charset=utf-8"

				return r
			}(),
			filter:     mirrorAll,
			wantStatus: http.StatusAccepted, wantOutcome: webhook.OutcomeAccepted, wantDispatch: true,
		},
		{
			name: "invalid signature", req: validRequest(pushPayload).with("X-Forgejo-Signature", sign("other")),
			filter: mirrorAll, wantStatus: http.StatusUnauthorized, wantOutcome: webhook.OutcomeUnauthorized,
		},
		{
			name: "missing signature", req: validRequest(pushPayload).with("X-Forgejo-Signature", ""),
			filter: mirrorAll, wantStatus: http.StatusUnauthorized, wantOutcome: webhook.OutcomeUnauthorized,
		},
		{
			name: "signature not hex", req: validRequest(pushPayload).with("X-Forgejo-Signature", "zz"),
			filter: mirrorAll, wantStatus: http.StatusUnauthorized, wantOutcome: webhook.OutcomeUnauthorized,
		},
		{
			name: "signature over different body", req: func() request {
				r := validRequest(pushPayload)
				r.body = strings.Replace(pushPayload, "acme/app", "acme/evil", 1)

				return r
			}(),
			filter: mirrorAll, wantStatus: http.StatusUnauthorized, wantOutcome: webhook.OutcomeUnauthorized,
		},
		{
			name: "form encoded", req: func() request {
				r := validRequest(pushPayload)
				r.contentType = "application/x-www-form-urlencoded"

				return r
			}(),
			filter: mirrorAll, wantStatus: http.StatusUnsupportedMediaType, wantOutcome: webhook.OutcomeUnsupportedMed,
		},
		{
			name: "body too large", req: validRequest(pushPayload), filter: mirrorAll, maxBody: 10,
			wantStatus: http.StatusRequestEntityTooLarge, wantOutcome: webhook.OutcomeTooLarge,
		},
		{
			name: "malformed json", req: validRequest(`{"ref": `), filter: mirrorAll,
			wantStatus: http.StatusBadRequest, wantOutcome: webhook.OutcomeBadRequest,
		},
		{
			name:        "missing fields",
			req:         validRequest(`{"ref": "refs/heads/main", "repository": {}}`),
			filter:      mirrorAll,
			wantStatus:  http.StatusBadRequest,
			wantOutcome: webhook.OutcomeBadRequest,
		},
		{
			name: "other event", req: validRequest(pushPayload).with("X-Forgejo-Event", "issues"), filter: mirrorAll,
			wantStatus: http.StatusOK, wantOutcome: webhook.OutcomeIgnored,
		},
		{
			name: "filtered", req: validRequest(pushPayload),
			filter:     staticFilter{decision: filter.Decision{Reason: "fork (filter.exclude_forks)"}},
			wantStatus: http.StatusOK, wantOutcome: webhook.OutcomeFiltered,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dispatcher := &recordingDispatcher{}
			observer := &recordingObserver{}
			maxBody := tc.maxBody
			if maxBody == 0 {
				maxBody = 1 << 20
			}
			h := webhook.NewHandler(webhook.Options{
				Secret:       []byte(secret),
				MaxBodyBytes: maxBody,
				Filter:       tc.filter,
				Dispatcher:   dispatcher,
				Logger:       slog.New(slog.DiscardHandler),
				Observer:     observer,
			})

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/webhook",
				strings.NewReader(tc.req.body),
			)
			req.Header.Set("Content-Type", tc.req.contentType)
			for k, v := range tc.req.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				body, _ := io.ReadAll(rec.Body)
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.wantStatus, body)
			}
			if !slices.Equal(observer.outcomes, []webhook.Outcome{tc.wantOutcome}) {
				t.Errorf("outcomes = %v, want [%s]", observer.outcomes, tc.wantOutcome)
			}
			if got := len(dispatcher.events) == 1; got != tc.wantDispatch {
				t.Fatalf("dispatched = %v (%d events), want %v", got, len(dispatcher.events), tc.wantDispatch)
			}
			if tc.wantDispatch && dispatcher.ctxErr[0] != nil {
				t.Errorf("dispatch context already done: %v", dispatcher.ctxErr[0])
			}
		})
	}
}

func TestHandlerEvent(t *testing.T) {
	t.Parallel()

	dispatcher := &recordingDispatcher{}
	h := webhook.NewHandler(webhook.Options{
		Secret:       []byte(secret),
		MaxBodyBytes: 1 << 20,
		Filter:       staticFilter{decision: filter.Decision{Mirror: true}},
		Dispatcher:   dispatcher,
		Logger:       slog.New(slog.DiscardHandler),
	})

	r := validRequest(pushPayload)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook", strings.NewReader(r.body))
	req.Header.Set("Content-Type", r.contentType)
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(dispatcher.events) != 1 {
		t.Fatalf("dispatched %d events, want 1", len(dispatcher.events))
	}
	got := dispatcher.events[0]
	want := webhook.Event{
		Ref:      "refs/heads/main",
		Delivery: "delivery-1",
		Repository: repo.Repository{
			Owner:    "acme",
			Name:     "app",
			FullName: "acme/app",
			CloneURL: "https://git.example/acme/app.git",
			Private:  true,
			Topics:   []string{"mirror"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event = %+v\nwant    %+v", got, want)
	}
}
