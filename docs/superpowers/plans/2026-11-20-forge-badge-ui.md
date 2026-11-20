# Forge Badge UI Implementation Plan

> Execute inline with test-driven development. This supersedes the compact-notice design. Leave all work uncommitted for UI review.

**Goal / approved design:** Implement option E: one repository entry and Sync action, forge-name-only status badges immediately after its name, and a single diff with identical changes grouped and forge badges immediately after each change. Green means a fresh Git/metadata comparison matches, yellow means pending changes, red means a target error, and gray means unknown/stale. Hover and accessible labels explain status. Errors take precedence. Full policy explanations, approval controls, pauses, and historical outcomes remain accessible in expandable target details; never merge different old/new values merely because display-shortened values match.

**Approaches:** User selected E over banners, quiet status rows, per-destination blocks, and summary-only status. Group by complete change payload, not display text. Sync semantics and release state are unchanged.

**Architecture:** Derive badges from comparison/result evidence; split plans into atomic display changes and group equal payloads with target labels. Share existing change rendering between grouped current diffs and per-target historical plans.

**Tech stack:** Go, HTML templates, CSS, Playwright, mise. No new dependencies.

## Review focus

- Freshness, running jobs, and missing comparisons never imply green.
- Source, target, push, and feature-operation errors remain visible.
- Different full ref IDs, settings, labels, releases, package destinations, and omitted details never merge incorrectly.
- Approval/pause controls and historical plans remain available.
- Mobile layout, live selection preservation, and action feedback continue working.

## Tasks

- [x] Add failing renderer tests and browser coverage for statuses, grouping, layout, and tooltips.
- [x] Derive target badges and group atomic changes using full payload equality.
- [x] Integrate grouped current diff, header badges, and collapsed target explanations without altering historical rendering.
- [x] Extend the safe fixture/preview with multiple-target scenarios.
- [x] Run the complete check gate and inspect the diff; restart preview and wait for UI approval. No commit/push/release.

## Ledger

- Approved by user: E, forge-only badges with hover status, header placement beside repository name, and inline per-change forge attribution.
- Baseline: prior `mise run check` passed (16 browser tests, 89.6% coverage).
- Workspace: existing branch `fix/compact-sync-notice`, uncommitted. No subagent tools available; execute/review inline.
- RED: renderer tests reproduced missing badges and duplicate shared ref/setting rows. Edge tests reproduced an incorrect green badge during refresh and missed source errors during sync.
- GREEN: all Go tests and all 16 browser tests pass. `mise run check` passed with 89.7% internal coverage; `git diff --check` passed.
- Verification adjustment: full-suite browser layout test needed to open the outer current diff before the nested target details after an earlier test synced the fixture. The focused run began pending, while the full run began synced.
- Review: inspected badge derivation, full-payload grouping, historical rendering, controls, live reconciliation, and updated documentation inline.
- Actual preview verified at `http://127.0.0.1:9099/` at 1440px and 390px: all three badges share the repository-name line; two shared-target ref changes appear as one row and a third target's different ref as another.
- Waiting for integrated-UI approval. No commit, push, merge, version change, tag, or release performed.
