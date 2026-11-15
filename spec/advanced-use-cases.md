---
title: forge-mirror - Advanced Use Cases
type: spec
tags:
- golang
- forgejo
- mirror
- advanced
---

Future enhancements and advanced features for forge-mirror. These are
**deliberately out of scope** for the initial build (`initial-project-spec.md`).
They are recorded here so ideas are not lost, and to keep the initial spec
focused. Do not start these until the initial phases are complete.

---

## Live Configuration Reload (config watch)

> **Why deferred:** koanf's file providers expose a `Watch` function that
> notifies on file changes so config can be reloaded without restarting. This is
> attractive but introduces concurrency (a background watcher running alongside
> the server) and shared-state questions that depend on Phase 5 knowledge
> (goroutines, mutexes). Tackle it only after the core service is stable.

- [ ] Use a koanf provider that supports `Watch` to detect config file changes at runtime
- [ ] On change: re-load and re-validate the config before applying it
- [ ] Decide what is safe to hot-reload vs. what requires a restart
      (e.g. log level is easy; the server listen address is not)
- [ ] Handle the concurrency: the watcher runs in a background goroutine while
      request handlers read the current config — research how to share the
      config safely (mutex, atomic pointer swap, etc.)
- [ ] If a reloaded config is invalid, keep running on the last-known-good config
      and log the error — never crash on a bad reload
- [ ] Write tests that simulate a config change and assert the new values take effect

---

## Scheduled deletion detection

> **Why deferred:** deletion only happens on Forgejo's `repository` deleted
> webhook event. A schedule-only deployment (or a lost delivery) never
> deletes, because a repository missing from the source list is
> indistinguishable from one that was never mirrored, excluded by the filter,
> or hidden by a token with reduced access. Deleting on that evidence could
> destroy a target repository because of a filter or token mistake. Safe
> detection needs a marker (e.g. a topic or description tag forgemirror sets
> on repositories it created) or persistent state.

- [ ] Mark target repositories created by forgemirror (topic or state file)
- [ ] Delete only marked repositories that are absent from the source list
- [ ] Refuse to delete when the source listing is empty, truncated or failed
- [ ] Keep it opt-in per target, on top of `delete: true`
