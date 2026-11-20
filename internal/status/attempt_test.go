package status_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestAttemptOutcomesAreIndependent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                         string
		firstError, errorGH, errorCB error
		want                         string
	}{
		{name: "success ignores previous errors", firstError: errors.New("old error"), want: "succeeded"},
		{name: "partial", errorCB: errors.New("denied"), want: "partially failed"},
		{name: "failed", errorGH: errors.New("denied"), errorCB: errors.New("denied"), want: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore()
			s.TargetFinished(t.Context(), "acme/app", "gh", time.Second, tc.firstError)
			s.RunStarted(t.Context(), "acme/app", true)
			s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true})
			s.TargetFinished(t.Context(), "acme/app", "gh", time.Second, tc.errorGH)
			s.TargetFinished(t.Context(), "acme/app", "cb", time.Second, tc.errorCB)
			s.RunFinished(t.Context(), "acme/app")
			s.RunStarted(t.Context(), "acme/app", false)
			s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app"})
			s.RunFinished(t.Context(), "acme/app")
			if got := s.Snapshot().Repos[0].LastAttempt.Status; got != tc.want {
				t.Fatalf("status = %s; want %s", got, tc.want)
			}
		})
	}
}

type switchPersistence struct{ fail bool }

func (*switchPersistence) Load(context.Context) (map[string][]byte, error) {
	return map[string][]byte{}, nil
}

func (p *switchPersistence) Save(context.Context, string, []byte) error {
	if p.fail {
		return errors.New("disk full")
	}
	return nil
}

func TestRefreshWriteFailurePreservesAttempt(t *testing.T) {
	t.Parallel()
	persistence := &switchPersistence{}
	s := status.New(status.Options{Persistence: persistence})
	s.RunStarted(t.Context(), "acme/app", true)
	s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app", Apply: true})
	s.RunFinished(t.Context(), "acme/app")
	persistence.fail = true
	s.RunStarted(t.Context(), "acme/app", false)
	s.PlanFinished(t.Context(), plan.Repo{Name: "acme/app"})
	s.RunFinished(t.Context(), "acme/app")
	r := s.Snapshot().Repos[0]
	if r.LastAttempt.Status != "succeeded" {
		t.Fatalf("read-only failure changed attempt: %+v", r.LastAttempt)
	}
	if !r.Stale {
		t.Fatal("undurable current diff reported fresh")
	}
}
