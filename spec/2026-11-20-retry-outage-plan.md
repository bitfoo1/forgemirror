# Retry and Outage Handling — Design and Plan

**Goal:** Automatically recover transient target failures without occupying worker slots or retrying permanent/operator-blocked failures.
**Architecture:** Typed retry metadata at HTTP/CLI boundaries; bounded jittered backoff and durable per-repository/target entries in the existing controls namespace; an idle dispatcher loop re-admits due jobs through current known repositories and target configuration and always re-plans. No stored source URL or credentials are executed.

## Decisions
- Five retries, starting at 30 seconds; exponential delay capped at 15 minutes, plus non-negative jitter. Honor Retry-After as a lower bound.
- Network timeouts, recognized CLI connection failures, HTTP 429/5xx and explicit rate-limit responses are retryable. Authentication, validation, unsupported endpoints and unknown failures require intervention.
- Pause does not consume attempts; source deletion supersedes pending normal work. Removed/unadmitted repositories never execute restored jobs. Auto-origin retries obey current review mode; explicit Sync/Repair retries retain operator intent.
- A retry re-plans and skips completed refs/artifacts. Existing create and content-addressed transfer operations reconcile remote state before repeating; ambiguous non-idempotent release operations must be verified by fresh planning.
- Retry timing and exhaustion appear in UI; state is mandatory. Shutdown cancels the scheduler before draining active operations.

## Tasks
- [x] RED/GREEN retry classification and backoff with deterministic clocks/jitter; wire HTTP response and CLI metadata without exposing raw diagnostics.
- [x] RED/GREEN durable retry state, pause/exhaustion/reset and restore.
- [x] RED/GREEN due-job admission, target scoping, review mode, single-run/coalescing and shutdown; wire dispatch results and startup loop.
- [x] UI/docs and self-review.
- [ ] Final full `mise run check`, commit, PR 3.

## Ledger
- Tests observed RED for missing retry APIs, joined Retry-After lower bounds,
  and a plan-only event widening a target-scoped sync. Implementations corrected
  these cases. Per-target results and repair-only intent stay separate.
- Controls schema version 2 reads version 1 and rejects malformed retry jobs;
  old binaries reject new controls rather than silently dropping scheduling.
- Full gate passed once with 90.4% internal coverage; subsequent review fixes
  require another final gate. No independent reviewer tool is installed.
- Ruling: no global forge circuit breaker in this increment. Response-specific
  Retry-After is respected per queued target job; new operator/source events
  deliberately reset their retry budget.
- Ruling: source-deletion retries after restart wait for trusted admission,
  rather than reconstructing executable source data from the database.
