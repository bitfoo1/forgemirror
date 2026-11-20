# Persistent Sync Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the latest sync attempt and fresh comparison across restarts, with full-width, non-disruptive web UI updates.

**Architecture:** A pure-Go SQLite package stores versioned JSON status records transactionally. Status keeps separate attempt/comparison records; the dispatcher checkpoints before remote writes and verifies after them. Existing HTMX fragment polling uses keyed reconciliation rather than destructive swaps.

**Tech Stack:** Go, database/sql, modernc.org/sqlite, HTMX, plain JavaScript, Node's test runner/browser tests where available, mise.

**Spec:** `docs/superpowers/specs/2026-11-20-persistent-sync-review-design.md`

## Global Constraints

- Credentials and unredacted secrets never reach disk or logs.
- CGO-disabled releases remain supported.
- Exactly one writer per database; empty `state.database` keeps in-memory mode.
- Current comparisons never overwrite historical attempts; restored comparisons are stale.
- Remote writes require a successful persistent checkpoint when enabled.
- Use existing filters, clone URL validation, concurrency and shutdown policies.
- No commits until the full project gate passes; no pushes without user request.

## Review Focus

- Interrupted writes/restarts must not appear successful (tasks 1–3).
- Changed source/targets/filters must not reactivate old sync destinations (task 3).
- Partial failure and failed verification must not show false in-sync status (tasks 2–4).
- Polls during selection or in-flight actions must not lose state or overwrite newer content (task 4).
- Database open/ownership/write errors must fail explicitly, without leaking data (tasks 1–3).

### Task 1: SQLite record storage

**Files:** create `internal/state/state.go`, `internal/state/state_test.go`; modify `go.mod`, `go.sum`.

**Interfaces:** `state.Open(ctx context.Context, path, scope string) (*DB, error)`, `(*DB).Load(ctx) (map[string][]byte,error)`, `(*DB).Save(ctx,name string,data []byte) error`, `(*DB).Close() error`.

- [ ] Write tests for durable reopen, scope isolation, second-owner rejection, unsupported schema, invalid path and corruption.
- [ ] Run selected tests and observe missing API failure.
- [ ] Implement versioned transactional SQLite storage with private permissions, WAL, FULL sync and exclusive process ownership.
- [ ] Run storage tests with the race detector; expected PASS.

### Task 2: Separate attempts, comparisons and verification

**Files:** modify `internal/status/status.go`, add `internal/status/persistence.go` and tests; modify `internal/dispatch/dispatch.go`, add verification tests.

**Interfaces:** status options accept storage and logger; `Store.Restore(ctx) error`, `Store.Checkpoint(ctx,p plan.Repo) error`; snapshot repositories expose `LastAttempt`, `Stale`, and storage error. Dispatcher options accept an optional checkpoint callback and enable post-apply verification.

- [ ] Write tests retaining an attempt across comparisons and restart; test interrupted recovery, result isolation and save failure.
- [ ] Run tests to observe failures.
- [ ] Implement synchronous durable status updates, safe DTOs, independent lifecycle outcomes and checkpoint gating.
- [ ] Write dispatcher tests for verification after successful/failed apply, no repeated application, and checkpoint failure blocking mutations.
- [ ] Observe failures, implement one read-only verification within serialized run, then rerun relevant tests; expected PASS.

### Task 3: Configuration, startup and recovery

**Files:** modify `internal/config/{config,effective}.go`, tests/schema; `internal/app/app.go`, tests; `internal/control/control.go`; add focused recovery helpers as needed.

**Interfaces:** `Config.State.Database` (`state.database`); source-scoped storage opens before serving and closes after dispatcher shutdown; restored repository metadata passes current admission policy before dispatch.

- [ ] Write config and service tests for explicit persistence, restoration, startup failure and changed configuration.
- [ ] Observe failures, implement wiring and read-only startup recovery without enabling implicit writes.
- [ ] Run config/app/control tests; expected PASS.

### Task 4: Full-width UI and stable live updates

**Files:** modify `internal/ui/{view,ui}.go`, `templates/ui.html`, `static/{app.js,style.css}`; add Go/JavaScript tests and mise UI test task.

**Interfaces:** stable DOM keys, separate current/historical target views, state-aware labels and stale errors; reconcile HTMX fragments while preserving unchanged DOM and deferring selected/focused content.

- [ ] Write rendering and JavaScript regression tests for independent sections, stale/partial/interrupted states, expansion/selection/focus retention, queued responses and responsive layout.
- [ ] Observe failures, implement keyed reconciliation and full-width layout.
- [ ] Run UI tests, JavaScript tests and browser checks if tooling is available; expected PASS, explicitly record unavailable browser coverage.

### Task 5: Deployment, documentation and final verification

**Files:** config example/schema, docs UI/config/operations/security, Helm/Kustomize examples and READMEs; mise `check` dependencies if needed.

- [ ] Document persistence setup, one writer, volume lifetime, backup/restore and restart semantics; provide opt-in persistent deployment examples.
- [ ] Run `mise run fmt`, `mise run check`, and a CGO-disabled build; fix all findings.
- [ ] Review the complete branch against the spec and tests; rerun affected tests for any fixes.
- [ ] Report exact verification evidence and limitations. Leave changes on the feature branch without pushing.
