# Persistent sync review and non-disruptive web UI

## Intent and agreed scope

Operators must be able to review both what the last sync attempted and what
still differs now, without losing either record when ForgeMirror restarts.
The web UI must use available screen width and update asynchronously without
periodically destroying text selections, expanded sections, or focus.

The user selected SQLite after considering Redis/Valkey, atomic JSON files,
object storage, and other backends. This document proposes the remaining
implementation choices for approval.

## Current behaviour

- `internal/status/status.go` holds one plan per repository in memory.
  `PlanFinished` replaces that plan before execution; results arrive separately.
- `internal/dispatch/dispatch.go` computes and optionally executes a plan, but
  does not automatically compare source and targets after execution.
- `internal/ui/templates/ui.html` polls three HTML fragments every five seconds
  using HTMX and replaces their contents with `innerHTML`. It is asynchronous,
  but replacing DOM nodes destroys browser selection and focus.
- `internal/ui/static/style.css` limits the page to 82rem and places targets in
  a multi-column grid.
- Dispatcher repository identities are also in memory. Loading display state
  alone would not restore working refresh/sync actions.
- Releases build with `CGO_ENABLED=0`, so SQLite must not introduce a C compiler
  or dynamically linked SQLite requirement.

## UI and state model

Each repository has two independently retained records:

1. **Current diff**: the latest read-only comparison, its timestamp, whether it
   is stale or being verified, and any verification error.
2. **Last sync attempt**: an immutable plan for that attempt, start/finish times,
   lifecycle status, and target/operation results belonging to that attempt.

Display the historical section as **Last applied plan** only for a successful
attempt. Use **Last sync attempt** for running, failed, partially failed, or
interrupted attempts. A plan describes intent, not proof of completed changes.
Keep both sections visible after refresh and restart. Default the current diff
open when it has changes and the historical section closed, while preserving
explicit user expansion choices for both sections independently.

Example:

```text
owner/repository                                      In sync
  v Current diff                                     Checked just now
    github: no changes
    codeberg: no changes
  > Last applied plan                                Synced at 14:32
    3 branches deleted, main updated; target results available inside
```

A successful no-op sync replaces the latest attempt just like any other sync;
read-only comparisons never replace it. Retain one attempt and one comparison
per repository, not a general audit history. Preserve deletion outcomes as
history, but do not offer invalid actions for repositories no longer admitted
by the current source/filter configuration.

## Sync lifecycle and correctness

1. Persist the attempt and its plan before any remote mutation. If that durable
   write fails, do not start applying the plan.
2. Record outcomes against that attempt, not against mutable global last-result
   timestamps. Serialize runs for each repository as today.
3. Finish the attempt with explicit success/failure/partial/interrupted status.
   Do not report a persisted success if persisting completion failed.
4. Perform one read-only verification after each applying run, including failed
   or partially failed runs. It must not trigger another application or loop.
5. Publish the resulting current diff separately. While verification is pending,
   the previous comparison is stale, not authoritative. A failed verification
   means **Could not verify**, never an inferred **In sync**.

Preserve existing follow-up event coalescing: a newer queued sync, push, or
repository deletion must not be overwritten by a verification refresh. Bound
verification with the existing concurrency and timeout controls. During
shutdown do not start unbounded new work; any unverified record remains stale.

## SQLite persistence

Introduce a focused persistence package using `database/sql` and a pure-Go
SQLite driver compatible with the pinned Go version and CGO-disabled releases
(proposed: `modernc.org/sqlite`, version chosen and pinned during implementation).
Keep database access out of templates and HTTP handlers.

Use a schema version with transactional migrations. Store explicitly defined,
versioned DTOs for comparisons, attempts, results, and safe repository identity,
not arbitrary runtime objects or configuration. JSON payload columns inside
SQLite are acceptable for nested plan data; use relational keys and transactions
for repository/attempt identity and lifecycle updates. Preserve existing plan
truncation and omitted-item counts so retaining two records does not make
individual plans unbounded.

Proposed configuration: `state.database`, a filesystem path. Empty means the
existing in-memory mode for backwards compatibility. Document clearly that
restart persistence requires setting this path, and configure the persistent
examples with `/var/lib/forgemirror/state.db`. Persistence applies even when the
UI listener is disabled, so history remains coherent when UI visibility changes.

Use a private state directory, SQLite WAL, foreign keys, a bounded busy timeout,
and durable commits (`synchronous=FULL`). Support one ForgeMirror process per
database; reject a second owner rather than implying multi-replica coordination.
Do not put the database on a shared/network filesystem. Open/schema/ownership
errors fail startup when persistence is configured; do not silently fall back
to memory. Runtime failures must be surfaced in logs and UI state and must not
permit unrecorded new applications. Drain dispatcher work before closing storage.

Credentials, HMAC secrets, authenticated URLs, effective configuration, and
unredacted error text must never be written. Persist repository names and safe
identity fields; reacquire authoritative repository metadata before enabling
restored actions. Scope cached records to source identity and invalidate current
comparisons when relevant target/filter configuration changes. Removed targets
may remain named in history, but cannot become active sync destinations.

## Restart recovery

Load retained records immediately. Mark saved current comparisons stale and
unfinished attempts interrupted, without automatically resuming remote writes.
Reconcile restored repository identities with the current source, clone-URL
policy, and filters, then enqueue bounded read-only comparisons. This must also
work when scheduled sync is disabled and auto-sync is enabled. Never reset
process uptime or restore a running flag from the previous process.

If the source cannot currently be reached, history remains readable with an
explicit stale/error indication. No remote writes are caused merely by loading
saved state. A database made by an unsupported newer schema fails startup with
a clear error rather than being reset or overwritten.

## Non-disruptive asynchronous updates

Retain the existing same-origin HTMX fragment endpoints and five-second polling;
network polling itself is not a full-page reload. Replace unconditional swaps
with incremental, keyed DOM reconciliation. Give repository, target, attempt,
and diff sections stable identifiers. Preserve unchanged nodes, focus, open
state, and scroll positions, and suppress swaps when semantic content is
unchanged. Defer updates affecting an active text selection until it clears;
coalesce to the latest pending response and prevent older responses from
replacing newer state. Do not lose explicit action responses while deferring.

After an action, show its progress promptly and poll while it completes. Display
the new attempt and verification results without a manual page refresh. Keep
plain HTML forms as the no-JavaScript fallback and preserve current CSP and
cross-origin protection. Use delegated event handling for updated content.

## Layout

Remove the 82rem main-width cap and align header/content with consistent small
side gutters. Stack target diffs in full-width rows rather than narrow cards.
Retain responsive wrapping and local horizontal scrolling for genuinely long
content; do not introduce page-level horizontal overflow on small screens.

## Deployment and documentation

Keep persistent state separate from disposable clone workspaces. Update config
examples, generated schema, operations/security/UI docs, and deployment guides.
Provide an explicit Helm/Kustomize persistent-volume setup and ownership/security
settings for the non-root container. Document that a container writable layer or
`emptyDir` does not survive replacement, and that enabling persistence needs a
local persistent volume and one writer. Backups must use a consistent SQLite
backup mechanism or stop the service; copying only the main file during WAL
writes is not a valid backup procedure.

## Verification

- Table-driven parallel Go tests with fakes for remote systems and temporary
  databases for persistence; race detector and existing coverage gate.
- Reopen tests for both views, independent attempt results, interrupted recovery,
  rollback, migration/version rejection, changed configuration, unavailable
  source, and error/secret handling.
- Dispatcher tests for success, partial failure, verification failure, deletion,
  newer queued events, cancellation, and persistence failure before mutation.
- UI tests for separate historical/current sections, stale/failed verification,
  restored actions, and matching per-attempt outcomes.
- Browser-level regression tests for selection/focus/expansion/scroll retention,
  unchanged polls, out-of-order responses, post-sync verification, and layout at
  narrow and wide viewport sizes. Integrate any new test tooling via mise tasks.
- `mise run check` and a CGO-disabled build before completion. Do not claim
  browser verification unless those tests actually ran.

## Non-goals

Unlimited history, distributed workers, automatic write resumption after a
restart, persistent UI log archives, external database services, server-sent
events, and changes to mirror filtering or publication semantics.
