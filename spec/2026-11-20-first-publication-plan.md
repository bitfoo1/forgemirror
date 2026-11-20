# Configurable First-publication Protection — Design and Plan

**Goal:** Optionally require explicit operator authorization before publishing a source repository to a destination, including LFS, releases, images, settings and creation.
**Architecture:** Global default-off policy with target/repository overrides; durable source+destination identity-bound approval records; an atomic per-operation pause/approval permit; read-only plan flags and admitted POST approval action.

## Decisions
- `sync.first_publication_approval: false` preserves existing behavior. Target and repository override booleans take precedence. Enabling the policy requires the UI, not additional source API access.
- Approval authorizes ongoing publication to that exact source/destination identity, not a reviewed snapshot. Source or destination remapping does not match the grant. Credential rotation alone does not invalidate it.
- Enabling protection treats existing unapproved destinations as pending too; no implicit adoption based on matching refs. No bulk approval in this increment.
- Approve persists the grant then requests a fresh target-scoped sync, with confirmation explaining ongoing authorization.
- Deletion removes exposure and does not require first-publication authorization; pauses still apply. Read-only planning remains available.
- Controls schema version 3 reads prior versions. Notifications use the `approval_required` code. History must not report a policy-blocked operation as successful.

## Tasks
- [x] RED/GREEN identity binding, pause/approval permits and durable restore.
- [x] RED/GREEN policy defaults/overrides/validation; wire every publication mutation and read-only plan flags.
- [x] RED/GREEN admitted approval action, CSRF, explicit confirmation and status; schema/docs/examples.
- [x] Full gate and self-review.
- [x] Commit and PR 7 (#54).

## Ledger
- Full `mise run check` passed, including all 5 browser tests and the coverage
  gate. Real local forge/UI integration proves creation is blocked until a
  current identity-bearing form is approved, then requests fresh publication.
- Grants bind both source and destination; stale forms return conflict.
  Deletion bypasses approval only, never pause. No bulk approval or implicit
  existing-mirror adoption was added.
- A RED regression test found blanket control-record redaction corrupting
  static retry codes and hex identity hashes when a short configured token
  matched a substring. Controls now redact operator-entered pause reasons at
  admission; functional identifiers/codes never contain copied credentials.
- Gated API steps now report skipped-operation failures to observers, avoiding
  a successful history result when a pause/approval prevented a later step.
- Self-review only; no independent reviewer tool is installed.
