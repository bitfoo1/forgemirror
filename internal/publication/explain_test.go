package publication_test

import (
	"slices"
	"testing"
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/publication"
	"go.bitfoo.de/forgemirror/internal/retry"
	"go.bitfoo.de/forgemirror/internal/status"
)

func TestPublicationExplanations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		input      publication.Input
		code       string
		additional string
	}{
		{name: "combined holds", input: publication.Input{Known: true, Auto: false, Plan: &plan.Target{ApprovalRequired: true, RefsUnknown: true}, Pause: &status.Pause{Reason: "maintenance"}}, code: "publication_paused", additional: "approval_required"},
		{name: "review", input: publication.Input{Known: true, Auto: false, Plan: &plan.Target{RefsUnknown: true}}, code: "review_required"},
		{name: "retry", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Retry: &retry.Entry{State: "waiting", Next: now.Add(time.Minute)}}, code: "retry_scheduled"},
		{name: "automatic retry held by review", input: publication.Input{Known: true, Auto: false, Plan: &plan.Target{}, Retry: &retry.Entry{State: "waiting", Automatic: true}}, code: "retry_review_hold"},
		{name: "stale", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Stale: true}, code: "verification_pending"},
		{name: "matching refs are not LFS proof", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}}, code: "in_sync", additional: "lfs_not_verified"},
		{name: "saved history not admitted", input: publication.Input{Plan: &plan.Target{}}, code: "not_admitted"},
		{name: "storage unavailable", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, StateUnavailable: true}, code: "state_unavailable"},
		{name: "active manual sync", input: publication.Input{Known: true, Auto: false, Running: true, Applying: true, Plan: &plan.Target{RefsUnknown: true}}, code: "run_in_progress"},
		{name: "deleted source", input: publication.Input{Known: true, Auto: true, SourceDeleted: true}, code: "source_deleted"},
		{name: "removed target", input: publication.Input{Known: true, Auto: true, TargetRemoved: true}, code: "target_not_configured"},
		{name: "exhausted retry", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Retry: &retry.Entry{State: "exhausted"}}, code: "retry_exhausted"},
		{name: "queued work", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Queued: true}, code: "follow_up_queued"},
		{name: "no comparison", input: publication.Input{Known: true, Auto: true}, code: "never_checked"},
		{name: "non-retryable failure", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Retry: &retry.Entry{State: "needs intervention"}}, code: "sync_failed"},
		{name: "historical push failure superseded", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Last: &status.Target{Error: "failed"}}, code: "in_sync", additional: "previous_push_failed"},
		{name: "error text is not policy", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{Error: "approval_required"}}, code: "comparison_failed"},
		{name: "incomplete feature comparison", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{Warnings: []string{"source package listing skipped"}}}, code: "comparison_warning"},
		{name: "explicit retry retains review authorization", input: publication.Input{Known: true, Auto: false, Plan: &plan.Target{RefsUnknown: true}, Retry: &retry.Entry{State: "waiting"}}, code: "retry_scheduled"},
		{name: "expired pause", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Pause: &status.Pause{Until: now.Add(-time.Hour)}}, code: "in_sync"},
		{name: "failed LFS persists past ref agreement", input: publication.Input{Known: true, Auto: true, Plan: &plan.Target{}, Last: &status.Target{Operations: []status.Operation{{Name: "lfs", Error: "failed"}}}}, code: "lfs_transfer_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.input.Now = now
			got := publication.Explain(tt.input)
			if got.Code != tt.code {
				t.Fatalf("primary=%s reasons=%+v", got.Code, got.Reasons)
			}
			if tt.additional != "" &&
				!slices.ContainsFunc(got.Reasons, func(r publication.Reason) bool { return r.Code == tt.additional }) {
				t.Fatalf("missing additional reason %s: %+v", tt.additional, got.Reasons)
			}
			if got.Next == "" {
				t.Fatal("no next action explained")
			}
		})
	}
}
