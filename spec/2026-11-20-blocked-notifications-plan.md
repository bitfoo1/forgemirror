# Blocked Sync Notifications — Design and Plan

**Goal:** Deliver useful blocked/exhausted/recovery notifications without exposing source content or creating notification storms.
**Architecture:** Optional generic outbound JSON webhook with its own durable SQLite outbox, transition deduplication, bounded delivery retries and independent worker. Dispatcher final outcomes feed sanitized state/reason codes, never raw errors. The webhook URL and optional bearer token are secrets; only endpoint fingerprints namespace state.

## Decisions
- Notify on paused/blocked/intervention/exhausted transitions and recovery after a notified problem. Temporary retries alone do not page the operator.
- Payload: stable event ID, repository, target, state, reason code, time and optional explicitly configured public UI link. No matching source text, raw error, token or internal URL.
- At-least-once delivery; receivers deduplicate by event ID/Idempotency-Key. Preserve per-repository/target event order. Five delivery attempts, independent bounded backoff.
- Bound the pending outbox and retained exhausted deliveries. Queue/storage/delivery failure is logged generically and never blocks publication policy or rolls back mirroring.
- No redirects for delivery; no forge/source credentials are reused. URL changes use a different persistent namespace rather than sending old events to a new endpoint.
- Secret scanning remains outside these eight increments; future scanners can contribute reason codes.

## Tasks
- [x] RED/GREEN config validation, secret-file loading/redaction and defaults.
- [x] RED/GREEN outbox transition deduplication, restore, delivery/retry/order and credential-safe HTTP.
- [x] Wire final dispatcher outcomes and worker lifecycle; schema/examples/docs and self-review.
- [x] Final full gate after delivery Retry-After fix.
- [x] Commit and PR 6 (#53).

## Ledger
- Full gate passed once with 89.3% aggregate internal coverage and 5 browser
  tests. Real local HTTP integration proves failed mirror outcomes reach the
  configured webhook; package tests cover durable deduplication, recovery,
  event IDs, redirect refusal and per-target transition order.
- A RED regression test caught delivery retries ignoring Retry-After; the
  delivery schedule now honors the response lower bound independently of
  mirror scheduling. Final gate is required after that fix.
- URL/token values use Secret and *_file resolution, are redacted from UI and
  included in persisted-status redaction. No raw error reaches the outbox.
- Self-review only; no independent reviewer tool is installed.
