package dispatch_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func TestScopedSyncDoesNotUpgradeQueuedRepair(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var clones atomic.Int32
	m := &fakeMirrorer{cloneHook: func(ctx context.Context, _ mirror.Remote) error {
		if clones.Add(1) == 1 {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	plain := strings.NewReplacer("    type: github\n", "", "    type: gitlab\n", "", "    create: true\n", "").
		Replace(githubTarget + codebergTarget + gitlabTarget)
	d, obs := newModeDispatcher(t, plain, m, nil, modeOptions{auto: true})
	first := withKind(event("app"), webhook.EventSync)
	first.Targets = []string{"github"}
	d.Dispatch(t.Context(), first)
	waitFor(t, started)
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventRepairLFS))
	d.Dispatch(t.Context(), first)
	close(release)
	waitRuns(t, obs, 3)
	shutdown(t, d)
	_, pushes, _, _ := m.snapshot()
	for _, push := range pushes {
		if strings.Contains(push.URL, "codeberg.org") || strings.Contains(push.URL, "gitlab.com") {
			t.Fatal("object-only repair upgraded to unselected ref publication")
		}
	}
}

func TestCoalescingKeepsScopedTargetUnion(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var clones atomic.Int32
	m := &fakeMirrorer{cloneHook: func(ctx context.Context, _ mirror.Remote) error {
		if clones.Add(1) == 1 {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	plain := strings.ReplaceAll(
		strings.ReplaceAll(githubTarget+codebergTarget+gitlabTarget, "    type: github\n", ""),
		"    type: gitlab\n",
		"",
	)
	plain = strings.ReplaceAll(plain, "    create: true\n", "")
	d, obs := newModeDispatcher(t, plain, m, nil, modeOptions{auto: true})
	first := withKind(event("app"), webhook.EventSync)
	first.Targets = []string{"github"}
	d.Dispatch(t.Context(), first)
	waitFor(t, started)
	second := first
	second.Targets = []string{"github"}
	d.Dispatch(t.Context(), second)
	third := first
	third.Targets = []string{"codeberg"}
	d.Dispatch(t.Context(), third)
	close(release)
	waitRuns(t, obs, 2)
	shutdown(t, d)
	_, pushes, _, _ := m.snapshot()
	if len(pushes) == 0 {
		t.Fatal("no selected target published")
	}
	for _, push := range pushes {
		if strings.Contains(push.URL, "gitlab.com") {
			t.Fatal("coalescing widened explicit scope to unselected GitLab")
		}
	}
}
