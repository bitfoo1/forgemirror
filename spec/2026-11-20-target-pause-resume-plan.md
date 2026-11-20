# Target Pause/Resume Design and Implementation Plan

The user authorized implementation without additional approval questions.

**Goal:** Persist whole-target and repository-target pauses with reasons and optional expiry, continuing read-only planning but preventing new remote mutations.
**Architecture:** Add separately namespaced control records to existing SQLite-backed status, a read/write mutation barrier in the store, dispatcher mutation permits, and admitted POST UI actions. A pause waits for active operations to complete before acknowledgement; subsequent operations are blocked. Resume dispatches a fresh Sync.
**Spec:** ordered roadmap, increment 2. Stacked on Git LFS PR #47.

## Tasks
- [x] Write failing tests for pause scope, expiry, restart, failed persistence and operation barrier; implement control persistence and status methods.
- [x] Write dispatch regression tests proving a pause prevents Git/LFS and all forge mutation paths while planning continues; implement mutation permits.
- [x] Add controller/UI actions and tests for scope validation, CSRF, POST, reason/expiry. Render pause state and global controls.
- [x] Update documentation, run full gate and self-review.
- [x] Commit and open PR 2 (#48).
- [x] User correction: make SQLite mandatory by default, including deployment
  volumes, schema validation and startup without a memory-only fallback.
  Source API credentials remain optional for webhook-only operation.

## Ledger
- Database correction: default `data/state.db`; Helm/Kubernetes use
  `/var/lib/forgemirror/state.db` with persistent storage enabled by default.
  Config RED reproduced the previous empty default; full gate passed after
  implementation with 90.9% internal coverage and all 5 browser tests.
- `mise run check` passed: 90.9% internal coverage, 5 browser tests, all other gates.
- Ruling: expiry removes admission holds on subsequent runs; it does not introduce a new scheduling engine. Document scheduled sync for automatic catch-up.
- Review found a possible permit/API-slot lock inversion; all mutation paths now acquire permits before process/API slots. Self-review only (no reviewer tool available).
- Dispatch coverage was added as regression coverage after the permit interface was introduced; it was not a separate observed RED cycle.

## Constraints and review focus
- No cancellation of an already active operation; pause acknowledgement waits for it.
- Global pause overrides a repository resume; expiry is evaluated at permit acquisition.
- Durable write failure must not acknowledge an unpersisted control change.
- Resume uses current source state, never replays a saved plan.
- Keep control records separate from repository status; no secret values in reasons or errors.
