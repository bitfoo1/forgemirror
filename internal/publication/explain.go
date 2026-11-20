// Package publication explains publication state from evidence, never error-text parsing.
package publication

import (
	"time"

	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/retry"
	"go.bitfoo.de/forgemirror/internal/status"
)

// Input contains current policy, comparison and historical evidence for one destination.
type Input struct {
	Known, Auto, Stale, Running, Applying, Queued, SourceDeleted, ComparisonFailed bool
	TargetRemoved                                                                  bool
	StateUnavailable                                                               bool
	Plan                                                                           *plan.Target
	Last                                                                           *status.Target
	Pause                                                                          *status.Pause
	Retry                                                                          *retry.Entry
	Now, CheckedAt, LastSuccess                                                    time.Time
}

// Reason has a stable code and an actionable, credential-free explanation.
type Reason struct {
	Code, Title, Message, Action string
	Blocking                     bool
	At                           time.Time
}

// View distinguishes present blockers, comparison scope and historical evidence.
type View struct {
	Code, Headline, Message, Next string
	Held                          bool
	Reasons                       []Reason
	CheckedAt, LastSuccess, LFSAt time.Time
	LFSSucceeded                  bool
}

func reason(code, title, message, action string, blocking bool) Reason {
	return Reason{Code: code, Title: title, Message: message, Action: action, Blocking: blocking}
}

// Explain is pure. Reasons are ordered by actionable priority; all remain visible.
func Explain(input Input) View {
	view := View{CheckedAt: input.CheckedAt, LastSuccess: input.LastSuccess}
	view.Reasons = append(view.Reasons, controlReasons(input)...)
	view.Reasons = append(view.Reasons, activityReasons(input)...)
	view.Reasons = append(view.Reasons, retryReasons(input)...)
	appendComparisonEvidence(&view, input)
	if comparisonMatches(input) {
		view.Reasons = append(
			view.Reasons,
			reason(
				"in_sync",
				"Git and metadata match",
				"The latest comparison found no pending Git or metadata changes; this does not audit LFS objects.",
				"No Git/metadata action is needed. Use Repair LFS if object availability is uncertain.",
				false,
			),
		)
	}
	if input.Last != nil && input.Last.Error != "" {
		view.Reasons = append(
			view.Reasons,
			reason(
				"previous_push_failed",
				"Previous push failed",
				"The previous push reported a failure. Its historical result is separate from the current comparison.",
				"Inspect the last attempt and current diff before retrying.",
				false,
			),
		)
	}
	view.Reasons = append(
		view.Reasons,
		reason(
			"lfs_not_verified",
			"LFS comparison scope",
			"Git-ref comparisons do not verify LFS object availability.",
			"Use Repair LFS to reconcile source-history objects without publishing unrelated changes.",
			false,
		),
	)
	first := view.Reasons[0]
	view.Code, view.Headline, view.Message, view.Next, view.Held = first.Code, first.Title, first.Message, first.Action, first.Blocking
	return view
}

func appendComparisonEvidence(view *View, in Input) {
	comparisons := comparisonReasons(in)
	for _, r := range comparisons {
		if r.Blocking {
			view.Reasons = append(view.Reasons, r)
		}
	}
	addLFSEvidence(view, in.Last, !in.SourceDeleted && !in.TargetRemoved)
	for _, r := range comparisons {
		if !r.Blocking {
			view.Reasons = append(view.Reasons, r)
		}
	}
}

func comparisonMatches(in Input) bool {
	return in.Plan != nil && !in.Stale && !in.ComparisonFailed && in.Plan.Error == "" && !in.Plan.RefsUnknown &&
		in.Plan.Changes() == 0 &&
		!in.SourceDeleted && !in.TargetRemoved && len(in.Plan.Warnings) == 0
}

func controlReasons(in Input) []Reason {
	var reasons []Reason
	if !in.Known {
		reasons = append(
			reasons,
			reason(
				"not_admitted",
				"Awaiting source admission",
				"This is saved history; the repository has not passed current source discovery and filters.",
				"Refresh source discovery or wait for an admitted webhook. Saved history cannot authorize publication.",
				true,
			),
		)
	}
	if in.TargetRemoved {
		reasons = append(
			reasons,
			reason(
				"target_not_configured",
				"Destination no longer configured",
				"Only retained results remain; current configuration no longer mirrors this target.",
				"Restore the intended target configuration if publication should resume; otherwise keep history for reference.",
				false,
			),
		)
	}
	if in.Pause != nil && (in.Pause.Until.IsZero() || in.Now.Before(in.Pause.Until)) {
		reasons = append(
			reasons,
			reason(
				"publication_paused",
				"Paused",
				"This scope is paused. Current source changes are held back.",
				"Resume the relevant repository/target hold, or wait for expiry and a subsequent run.",
				true,
			),
		)
	}
	if in.Plan != nil && in.Plan.ApprovalRequired {
		reasons = append(
			reasons,
			reason(
				"approval_required",
				"Awaiting first-publication approval",
				"This source/destination identity has no publication grant.",
				"Review the destination, then use Approve destination and sync. A grant authorizes ongoing publication.",
				true,
			),
		)
	}
	if in.StateUnavailable {
		reasons = append(
			reasons,
			reason(
				"state_unavailable",
				"Durable state unavailable",
				"New syncs require a successful durable checkpoint before remote mutations.",
				"Restore writable database storage, then request a fresh sync.",
				true,
			),
		)
	}
	return reasons
}

func activityReasons(in Input) []Reason {
	var reasons []Reason
	if in.Running {
		title, message := "Checking current state", "A read-only comparison is running for this repository."
		if in.Applying {
			title, message = "Sync run in progress", "A sync run is active for this repository; final per-target outcomes are not available yet."
		}
		reasons = append(
			reasons,
			reason(
				"run_in_progress",
				title,
				message,
				"Wait for the current run to finish; inspect the resulting comparison and operation outcomes.",
				false,
			),
		)
	}
	if in.Queued {
		reasons = append(
			reasons,
			reason(
				"follow_up_queued",
				"Follow-up work queued",
				"One coalesced follow-up run will recompute current source state.",
				"Wait for the queued run; a queued comparison alone does not authorize publication.",
				false,
			),
		)
	}
	return reasons
}
