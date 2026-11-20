# UI Activity Indicators Implementation Plan

> Execute inline with test-driven-development. The user requested visible progress for repository and bulk refresh/sync; their standing instruction is to proceed without further approval questions.

**Goal:** Immediate, accessible request feedback and truthful spinning indicators while repository work remains active.
**Architecture:** CSS-only spinners in the four refresh/sync forms, plus server-rendered repository/global activity derived from the union of dispatcher busy names and status-store running records. Keep active HTMX request classes during incremental reconciliation; polling itself must never activate action spinners.
**Tech Stack:** Existing Go templates, CSS, embedded HTMX/live reconciliation, Go tests and Playwright; no new dependencies or build step.
**Spec:** Design decisions below.

## Design decisions

- Use CSS rings, not an external icon/animation library. Visible text accompanies decorative icons; `prefers-reduced-motion` disables rotation without hiding status.
- Button indicators cover HTTP submission only. Repository and aggregate indicators continue from actual background state until a later live update observes completion/failure. Do not equate HTTP acknowledgement with completed mirroring or invent percentage progress.
- Distinguish Refreshing, Syncing and Starting (dispatcher accepted but observer has not started yet). Mixed global work uses a generic active-repository count.
- Deduplicate dispatcher/status counts. Do not spin while merely waiting for retry, pause or approval.
- Preserve selection, expanded details, focus and HTMX request classes during keyed reconciliation. Add stable keys to action forms.
- No-JavaScript forms retain native submission behavior; background completion still requires manual reload without live polling.
- Deliver as a follow-up PR based on #55 (or main if merged), preserving existing worktrees and the user's checkout.

## Review focus

- Very fast completion and the dispatcher/observer hand-off must not leave a stuck wheel.
- Background polling must not light up every action button.
- Delayed/action-error responses and deferred reconciliation must clean up or preserve the correct request indicator.
- Bulk work must remain visible until the last active repository finishes, without marking idle peers busy.
- Reduced-motion users need visible text and no rotation; no CSP-relaxing inline styles/scripts.

## Tasks

### 1. Server activity projection and markup
**Files:** `internal/ui/activity.go`, `activity_test.go`, `view.go`, `ui.go`, `templates/ui.html`, `static/style.css`.
**Interfaces:** add `repoView.Activity *activity` and `pageData.Activity string`; `populateActivity(*pageData, map[string]bool)` deduplicates active names and sets `Running`/`Queued` consistently.
- [x] Write rendered-UI tests for refresh, sync, dispatcher-starting, mixed/deduplicated aggregate state and completed/failed idle state. Observe RED using `mise run test -- -run TestActivity`.
- [x] Implement the projection, scoped request spinners, keyed forms and background indicators. Add reduced-motion CSS.
- [x] Verify selected tests and the Go suite.

### 2. Browser lifecycle and reconciliation
**Files:** `internal/ui/static/live.js`, `internal/ui/testdata/server/main.go`, `tests/ui/live.spec.cjs`.
- [x] Add deterministic fixture hold/finish/seed controls and failing browser cases for immediate request feedback, background continuation, bulk scope, polling isolation, request errors and reduced motion.
- [x] Preserve the transient `htmx-request` class across reconciliation; keep existing focus/selection behavior.
- [x] Run `mise run test:ui` and fix only demonstrated failures.

### 3. Docs, verification and delivery
**Files:** `docs/content/docs/web-ui.md`, this plan.
- [x] Document submission-vs-background progress, aggregate scope, update cadence and reduced motion/no-JS behavior.
- [x] Self-review and full `mise run check`: passed, 89.7% internal coverage,
  all 10 browser tests, strict docs/link checks and all other gates.
- [ ] Commit and open the follow-up PR. No independent reviewer tool is available.

## Execution ledger

- Isolated worktree: `.superpowers/worktrees/activity`, branch `feat/ui-activity-indicators`, based on `feat/publication-explanations` (#55).
- Baseline `mise run test`: passed before implementation.
- RED: rendered tests showed missing refresh/sync/starting/aggregate indicators.
  GREEN: projection and markup cover dispatcher/observer hand-off, deduplication,
  mixed phases, idle peers and failed completion.
- RED: browser test showed missing request `aria-busy` state. GREEN: pending
  feedback now survives a reconciled snapshot and clears on success/error;
  polling does not activate action indicators.
- Deterministic test-only hold/finish/seed controls let browser tests prove
  bulk progress remains until the last repository completes, without timing
  sleeps or any new production control endpoints.
- Reduced-motion browser coverage confirms a static ring with visible status.
  Existing selection, focus, disclosure and responsive-layout tests still pass.
- Documentation records five-second/deferred-update and no-JavaScript limits.
  Global activity describes service-wide work, not a fictional batch percentage.
