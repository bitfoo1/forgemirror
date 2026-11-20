# Git LFS Mirroring Implementation Plan

> Execute inline with test-driven-development; the user authorized implementation without further approval questions and requests eight separate PRs.

**Goal:** Automatically publish complete LFS history before Git refs, with an explicit repair action.
**Architecture:** Extend mirror workspaces with LFS operations and use explicit dispatcher repair intent. Keep existing observer/status infrastructure and per-repository serialization.
**Spec:** `spec/2026-11-20-git-lfs-mirroring-design.md`

## Global constraints

Use the official pinned CLI, no source changes, no credentials on disk/argv, no hidden-ref-only objects, no external remotes in tests. Preserve cheap read-only plans. Every commit requires `mise run check`.

## Tasks

- [x] 1. Pin git-lfs and verify its documented commands/auth/diagnostics using local CLI fixtures. Write mirror tests for ref-scoped transfers, empty histories, auth isolation and failures; observe RED; implement minimal workspace LFS operations; verify GREEN.
- [x] 2. Add dispatch tests for shared source fetch, upload-before-push, destination failure isolation, repair without unrelated mutations, and intent coalescing; observe RED; implement LFS orchestration and repair; verify GREEN.
- [x] 3. Add control/UI tests for admitted POST-only repair, review mode, CSRF and persisted LFS outcomes; observe RED; wire action/status; verify GREEN.
- [x] 4. Integrate runtime images, binary dependency checks and operator documentation. Verify packaging with pinned tools; run the full gate; review changes; commit and open PR 1.

## Review focus

- LFS CLI may write diagnostic logs containing credentials: suppress raw output and verify the actual pinned CLI behavior.
- Batch transfer storage hosts must never receive forge credentials.
- `--all` must be scoped to published heads/tags, not hidden mirror refs.
- Equal Git refs do not prove object availability; repair must execute without a ref diff.
- Repair intent must survive event coalescing and never recreate a deleted source.

## Execution ledger

- Baseline: `mise run test` passed on origin/main (92f223a).
- Ruling: use worktrees under ignored `.superpowers/worktrees/`; preserve the user's current checkout and the existing branding worktree.
- Ruling: later PRs may be stacked, with explicit dependency bases. Do not merge any PR automatically.
- Ruling: stage static, pinned CLI builds in ko's architecture-specific kodata; no runtime base image replacement or Docker daemon is needed. Both Linux architectures cross-built successfully. Full image execution is not exercised on this host.
- Verification: full `mise run check` passed with 91.4% internal coverage; real CLI local HTTP tests cover historical pointers, hidden refs, custom endpoint rejection, separate storage auth, missing objects and credentials not reaching disk. GoReleaser config validation passed.
- Review: self-review only; no subagent tool is installed. Raw CLI failure classification is intentionally generic until structured retry classification lands; timeout remains inspectable.
