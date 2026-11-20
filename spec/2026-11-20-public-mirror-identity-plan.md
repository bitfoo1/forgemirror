# Public Mirror Identity — Design and Plan

**Goal:** Public visitors can identify a read-only mirror and find an explicitly configured public contribution location, without exposing a private source URL or rewriting Git history.
**Existing foundation:** `settings.mirror_note` and `settings.mirror_topic` already implement templated identity and per-repository override layers. Extend that mechanism instead of adding another identity subsystem.

## Decisions
- Add `settings.mirror_url`: explicit public HTTP(S) URL template (same repository fields as mirror_note). Empty disables it. Never infer it from the private source.
- Append a contribution link to the mirror note and set the target website where supported; GitLab still receives the link in its description.
- Add `settings.mirror_description_limit`, default 350 UTF-8 bytes, configurable 1–4096. Preserve the marker/link and truncate only the source description at a UTF-8 boundary. If the marker itself cannot fit, report a plan error rather than repeatedly submitting invalid settings.
- Existing description overrides still replace the source description before the marker is appended; an empty mirror note disables that note independently of the contribution link/topic.
- Identity remains part of normal settings planning/review and target creation, never edits commits or READMEs.

## Tasks
- [x] RED/GREEN explicit URL rendering/validation, override precedence, description bounds, repeat stability and unsupported website capability.
- [x] Schema/examples/operator documentation; full gate and self-review.
- [x] Commit and PR 5 (#52).

## Ledger
- Full `mise run check` passed, including all 5 browser tests and the coverage
  gate. Tests cover URL validation/rendering, override disablement, UTF-8 byte
  bounds, marker-too-long errors, repeat stability and unsupported website
  capability.
- An existing regression test caught whitespace normalization when all text
  markers were disabled; preserved the original description in that case.
- No separate identity config was added because existing marker settings
  already provide scope/layering. Self-review only (no reviewer tool).
