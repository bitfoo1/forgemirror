package publication

import (
	"go.bitfoo.de/forgemirror/internal/status"
)

func retryReasons(in Input) []Reason {
	if in.TargetRemoved {
		return nil
	}
	if in.Retry == nil || (in.Running && in.Applying) {
		return nil
	}
	job := in.Retry
	if job.Automatic && !in.Auto && pendingRetry(in) {
		return []Reason{
			reason(
				"retry_review_hold",
				"Automatic retry held for review",
				"This retry originated in automatic mode and is suspended by the current review-mode policy.",
				"Use Sync to authorize current state, or intentionally restore automatic mode.",
				true,
			),
		}
	}
	switch job.State {
	case "waiting", "paused":
		next := reason(
			"retry_scheduled",
			"Waiting to retry",
			"Pending work will be re-planned by the retry scheduler when eligible and admitted.",
			"Wait for the retry, or use Sync to request a fresh attempt and reset its budget.",
			true,
		)
		next.At = job.Next
		return []Reason{next}
	case "exhausted":
		return []Reason{
			reason(
				"retry_exhausted",
				"Automatic retries exhausted",
				"The bounded retry budget has been used up.",
				"Resolve the underlying problem, inspect the last attempt, then use Sync to reset the budget.",
				true,
			),
		}
	case "needs intervention":
		return []Reason{
			reason(
				"sync_failed",
				"Operator intervention required",
				"The last failure was not classified as safely retryable.",
				"Inspect operation results and fix credentials, configuration, quota or source data before Sync.",
				true,
			),
		}
	default:
		return nil
	}
}

func pendingRetry(in Input) bool {
	return in.Retry != nil && (in.Retry.State == "waiting" || in.Retry.State == "paused")
}

func comparisonReasons(in Input) []Reason {
	if in.TargetRemoved {
		return nil
	}
	var reasons []Reason
	if in.ComparisonFailed || (in.Plan != nil && in.Plan.Error != "") {
		reasons = append(
			reasons,
			reason(
				"comparison_failed",
				"Comparison failed",
				"Current destination state could not be verified.",
				"Restore source/destination access, then Refresh. Do not treat saved results as current.",
				true,
			),
		)
	}
	if in.Stale {
		reasons = append(
			reasons,
			reason(
				"verification_pending",
				"Awaiting verification",
				"The saved or pre-sync comparison may be outdated.",
				"Refresh the current comparison or wait for the active verification run.",
				true,
			),
		)
	}
	if in.SourceDeleted && (in.Plan == nil || !in.Plan.Delete) {
		return append(
			reasons,
			reason(
				"source_deleted",
				"Source reported absent",
				"A deletion event or source lookup reports the repository absent; verify source visibility, target deletion policy and retained history.",
				"Verify source permissions and availability, then Refresh. Review target deletion policy separately.",
				false,
			),
		)
	}
	if in.Plan == nil {
		return append(
			reasons,
			reason(
				"never_checked",
				"Not checked yet",
				"No current per-target comparison is available.",
				"Refresh this repository to discover pending work.",
				true,
			),
		)
	}
	if len(in.Plan.Warnings) > 0 {
		reasons = append(
			reasons,
			reason(
				"comparison_warning",
				"Comparison has warnings",
				"Some configured features were skipped or normalized; ref agreement is not a complete feature check.",
				"Inspect the target warning list and per-feature token permissions, then Refresh. Other permitted writes may still proceed.",
				false,
			),
		)
	}
	if in.Plan.Changes() > 0 {
		reasons = append(reasons, pendingChangesReason(in))
	}
	if in.Plan.RefsUnknown {
		reasons = append(
			reasons,
			reason(
				"refs_unknown",
				"Git refs not verified",
				"The destination's Git refs could not be compared; an apply may require a full push.",
				"Check remote availability and inspect the plan warning before syncing.",
				false,
			),
		)
	}
	return reasons
}

func pendingChangesReason(in Input) Reason {
	authorizedRetry := pendingRetry(in) && !in.Retry.Automatic
	activeSync := in.Running && in.Applying
	if !in.Auto && !activeSync && !authorizedRetry {
		return reason(
			"review_required",
			"Waiting for manual sync",
			"Review mode leaves the current changes unapplied.",
			"Review the current diff, then use Sync. Sync re-plans current state rather than replaying a saved snapshot.",
			true,
		)
	}
	return reason(
		"changes_pending",
		"Changes pending",
		"The current comparison contains unapplied changes.",
		"Sync now, or wait for the next enabled trigger. A read-only Refresh does not itself queue publication.",
		false,
	)
}

func addLFSEvidence(view *View, last *status.Target, active bool) {
	if last == nil {
		return
	}
	for _, operation := range last.Operations {
		if operation.Name != "lfs" {
			continue
		}
		view.LFSAt = operation.At
		view.LFSSucceeded = operation.Error == ""
		if active && operation.Error != "" {
			view.Reasons = append(
				view.Reasons,
				reason(
					"lfs_transfer_failed",
					"LFS recovery not verified",
					"The last LFS step failed. Matching Git refs cannot prove that missing objects were repaired.",
					"Inspect the LFS result, then use Repair LFS or let an eligible publication retry reconcile the objects.",
					true,
				),
			)
		}
	}
}
