package dispatch_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestLFSBlocksOnlyFailedDestination(t *testing.T) {
	t.Parallel()
	for _, sourceFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "destination", true: "source"}[sourceFailure], func(t *testing.T) {
			t.Parallel()
			testLFSFailureIsolation(t, sourceFailure)
		})
	}
}

func testLFSFailureIsolation(t *testing.T, sourceFailure bool) {
	t.Helper()
	var mu sync.Mutex
	downloaded := false
	uploaded := map[string]bool{}
	m := &fakeMirrorer{}
	m.fetchLFSHook = func(context.Context, mirror.Remote) error {
		mu.Lock()
		defer mu.Unlock()
		downloaded = true
		if sourceFailure {
			return errors.New("missing object")
		}
		return nil
	}
	m.pushLFSHook = func(_ context.Context, r mirror.Remote) error {
		mu.Lock()
		defer mu.Unlock()
		if !downloaded {
			t.Error("upload preceded download")
		}
		if strings.Contains(r.URL, "github.com") {
			return errors.New("quota")
		}
		uploaded[r.URL] = true
		return nil
	}
	m.pushHook = func(_ context.Context, r mirror.Remote) error {
		mu.Lock()
		defer mu.Unlock()
		if !uploaded[r.URL] {
			t.Error("Git refs published before LFS")
		}
		return nil
	}
	d := newDispatcher(t, m, nil, 2)
	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)
	_, pushes, _, _ := m.snapshot()
	want := 1
	if sourceFailure {
		want = 0
	}
	if len(pushes) != want {
		t.Fatalf("Git pushes=%d want %d", len(pushes), want)
	}
}

func TestQueuedLFSRepairSurvivesRefresh(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m := &fakeMirrorer{refsHook: func(context.Context, mirror.Remote) (map[string]string, error) {
		once.Do(func() { close(entered); <-release })
		return map[string]string{"refs/heads/main": "same"}, nil
	}}
	obs := &recordingObserver{}
	d := newDispatcher(t, m, obs, 2)
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	<-entered
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRepairLFS))
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRefresh))
	close(release)
	waitRuns(t, obs, 2)
	shutdown(t, d)
	clones, pushes, _, _ := m.snapshot()
	if len(clones) != 1 || len(pushes) != 0 {
		t.Fatalf("repair lost or published refs: clones=%d pushes=%d", len(clones), len(pushes))
	}
}

func TestLFSRepairWithMatchingRefs(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	uploads := 0
	m := &fakeMirrorer{
		refsHook: func(context.Context, mirror.Remote) (map[string]string, error) {
			return map[string]string{"refs/heads/main": "same"}, nil
		},
		pushLFSHook: func(context.Context, mirror.Remote) error { mu.Lock(); defer mu.Unlock(); uploads++; return nil },
	}
	d := newDispatcher(t, m, nil, 2)
	e := event("app")
	e.Kind = webhook.EventRepairLFS
	d.Dispatch(t.Context(), e)
	shutdown(t, d)
	clones, pushes, _, _ := m.snapshot()
	if len(clones) != 1 || len(pushes) != 0 || uploads != 2 {
		t.Fatalf("clones=%d pushes=%d LFS uploads=%d", len(clones), len(pushes), uploads)
	}
}
