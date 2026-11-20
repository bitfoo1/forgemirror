# Explainable Publication Status — Design and Plan

**Goal:** Each destination explains why publication is held or pending, what is already known, and the next automatic/operator action without confusing current state with historical failures.
**Architecture:** A pure publication-explanation model consumes current comparison, admission, pause/approval state, durable retry state and completed history. UI target cards render stable reason codes, primary status, all contributing reasons, next action, last verified comparison and last successful target run.

## Decisions
- Priority: current source admission, pause, approval, durable-state availability, active run, retry/intervention, comparison/review/pending work. Keep all contributing reasons visible.
- Ref/metadata agreement does not prove LFS health. Surface failed LFS steps and label successful steps as historical evidence, not perpetual object availability.
- Automatic-origin retries are visibly suspended in review mode; explicit operator retries remain eligible. Out-of-sync does not falsely promise a queued automatic run after a read-only refresh.
- Historical failures stay in history/results; a verified fresh comparison can supersede an old push error. Pending verification never becomes an in-sync claim.
- Stable machine-readable `data-reason` codes use no error-text parsing. No new mutation endpoints or authentication model.

## Tasks
- [x] RED/GREEN pure explanation cases for combined blockers, retries, review mode, unknown/stale comparisons, matching refs, LFS evidence and historical failures.
- [x] RED/GREEN per-target last-success evidence and UI reason rendering, including held-destination summaries and existing action links.
- [x] Docs/roadmap completion and whole-stack self-review.
- [x] Final full gate: all checks passed, 89.7% internal coverage, 5 browser tests,
  strict docs/link validation, vulnerability scan and deployment/chart checks.
- [ ] Commit and PR 8.

## Ledger
- RED tests covered missing explanation API/UI, target-success evidence and
  successful no-op evidence. Pure cases distinguish combined holds, source
  admission, current-vs-historical failures, review retry intent, LFS scope,
  target retirement and comparison warnings.
- Integration race: an apply-finished log precedes post-sync verification.
  The approval test now waits for the required identity-bearing UI state;
  it passed ten repeated race-enabled runs.
- Browser regression: nested reason disclosures made descendant summary
  selectors ambiguous. Direct-child summary selectors preserve the actual
  selection, disclosure and focus assertions; all 5 browser tests passed.
- User follow-up: provider permissions now have structured Configuration
  tables, verified against official provider docs and adapter calls. All live
  references were moved, and all eight feature docs/defaults were audited.
  See `2026-11-20-documentation-audit.md`.
- Final `mise run check` passed after all code/docs edits: 89.7% internal
  coverage, 5 browser tests, 17 strict-built documentation pages and zero link
  errors. Lint, vet, tidy/format, vulnerability and deployment/chart gates passed.
- Whole-stack review checked mutation permits, durable state/versions, retry
  authorization/scoping, notification independence, LFS evidence, identity
  binding and docs consistency. No independent reviewer tool is installed.
- Packaging limitation remains explicit in PR #47: both Linux LFS binaries
  cross-built and GoReleaser config validated; full container execution was
  not exercised on this host.
