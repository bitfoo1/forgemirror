package notify_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/notify"
	"go.bitfoo.de/forgemirror/internal/state"
)

func notificationState(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"), "source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err = db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func testOutbox(t *testing.T, server *httptest.Server, now func() time.Time) *notify.Outbox {
	t.Helper()
	box, err := notify.New(
		t.Context(),
		notify.Options{
			URL:         server.URL,
			Token:       "notification-token",
			Client:      server.Client(),
			Persistence: notificationState(t).Namespace("notifications"),
			Now:         now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestDeliveryDoesNotRedirect(t *testing.T) {
	t.Parallel()
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	box := testOutbox(t, source, time.Now)
	if err := box.Record(t.Context(), notify.Event{Repo: "acme/app", Target: "github", State: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if err := box.DeliverDue(t.Context()); err == nil {
		t.Fatal("redirect reported success")
	}
	if leaked.Load() != 0 {
		t.Fatal("notification followed redirect")
	}
}

func TestRetryPreservesTransitionOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	box := testOutbox(t, server, func() time.Time { return now })
	if err := box.Record(t.Context(), notify.Event{Repo: "acme/app", Target: "github", State: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if err := box.DeliverDue(t.Context()); err == nil {
		t.Fatal("failed delivery reported success")
	}
	if err := box.Record(t.Context(), notify.Event{Repo: "acme/app", Target: "github", State: "healthy"}); err != nil {
		t.Fatal(err)
	}
	if err := box.DeliverDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("recovery overtook the pending blocked notification")
	}
	now = now.Add(time.Minute)
	if err := box.DeliverDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("delivery ignored Retry-After")
	}
	now = now.Add(time.Hour)
	if err := box.DeliverDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := box.DeliverDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("delivery calls=%d", calls.Load())
	}
}

func TestDurableTransitionNotifications(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var received []notify.Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer notification-token" {
			t.Error("missing webhook credential")
		}
		var event notify.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
		}
		if event.ID == "" || r.Header.Get("Idempotency-Key") != event.ID {
			t.Error("missing delivery identity")
		}
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	db := notificationState(t)
	opts := notify.Options{
		URL:         server.URL,
		Token:       "notification-token",
		UIURL:       "https://mirror.example/prefix",
		Client:      server.Client(),
		Persistence: db.Namespace("notifications"),
		Now:         func() time.Time { return now },
	}
	box, err := notify.New(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	event := notify.Event{Repo: "acme/app", Target: "github", State: "blocked", Reason: "approval_required"}
	if err = box.Record(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err = box.Record(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	restored, err := notify.New(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.DeliverDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	event.State = "healthy"
	event.Reason = ""
	if err = restored.Record(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err = restored.DeliverDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0].State != "blocked" || received[1].State != "recovered" ||
		received[0].URL != "https://mirror.example/prefix/" {
		t.Fatalf("received=%+v", received)
	}
}
