# Publication Holds Implementation Plan

> Execute inline with test-driven development using executing-plans. The implementation and real UI preview were reviewed; the user subsequently approved committing and opening a pull request.

**Goal:** Implement the approved H+C publication-controls card with persisted multi-target/repository/glob holds.
**Architecture:** Add shared scope validation/matching, extend persisted pauses with stable IDs/scopes, and add atomic scoped controller actions. Render a separate live controls pane with a client-owned four-step draft and server-owned Current holds list.
**Tech stack:** Go, HTML templates, vanilla JavaScript/CSS, SQLite records, Playwright, mise; no new dependencies.
**Spec:** `docs/superpowers/specs/2026-11-20-publication-holds-design.md`

## Global Constraints

- Until resumed is the default. Empty target/repository selections never mean all.
- Resume removes only one hold ID and obeys remaining holds/approval/clone URL policy.
- Matching includes future admitted repositories/targets; exact selections still require current admission.
- Persist atomically behind the existing mutation barrier; retain legacy APIs and restore v1–v3 controls.
- Strict CSP, no secrets in errors/logs/disk, native form fallback, bounded input.
- Commit and push only after UI review. No merge, version change, tag, or release.

## Review Focus

- Broad/narrow overlaps and failed persistence cannot accidentally enable publication (task 1).
- Invalid or unadmitted scopes and unknown IDs cannot authorize arbitrary syncs (task 2).
- IDs survive restart and old exact pauses remain removable without deleting new holds (task 1).
- Live polling updates current holds while preserving every wizard/picker draft field (task 3).
- Keyboard, mobile, no-JS, prefix-relative actions, escaping, validation feedback, and future glob matching (tasks 2–3).

## Task 1: Validated durable hold scopes

Files: new `internal/hold/scope.go`, `internal/hold/scope_test.go`; modify `internal/status/pause.go`; new `internal/status/holds.go`, `internal/status/holds_test.go`.
Interfaces: `hold.Scope.Validate() error`, `hold.Scope.Matches(repo,target string) bool`; `Store.AddHold(ctx, scope, reason, until) (Pause,error)` and `Store.ResumeHold(ctx,id) (Pause,error)`. Existing `Pause/Resume/Paused` continue working.

- [x] Write and observe failing tests for bounded exact/glob/all matching, invalid scopes, overlapping independent IDs, future matches, persistence failures, barrier waiting, and legacy restore.
- [x] Implement shared scope, versioned pause migration, atomic creation/removal, and matching.
- [x] Run focused tests, format, and inspect results before task 2.

## Task 2: Controller and server actions

Files: new `internal/control/holds.go`, `internal/control/holds_test.go`; modify `internal/ui/ui.go`, add `internal/ui/holds.go` and handler tests; update UI action fakes.
Interfaces: `PauseScope(ctx, hold.Scope, reason string, until time.Time) error`, `ResumeHold(ctx,id string) error`; POST `actions/hold` and `actions/resume-hold`; GET `fragments/controls`.

- [x] Write failing tests for all-or-nothing validation, exact admission, configured targets, matching resume events, clone checks, unknown IDs, request protection and safe errors.
- [x] Implement controller validation and ID-scoped resume; preserve old actions. New actions render controls, use existing toasts, and redirect correctly under URL prefixes.
- [x] Run focused controller/UI tests and full Go suite.

## Task 3: Production H+C controls pane

Files: new `internal/ui/templates/controls.html`, `internal/ui/static/controls.js`, `internal/ui/static/controls.css`; modify `ui.html`, `live.js`, page data/static embeds; update browser fixture to real scoped actions; new `tests/ui/holds.spec.cjs`; update docs.

- [x] Add failing renderer/browser tests for separate card, defaults, tabs/steps, selectable badges/globs, single-select duration, server-side create/resume, draft retention under polling, keyboard/mobile/no-JS, and overlap semantics.
- [x] Implement server view, accessible progressive enhancement, client-owned draft reconciliation, safe matching preview and real current-holds actions.
- [x] Use fixture store/control methods for preview, document durable scopes, and run `mise run check`.
- [x] Review complete diff inline, restart safe preview, verify actual UI, and stop for review without committing.

## Execution ledger

- Workspace: existing feature branch `fix/compact-sync-notice`, clean at start; no extra worktree created.
- No subagent tools available; execute and review inline.
- Pre-flight: task 1 scope and Pause IDs are consumed by controller/UI; canonical flags/IDs must be shared, not inferred from empty form fields. Task 3 submits only task 2 action fields and never loops legacy actions.
- Ruling: add explicit `webhook.Event.Targets` unions and preserve them during dispatcher coalescing. Multiple legacy single-target events could otherwise widen a resume to all targets. A failing regression test now proves the third, unselected forge is not pushed.
- Ruling: allow all-repository holds before discovery; otherwise operators cannot safely stop future publication before the first admission. Globs still require an initial admitted match. A failing controller test pinned this distinction.
- Ruling: bound current holds to 256 and prune expired records when adding. New scopes are independent, so the old per-pair replacement bound no longer suffices.
- RED→GREEN: renderer tests reproduced missing routes/card; browser polling reproduced draft/tab replacement and then hidden server-default tabs. Client-owned form reconciliation plus restoration of enhanced tab visibility fixed both.
- Ruling: preserve separate bounded coalescing slots for ref publication and object-only repair. A failing mixed-action regression showed repair could be upgraded into ref writes; explicit Operator intent now also preserves the read-only/object-only action kind.
- Browser suite adjustment: earlier hold/resume scenarios retain real attempt history, so an old applied-plan summary is not a completion barrier. The existing sync test now waits for the fresh comparison across the documented polling interval rather than timing out at exactly one interval.
- Verification: full `mise run check` passed on the final implementation, including all Go tests with race detection, all 21 browser tests, 89.8% internal coverage, format/tidy/vet/lint/vulnerability checks, strict docs, Kubernetes and Helm validation. `git diff --check` passed.
- Inline final review covered durable migration, failed-save rollback, overlapping/all/future scopes, ID-based removal, clone checks/admission, queue capability isolation, form validation/CSRF/body limits, escaping, prefix-safe native redirects, and client-owned draft reconciliation. No separate reviewer tool is available.
- Actual production UI preview verified at `http://127.0.0.1:9099/` at 1440px and 390px in both themes, with New hold / Current holds (1), the four steps, Until resumed, and the sample Codeberg hold. Preview uses the real store/controller with simulated jobs and no forge connections.
- User approved the real UI and requested commit, push, and a new PR. Renamed the feature branch from `fix/compact-sync-notice` to `feat/publication-hold-controls`; retain the two approved badge/branding commits (`0b811c0`, `3fe1391`) and commit the cohesive scoped-holds implementation after the full gate.
- No merge, version change, tag, or release is authorized.
