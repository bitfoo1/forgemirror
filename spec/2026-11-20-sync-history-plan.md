# Sync History Timeline — Design and Plan

**Goal:** A durable, bounded and paginated per-repository record of applied sync/repair attempts, distinct from current read-only comparisons.
**Architecture:** Extend existing attempt records with stable per-repository sequence IDs, trigger/retry linkage and immutable completed history. Store it in versioned repository records in the mandatory SQLite database. Add a read-only history page using existing UI rendering.

## Decisions
- Default retention: latest 50 attempts per repository, at most 30 days; configurable positive bounds (maximum 1000 attempts).
- Record successes, partial failures, failures and interruptions, including per-operation LFS/artifact results. Planning alone does not flood history.
- Show target repository links and intended change details; observed outcomes remain distinct from the plan. Do not invent a commit link format for untyped/custom forges.
- Paginate newest-first, 20 attempts per page. History never authorizes or schedules work.
- Read older status version 1 records; migrate latest attempts, preserve interrupted work as interrupted. New status version 2 is rejected by older binaries.

## Tasks
- [x] RED/GREEN bounded retention, pagination, immutable outcomes and restore/interruption handling.
- [x] RED/GREEN configuration defaults/validation and trigger/retry metadata; add safe target links.
- [x] RED/GREEN history page, pagination and unknown repository behavior; update docs/schema/examples.
- [x] Full gate and self-review (all checks passed, 5 browser tests).
- [ ] Final gate after startup-pruning review fix, commit and PR 4.

## Ledger
- RED tests covered missing history API/configuration and unregistered history
  route. GREEN suite includes SQLite restore of an interrupted attempt,
  pagination and count/age retention.
- Ruling: age pruning happens on state writes and startup, with expired rows
  also hidden on reads. There is no continuous idle-record erasure service;
  documentation states that limit rather than implying strict TTL deletion.
- Retry parent IDs are carried in durable scheduling metadata instead of
  guessing from whichever unrelated target attempt happened most recently.
- Review: self-review only; no independent reviewer tool is available.
