# Git LFS mirroring — design

Status: draft for approval. User decisions: use the official `git-lfs` CLI;
LFS mirroring is automatically enabled. Remaining behavior below is the
recommended design, not yet approved.

## Intent

A public mirror must contain the LFS objects referenced by the Git history
it publishes, not just pointer blobs. A failure to transfer those objects
must prevent that destination's new Git refs from being pushed.

This is the first increment of `2026-11-20-mirroring-roadmap.md`.

## Current behavior and integration points

- `internal/mirror/git.go` creates one temporary bare mirror clone and pushes
  branches and tags to multiple destinations. Hidden refs are not published.
- `internal/dispatch/dispatch.go` plans, prepares typed destinations, clones
  once when refs differ, pushes in parallel and then syncs other artifacts
  and settings for successful targets.
- Git commands use bounded contexts, sanitized environments and URL-scoped
  credentials. The workspace is removed after the run.
- `filter.include_branches` controls event admission, not published refs.
  This feature must not change that behavior or imply otherwise.
- Ref comparisons skip unchanged repositories without cloning. UI Refresh
  is read-only, and Sync all selects repositories with planned changes.
- Status persistence retains comparisons and the latest attempt; operation
  results already appear in target details.
- Releases use GoReleaser/ko with a Git-capable non-root base image; LFS
  availability in that image must not be assumed.

## Goals

- Automatically transfer LFS objects using the official CLI, with no
  repository opt-in or new forge API requirement.
- Include objects reachable through the complete history of every published
  branch and tag, including historical files deleted from current trees.
- Download source objects once per workspace and reuse them across targets.
- Complete destination LFS uploads before pushing that destination's refs.
- Isolate destination failures and expose LFS-specific outcomes.
- Repair LFS content even when Git refs already match.
- Preserve cheap planning and the normal unchanged-ref fast path.

## Non-goals and anti-goals

- No rewriting pointers, migrating ordinary Git blobs into LFS or editing
  history. No source modifications or LFS object deletion/pruning.
- No permanent local object cache, byte-level progress UI, background retry
  engine or new generic history system in this increment.
- No LFS content scanning; secret scanning is a separate design.
- No assertion that ref equality or successful Git verification proves LFS
  health. No silent fallback to pushing pointers after an LFS failure.
- No following repository-selected custom endpoints or transfer executables.

## Approaches

1. **Official CLI orchestration — selected by the user.** Reuse protocol,
   pointer discovery and transfer behavior from `git-lfs`; integrate it into
   the existing workspace lifecycle and dispatcher.
2. **Native Go implementation — rejected.** Would require maintaining batch
   protocol handling, object discovery, transfer and authentication logic
   without a compelling project benefit.

## Runtime and configuration

LFS is on automatically, with no enable switch in the initial version.
`git-lfs` is resolved through PATH, like Git's normal external subcommands;
`mirror.git_binary` continues selecting Git. Operators of binary deployments
must install a supported `git-lfs` alongside Git. Missing LFS is a startup
error with an actionable dependency message, rather than a silent downgrade.

Pin Git LFS in mise and its lockfile. Ship it in both linux/amd64 and
linux/arm64 runtime images. Keep the existing daemonless release build and
non-root runtime. Packaging must prove the final image can execute both Git
and Git LFS; do not assume the current base image includes LFS. The exact
compatible image/layer construction is an implementation packaging choice,
subject to those requirements and verification before implementation lands.

Retain current per-command timeout semantics, rather than silently changing
them to a whole-run deadline. Apply `mirror.timeout` to LFS commands as well.
Document that large initial transfers may need a higher timeout and more
space under `mirror.work_dir`.

## Object scope and command orchestration

Capture the cloned workspace's branches and tags as the publication snapshot.
Enumerate LFS pointers across their reachable history, deduplicate object IDs,
and fetch the required objects from the source once. Do not fetch objects
reachable only from hidden refs such as `refs/pull/*` merely because the bare
clone contains those refs.

Use the official CLI's ref-scoped all-history fetch/push behavior, selecting
only the captured heads and tags. Where invocation size requires batching,
bound argument sizes without widening the ref scope. The implementation plan
must pin exact commands against the selected CLI's documented behavior;
blind unscoped `--all` over the entire mirror clone is not acceptable.

Do not install hooks globally or run `git lfs install` against operator
configuration. Explicitly orchestrate LFS rather than depending on a pre-push
hook. No working-tree checkout is necessary. A snapshot with no LFS pointers
is a successful no-op and must not require an LFS-capable remote endpoint.

Source fetches finish before any parallel destination uploads. Avoid shared
mutable repository configuration during concurrent uploads; supply endpoint
and command settings per invocation. Objects are read-only during uploads.
Remove the workspace, including LFS objects and diagnostic files, on all
completion paths. Limit internal LFS transfer concurrency so CLI fan-out does
not multiply the dispatcher's configured process budget unexpectedly.

## Publication order and failures

For destinations needing a Git push:

1. Compute the existing read-only plan and checkpoint as today.
2. Prepare destination repositories as needed; a missing repository may need
   creation before its LFS API can accept uploads.
3. Clone the source once and prepare/download its required LFS objects.
4. For each eligible destination, upload its required LFS objects.
5. Only after that upload succeeds, push the captured branches and tags.
6. Continue existing settings, releases and packages for successful targets.

A source object discovery/download failure prevents Git pushes for every
participating target. A destination upload failure prevents only that
target's Git push and subsequent artifact/settings steps, using the existing
failed-target behavior. Other destinations continue. Unrelated targets with
no Git/LFS work retain their existing behavior.

Preparation is not transactional: a newly created destination may remain
empty after a transfer failure, and already uploaded content-addressed LFS
objects may remain after a failed Git push. Do not delete repositories or
objects to simulate rollback. Existing published refs are not intentionally
changed on LFS failure; Git pushes retain their current partial-update
semantics if the Git push itself fails.

Use existing git-process acquisition for LFS commands. Bound internal LFS
parallelism independently (initially one transfer per invocation) and avoid
holding an extra slot across nested acquisitions.

## Authentication and endpoint safety

Support the existing HTTP(S) forge Git destinations and their standard LFS
endpoints. Derive and pin the source and destination LFS endpoint from the
validated Git remote, rather than trusting `.lfsconfig` or inherited LFS
configuration. Custom endpoint overrides are out of scope initially.

Preserve isolation between source and destination credentials, and between
repositories on the same host. Verify CLI authentication behavior explicitly:
Git's existing URL-scoped extra-header setup must not simply be assumed to
cover LFS correctly. Tokens must remain out of argv, repository config,
helper files, logs, status and UI.

Allow standard LFS batch responses to direct object transfers to storage
hosts, but never forward forge authorization headers to those hosts. Honor
only transfer-specific credentials returned for that transfer, and verify
redirect behavior does not expose forge credentials. Reject unsupported
custom transfer agents. Sanitize inherited `LFS_*` variables in addition to
existing Git environment controls.

Do not propagate raw CLI diagnostics, signed transfer URLs or LFS diagnostic
log contents into logs, returned errors or persistent status. Return bounded,
actionable operation errors with safe categories (dependency, authorization,
missing object, quota/unsupported endpoint, timeout, transfer failure).
Temporary workspaces can contain repository content and transient CLI
metadata, but configured tokens must never be written there. Test the pinned
CLI's error paths for its own diagnostic-file behavior.

## Repair and unchanged repositories

Keep ordinary plan-only Refresh and scheduled unchanged-ref runs cheap. They
do not clone or audit object availability. Show ref agreement as a Git-ref
comparison, not proof of LFS completeness.

Add a dedicated per-repository POST action, **Repair LFS**, using the same
known-repository admission and cross-origin protection as Sync. It applies
only when explicitly requested, in both auto and review modes. It reconciles
LFS across configured destinations even if refs match, but does not push Git
refs or apply unrelated settings/releases/packages. If a target is missing,
report that it requires normal Sync first rather than creating it here.

A repair is scoped to the current source snapshot, not destination-only
historical refs. Existing target refs may therefore remain unchanged while
missing required source-history objects are uploaded. Report this scope in
the documentation rather than implying a forensic audit of every target ref.

Model repair as explicit dispatcher intent, not an overloaded plan change.
Preserve it when events coalesce: multiple repairs collapse; repair plus a
normal sync executes both intents (reconciling unchanged eligible targets as
well); plan-only events must not discard repair intent; deletion retains
precedence. Repair follows per-repository serialization and shutdown rules.
Sync all retains its existing meaning and does not become an all-repository
LFS audit.

## Status and UI

Reuse target operation results for an `lfs` operation, including repair runs.
Record source download failures against all affected destinations. Record
no-pointer reconciliation as successful with a no-op explanation. Distinguish
LFS upload failure from Git push failure and do not fabricate a successful
Git push when it was skipped. Preserve failure evidence independently of a
subsequent ref-only verification.

Add the repair action to the existing repository action area with concise
help text explaining its bandwidth and scope. No new visual layout or
progress dashboard is needed. A last successful LFS operation is evidence
about that snapshot at that time, not perpetual remote health.

## Acceptance and verification

- A first publication with LFS files, including deleted historical files,
  provides all required objects before its branch/tag refs are pushed.
- Hidden-ref-only objects are not transferred; trigger branch filters do not
  unexpectedly become publication filters.
- No-pointer repositories work with ordinary non-LFS Git servers.
- Missing source objects block affected pushes; one target failure does not
  block a successful target. Upload success followed by Git push failure is
  represented accurately.
- Matching-ref repair uploads missing objects without pushing refs or
  publishing unrelated artifacts; Refresh remains read-only.
- Repair admission, CSRF protection, serialization, coalescing and deletion
  precedence match the behavior above.
- Fakes cover command order, scope, auth/environment isolation, timeouts,
  cleanup, concurrency, diagnostics and failure classification.
- A local HTTP test server exercises the real pinned CLI's pointer discovery,
  batch/upload/download behavior, cross-host storage URLs, redirects and
  missing-object paths. It is not a real external Git remote. Explicit
  opt-in integration tasks may use local disposable services; normal tests
  remain deterministic and do not require internet access.
- Missing runtime tools produce the documented startup error; release
  verification confirms Git LFS is executable in both image architectures.
- Documentation covers automatic enablement, binary prerequisites, work-dir
  storage, timeout tuning, standard endpoint support, repair, and limits of
  ref-only verification. Config examples and deployment docs stay consistent.
- Follow test-driven implementation and run `mise run check` before commits.

## Implementation handoff

After this design is approved, write a separate implementation plan with exact
CLI commands, dependency versions, packaging steps, migration/backward-
compatibility tests and file-level tasks. No product implementation or tool
installation is authorized by this draft.
