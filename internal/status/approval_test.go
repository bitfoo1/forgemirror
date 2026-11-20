package status_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestControlRedactionPreservesIdentity(t *testing.T) {
	t.Parallel()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	opts := status.Options{ControlPersistence: db.Namespace("controls"), Redact: state.Redactor([]string{"s", "dead"})}
	s := status.New(opts)
	id := strings.Repeat("dead", 16)
	if err = s.Approve(t.Context(), "acme/app", "github", id); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordRetry(t.Context(), "acme/app", "github", "sync", false, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	if err = s.Pause(t.Context(), status.Pause{Target: "github", Reason: "auth=s"}); err != nil {
		t.Fatal(err)
	}
	restored := status.New(opts)
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !restored.Approved("acme/app", "github", id) || restored.Retries()[0].Kind != "sync" {
		t.Fatal("redaction corrupted functional control metadata")
	}
	if p, _ := restored.Paused("acme/app", "github"); strings.Contains(p.Reason, "auth=s") {
		t.Fatal("pause reason secret not redacted")
	}
}

func TestApprovalBindsSourceAndDestination(t *testing.T) {
	t.Parallel()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := status.New(status.Options{ControlPersistence: db.Namespace("controls")})
	id := status.PublicationID("https://source.example/acme/app.git", "https://github.com/acme/app.git")
	if release, permitErr := s.BeginPublication(
		"acme/app",
		"github",
		id,
		true,
	); !errors.Is(
		permitErr,
		status.ErrApprovalRequired,
	) {
		if release != nil {
			release()
		}
		t.Fatalf("publication allowed before approval: %v", permitErr)
	}
	if err = s.Approve(t.Context(), "acme/app", "github", id); err != nil {
		t.Fatal(err)
	}
	restored := status.New(status.Options{ControlPersistence: db.Namespace("controls")})
	if err = restored.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	release, err := restored.BeginPublication("acme/app", "github", id, true)
	if err != nil {
		t.Fatal(err)
	}
	release()
	changed := status.PublicationID("https://source.example/acme/app.git", "https://github.com/other/app.git")
	if restored.Approved("acme/app", "github", changed) {
		t.Fatal("destination remapping inherited approval")
	}
	changed = status.PublicationID("https://other.example/acme/app.git", "https://github.com/acme/app.git")
	if restored.Approved("acme/app", "github", changed) {
		t.Fatal("source remapping inherited approval")
	}
}
