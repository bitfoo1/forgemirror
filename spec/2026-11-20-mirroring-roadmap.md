# Mirroring improvements roadmap

Status: delivery order and incremental approach approved in conversation;
individual feature designs still require approval.

## Delivery approach

Ship independent, usable increments in the order below. Each feature gets
its own design and implementation plan. Reuse the dispatcher, plans, status
store and SQLite persistence rather than building another job system.

Each increment includes tests, configuration/schema changes where needed,
operator documentation, deployment/release integration and the full
`mise run check` gate before a commit. No product implementation is approved
merely by this roadmap.

## Ordered increments

1. **Git LFS mirroring.** Official `git-lfs` CLI; automatically enabled.
   Fetch source objects once, upload before destination Git refs, isolate
   destination failures, provide reconciliation for matching Git refs.
   Design: `2026-11-20-git-lfs-mirroring-design.md`.
2. **Per-target pause/resume.** Persistent global-target and repository-target
   pauses; continue planning but prevent new mutations. Optional reason and
   expiry; resume computes fresh state. Proposed active-operation semantics:
   finish the operation already started, then stop further mutations.
3. **Retry and outage handling.** Typed retry decisions, bounded backoff with
   jitter, rate-limit awareness, persistent timing, fresh planning and event
   coalescing. Resolve ambiguous mutations by reading remote state first.
4. **Sync history timeline.** Extend latest-attempt persistence into bounded,
   paginated history with trigger, changes, per-operation results, partial
   outcomes, interruption and retry relationships.
5. **Public-mirror identity.** Configurable target descriptions and public
   contribution/contact links integrated with settings planning. Do not edit
   Git history or automatically link to a private source instance.
6. **Blocked-sync notifications.** Generic outbound webhook, transition-based
   deduplication, bounded independent delivery retries and recovery events.
   Secret scanning is a separate feature, not implicitly included here.
7. **Configurable first-publication protection.** Optional, default off;
   persisted approval per source/destination identity; cover Git, releases
   and packages. Explicit adoption behavior for existing destinations.
   Proposed approval scope: destination authorization for ongoing mirroring,
   not approval of one exact source snapshot.
8. **Explainable publication status.** Consolidated reasons, last publication,
   pending work, next automatic action and operator remedies. Introduce
   structured reason codes as earlier features need them; assemble the full
   explanation view here.

## Shared constraints

- User correction: SQLite is mandatory and configured by default, not opt-in.
  Binary default: `data/state.db`; deployments configure a persistent volume.
  Empty paths and database startup failures are errors; no memory-only fallback.

- Successful destinations must not be reported as failed because another
  destination failed; partial outcomes remain explicit.
- Planning and publication evidence are distinct. Ref equality alone does
  not prove LFS availability or that all artifact steps succeeded.
- Durable history is not an implicit executable queue.
- Policy and operator controls apply to all entry points, not only UI buttons.
- Secrets and raw external-tool diagnostics must not leak into stored status,
  notifications, logs or UI responses.
- Define later features in detail when their turn arrives rather than
  prebuilding speculative abstractions.
