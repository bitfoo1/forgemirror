# Documentation alignment audit — mirroring improvements

Date: 2026-11-20. User requirements: all eight increments must have aligned
operator documentation; provider token permissions belong in Configuration,
not in Security. This audit accompanies the final increment, not a ninth PR.

## Feature coverage

| Increment | Implementation checked | Operator reference and behavior checked |
|-----------|------------------------|-----------------------------------------|
| 1. Git LFS | `internal/mirror/lfs.go`, dispatch LFS operations, `mise-tasks/lfs/stage`, runtime PATH | Operations **Git LFS**, Configuration **Git LFS**, binary/container prerequisites: automatic CLI, history scope, pre-ref upload, isolated failures, explicit object-only repair, scratch space and per-command timeouts |
| 2. Pause/resume | `status/pause.go`, dispatch mutation permits, control/UI actions | Web UI **Pause and resume targets**: whole-target/repository scopes, active-operation barrier, mandatory persistence, expiry on subsequent work, fresh target-scoped resume including review mode |
| 3. Retry/outage | `internal/retry`, dispatch retry loop, durable retry state | Operations **Retries and outages**: five retries after initial failure, bounded jitter/backoff, Retry-After lower bound, current admission, review holds, exhaustion and no worker slot while waiting |
| 4. History | `status/history.go`, versioned attempts/records, history UI | Web UI **Sync history timeline**, state configuration: 50 attempts / 720h defaults, 1–1000 limit, 20 per page, count/age pruning semantics, interruption and per-target evidence |
| 5. Public identity | `forge/marker.go`, `forge/identity.go`, settings layering | Configuration **Mirror marker**: explicit public URL, website capability, per-target/repository overrides, 350 UTF-8 byte default, 1–4096 range, marker preservation and no Git rewriting/private URL inference |
| 6. Notifications | `internal/notify`, application final-outcome callback and worker | Operations **Blocked-sync notifications**, Configuration, example YAML: secret URL/token files, public UI root, transition dedupe, at-least-once IDs, ordered delivery, five total attempts, Retry-After, queue bounds and failure isolation |
| 7. First publication | source/destination identity grants, atomic publication permits, policy overrides, stale-form check | Configuration and Web UI: default off, target/repository overrides, existing-target adoption, ongoing rather than snapshot authorization, UI requirement, no implicit source API requirement, deletion exception and pause precedence |
| 8. Explanations | pure `internal/publication` model, retained success evidence, UI reason attributes | Web UI **Why was this not published?**, Operations: all blockers, next action, admission/stale state, explicit-vs-auto retry intent, historical-vs-current failures, comparison warnings and LFS evidence limits |

## Shared/defaults audit

- SQLite is mandatory and configured by default. Binary path `data/state.db`;
  Helm/Kubernetes path `/var/lib/forgemirror/state.db` with persistent storage
  enabled. No memory-only fallback. Updated example comments, Security,
  deployment guides, chart values/reference and source-recovery caveats.
- Git LFS is bundled in images and required on PATH for binary installations.
  Scratch objects share the clone work directory, not the state PVC.
- Review mode permits explicit Sync, resume, approval-and-sync and LFS repair,
  plus their authorized retry intent; it is not limited to one button name.
- Private-repository opt-in also applies in review mode. Branch filters select
  triggering pushes; they do not restrict published branches/history.
- Deletion is not inferred solely from missing listing entries. Documented
  current event/not-found lookup behavior, push-filter bypass and source
  visibility caveat rather than claiming only one webhook can delete.
- LFS no-op reconciliation can count as an operation; denied applies are not
  reported as successful pushes. Notification attempts and mirror retries
  have different limits (five total delivery attempts vs five mirror retries).
- Homepage/overview, README, chart and kustomize guides link the new controls;
  schema hover descriptions and example YAML agree with runtime semantics.

## Token permission restructuring

Authoritative reference: `docs/content/docs/configuration.md`, **Token
permissions**, split into Forgejo source, GitHub destination, GitLab destination,
Forgejo/Codeberg destination and per-feature control profiles. Security keeps
credential transport/handling and policy concerns, with a forward link only.
Getting Started, Operations, chart and deployment references were updated.

Provider documentation was checked directly while writing the reference:

- GitHub PAT limitations, label API permissions and GHCR authentication.
  Added missing label Issues-write and conditional Workflows-write guidance;
  documented classic-only GHCR and the single target credential model.
- GitLab access-token scope matrix and registry/Git-over-HTTP documentation.
  Distinguished personal-token `api` implications from group/project Git rights,
  registry scope alternatives and fine-grained registry-push limitations.
- Forgejo token scopes/access modes and actual adapter namespace probes.
  Documented `read:organization` for personal namespace creation probes,
  `write:user`/`write:organization`, package scopes and specific-repository
  token restrictions.

## Verification record

- Removed obsolete live-doc references to Security token tables and optional
  SQLite/read-only UI claims. Historical design documents are not rewritten.
- Approval integration was repeated ten times after replacing an apply-phase
  log timing assumption with a wait for the identity-bearing UI form.
- Nested publication reason disclosures required direct-child summary locators
  in the browser regression test; selection/focus/disclosure assertions remain.
- Final strict docs build/link check, browser suite and full quality gate are
  recorded in the publication-explanations execution plan after completion.
- Independent reviewer tooling is unavailable; this is a code/doc self-audit.
