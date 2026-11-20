package dispatch_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func verificationRefs(fail bool) func(context.Context, mirror.Remote) (map[string]string, error) {
	var reads atomic.Int32
	return func(_ context.Context, remote mirror.Remote) (map[string]string, error) {
		if !strings.Contains(remote.URL, "git.example") {
			return map[string]string{"refs/heads/main": "old"}, nil
		}
		if reads.Add(1) > 1 && fail {
			return nil, errors.New("source unavailable")
		}
		return map[string]string{"refs/heads/main": "new"}, nil
	}
}

func TestVerificationAfterFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                       string
		failPush, failVerification bool
	}{
		{name: "success"}, {name: "failed push", failPush: true}, {name: "failed comparison", failVerification: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &fakeMirrorer{
				refsHook: verificationRefs(tc.failVerification),
				pushHook: func(context.Context, mirror.Remote) error {
					if tc.failPush {
						return errors.New("push denied")
					}
					return nil
				},
			}
			obs := &recordingObserver{}
			d := dispatch.New(dispatch.Options{
				Mirrorer: m, Source: config.Source{}, Targets: loadTargets(t, codebergTarget),
				MaxConcurrent: 4, Logger: slog.New(slog.DiscardHandler), Observer: obs, VerifyAfterApply: true,
			})
			d.Dispatch(t.Context(), withKind(event("app"), webhook.EventSync))
			shutdown(t, d)
			plans := obs.planSnapshot()
			if len(plans) != 2 || plans[1].Apply {
				t.Fatalf("missing read-only verification: %+v", plans)
			}
			if tc.failVerification && !plans[1].Failed() {
				t.Fatal("comparison failure reported verified")
			}
			_, pushes, _, _ := m.snapshot()
			if len(pushes) != 1 {
				t.Fatalf("push count = %d, want one", len(pushes))
			}
		})
	}
}

func TestPostApplyVerification(t *testing.T) {
	t.Parallel()
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "checkpoint failed"}[blocked], func(t *testing.T) {
			t.Parallel()
			obs := &recordingObserver{}
			m := &fakeMirrorer{}
			d := dispatch.New(dispatch.Options{
				Mirrorer:         m,
				Source:           config.Source{Username: "oauth2", Token: "source-token"},
				Targets:          loadTargets(t, codebergTarget),
				MaxConcurrent:    4,
				Auto:             true,
				Logger:           slog.New(slog.DiscardHandler),
				Observer:         obs,
				VerifyAfterApply: true,
				BeforeApply: func(context.Context, plan.Repo) error {
					if blocked {
						return errors.New("disk full")
					}
					return nil
				},
			})
			d.Dispatch(t.Context(), withKind(event("app"), webhook.EventSync))
			shutdown(t, d)
			plans := obs.planSnapshot()
			clones, pushes, _, _ := m.snapshot()
			if blocked {
				if len(clones)+len(pushes) != 0 {
					t.Fatal("mutated remote after failed checkpoint")
				}
			} else if len(plans) != 2 || !plans[0].Apply || plans[1].Apply {
				t.Fatalf("plans = %+v; want apply then read-only verification", plans)
			}
		})
	}
}
