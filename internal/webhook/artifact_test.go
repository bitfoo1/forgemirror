package webhook_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

const (
	artifactRepo = `"repository": {
    "name": "app", "full_name": "acme/app", "owner": {"login": "acme"},
    "clone_url": "https://git.example/acme/app.git", "topics": ["mirror"]
  }`
	releasePayload         = `{"action": "published", "release": {"tag_name": "v1.0.0"}, ` + artifactRepo + `}`
	packagePayload         = `{"action": "created", "package": {"name": "app", "version": "1.0.0"}, ` + artifactRepo + `}`
	unlinkedPackage        = `{"action": "created", "package": {"name": "app", "version": "1.0.0"}, "repository": null}`
	mismatchedRelease      = `{"action": "published", "release": {"tag_name": "v1"}, "repository": {"name": "other", "full_name": "acme/app", "owner": {"login": "acme"}, "clone_url": "https://git.example/acme/app.git"}}`
	releaseWithoutCloneURL = `{"action": "published", "release": {"tag_name": "v1"}, "repository": {"name": "app", "full_name": "acme/app", "owner": {"login": "acme"}}}`
)

func TestArtifactEvents(t *testing.T) {
	t.Parallel()

	admit := staticFilter{decision: filter.Decision{Mirror: true}}
	tests := []struct {
		name    string
		event   string
		body    string
		opts    webhook.Options
		status  int
		ref     string
		message string
	}{
		{
			name: "release", event: "release", body: releasePayload, opts: webhook.Options{Filter: admit},
			status: http.StatusAccepted, ref: "refs/tags/v1.0.0", message: "mirror queued",
		},
		{
			name: "package", event: "package", body: packagePayload, opts: webhook.Options{Filter: admit},
			status: http.StatusAccepted, ref: "package app:1.0.0", message: "mirror queued",
		},
		{
			name: "unlinked package", event: "package", body: unlinkedPackage, opts: webhook.Options{Filter: admit},
			status: http.StatusOK, message: "not linked to a repository",
		},
		{
			name:    "disabled",
			event:   "release",
			body:    releasePayload,
			opts:    webhook.Options{Filter: admit, IgnorePush: true},
			status:  http.StatusOK,
			message: "release events disabled",
		},
		{
			name: "filtered", event: "package", body: packagePayload,
			opts:   webhook.Options{Filter: staticFilter{decision: filter.Decision{Reason: "nope"}}},
			status: http.StatusOK, message: "not mirrored: nope",
		},
		{
			name: "clone URL rejected", event: "release", body: releasePayload,
			opts:   webhook.Options{Filter: admit, Source: staticSource{err: errors.New("foreign host")}},
			status: http.StatusBadRequest, message: "foreign host",
		},
		{
			name: "name mismatch", event: "release", body: mismatchedRelease, opts: webhook.Options{Filter: admit},
			status: http.StatusBadRequest, message: "does not match",
		},
		{
			name:    "missing clone URL",
			event:   "release",
			body:    releaseWithoutCloneURL,
			opts:    webhook.Options{Filter: admit},
			status:  http.StatusBadRequest,
			message: "missing repository.clone_url",
		},
		{
			name: "malformed", event: "package", body: `{`, opts: webhook.Options{Filter: admit},
			status: http.StatusBadRequest, message: "malformed JSON",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, dispatcher := serve(t, tc.opts, validRequest(tc.body).with("X-Forgejo-Event", tc.event))

			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("response = %d %q, want %d containing %q", rec.Code, rec.Body.String(), tc.status, tc.message)
			}
			if tc.status != http.StatusAccepted {
				if len(dispatcher.events) != 0 {
					t.Errorf("dispatched %+v, want nothing", dispatcher.events)
				}

				return
			}
			if len(dispatcher.events) != 1 {
				t.Fatalf("dispatched %d events, want 1", len(dispatcher.events))
			}
			got := dispatcher.events[0]
			if got.Kind != webhook.EventPush || got.Ref != tc.ref || got.Repository.FullName != "acme/app" ||
				got.Repository.Owner != "acme" || got.Repository.Name != "app" || got.Delivery != "delivery-1" {
				t.Errorf("event = %+v", got)
			}
		})
	}
}
