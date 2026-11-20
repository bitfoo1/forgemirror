# Compact Sync Notice Implementation Plan

> Superseded after UI review by [Forge Badge UI](2026-11-20-forge-badge-ui.md). The user selected option E instead of the compact notice.

**Goal:** Show `github — Awaiting sync` rather than the lengthy manual-sync notice, and provide a safe local preview before any release.

**Design / approved spec:** Manual-sync hold summaries show only the target and `Awaiting sync`; their tooltip says `Review the diff, then Sync. Sync uses the latest state.` Full publication explanations remain in the expanded diff. Other blockers retain their existing summary and action text. Sync behavior is unchanged.

**Approaches:** Shortening the notice with a tooltip was approved. Smaller type would retain the clutter; removing all hold explanations would hide important approval/pause actions.

**Architecture:** Keep the existing publication reason code. Shorten its title/action, and render the manual-sync summary without inline next-step text. Use the existing isolated browser fixture for preview, not production credentials or real mirrors.

**Tech stack:** Go templates, Go tests, Playwright, mise.

## Global Constraints

- No commit, push, merge, tag, or release before UI approval.
- Preview binds to loopback and uses simulated actions/sample data.
- No new dependencies or change to sync semantics.

## Review Focus

- Manual-sync summaries must omit inline instructions but retain a tooltip.
- Approval/pause notices must keep their actionable text.
- The full explanation must remain available inside the diff.
- Mobile layouts must not overflow.
- Preview must not contact a forge.

## Task 1: Compact notice and reviewable preview

**Files:** `internal/publication/reasons.go`, `internal/ui/templates/ui.html`, `tests/ui/live.spec.cjs`, `mise-tasks/ui/preview`.

**Interfaces:** Existing `publication.View.Code`, `.Headline`, `.Next` feed hold summaries; the browser fixture already runs simulated jobs on loopback.

- [x] Add a browser regression checking compact visible notice, tooltip, expanded explanation, and mobile width; run it and observe failure.
- [x] Shorten manual-sync title/action and render only this summary with tooltip instead of inline instructions.
- [x] Run Go and browser suites; verify formatting/lint.
- [x] Add a mise preview task wrapping the existing browser fixture; start and verify HTTP output.
- [x] Review the diff, then stop for user UI approval without committing or releasing.

## Execution ledger

- Baseline: `mise run test` passed.
- Workspace: local branch `fix/compact-sync-notice`; no extra worktree created.
- RED: browser regression failed with the original long notice as received text.
- GREEN: browser regression passed at 1280px and 390px.
- Verification: `mise run check` passed, including all 16 browser tests and 89.6% internal coverage; `git diff --check` passed.
- Review: inspected the complete diff inline (no reviewer tool available). Non-review blockers retain their existing inline instructions; sync logic is unchanged.
- Preview: `mise run ui:preview`, verified HTTP response at `http://127.0.0.1:9099/` contains the compact summary and tooltip.
- Awaiting user UI approval. No commits, pushes, merges, tags, or releases performed.
