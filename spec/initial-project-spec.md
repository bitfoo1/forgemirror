---
title: forgemirror - Project Spec
type: spec
permalink: main/projects/forge-mirror/forge-mirror-project-spec
tags:
- golang
- git
- forgejo
- mirror
- kubernetes
- otel
- prometheus
---

A webhook-driven Go service that listens for Forgejo push events and
immediately mirrors the affected repository to one or more remote forges
(GitHub, Codeberg, GitLab, …).

Each phase ends with a green `mise run check` and one conventional commit.

## Status

- [x] Phase 0: Tooling, git hooks, CI/CD pipeline, release flow
- [x] Phase 1: Project skeleton (CLI, version, entrypoint)
- [x] Phase 2: Configuration
- [x] Phase 2.5: Filtering
- [x] Phase 3: Core mirroring
- [x] Phase 4: HTTP webhook server
- [x] Phase 5: Concurrency & multi-target
- [ ] Phase 6: Observability & graceful shutdown
- [ ] Phase 7: Kubernetes deployment

## Design decisions

- **Target URLs are templates.** One target serves many repositories, so its
  URL is a Go `text/template` (e.g.
  `https://github.com/acme/{{ .Name }}.git`), validated at startup.
- **Credentials are injected per command, never persisted.** Tokens go into
  the remote URL only for the lifetime of a git subprocess and are redacted
  from all output. Every secret can be given inline or as `*_file` (for
  Kubernetes Secret mounts).
- **Source credentials.** Private Forgejo repositories need a token to clone;
  the config has a `source` section for it.
- **Concurrent pushes to one repo coalesce.** Instead of the naive "skip if
  already running" (which loses the latest push), a push that arrives during
  a running mirror schedules exactly one re-run after it finishes.
- **Version comes from Go build info**, not `-ldflags -X` globals.
- **Container images are built by goreleaser's ko integration** (daemonless;
  the CI runner has no Docker daemon). The base image must contain `git`.

## Phase 0: Tooling & CI/CD

- [x] mise pins all tools; tasks are file tasks in `mise-tasks/`
- [x] mise `enter`/`postinstall` hooks set `core.hooksPath=.githooks`
- [x] golangci-lint with the maratori golden config + gofumpt
- [x] `commit-msg` hook validates Conventional Commits
- [x] `pre-push` hook validates all outgoing commits, then runs `mise run check`
- [x] Forgejo Actions pipeline: commit lint, fmt, tidy, vet, lint, test
      (race + ≥80 % coverage), govulncheck, goreleaser snapshot, git-cliff
      changelog as the last job
- [x] Release PR flow: bot keeps `release/next` with the bumped CHANGELOG.md;
      merging it tags `vX.Y.Z` and runs goreleaser in the same run
- [x] goreleaser: multi-arch binaries + ko image to the Forgejo registry,
      SBOMs, cosign signatures
- [x] Renovate config; branch protection documented in `docs/`

## Phase 1: Project skeleton

- [x] `cmd/forgemirror/main.go`: signal-aware context, exit code handling
- [x] `internal/app`: urfave/cli v3 root command with `--version`
- [x] `internal/version`: version/commit from `runtime/debug.ReadBuildInfo`

## Phase 2: Configuration

- [x] `internal/config`: typed `Config` loaded with koanf
      (YAML file → `FORGEMIRROR_*` env vars → CLI flags, later wins)
- [x] Sections: `server` (listen address, shutdown timeout), `log` (level,
      format), `webhook` (secret / secret_file), `source` (username, token /
      token_file), `targets[]` (name, url template, username, token /
      token_file), `mirror` (timeout, work dir)
- [x] Defaults for every optional value; secrets from `*_file` resolved
- [x] `Validate()` returns all problems at once (`errors.Join`)
- [x] Table-driven tests: valid file, env override, flag override, missing
      required fields, empty targets, invalid template, secret files

## Phase 2.5: Filtering

- [x] `filter` config: `include_repos` / `exclude_repos` (regex),
      `include_branches` (regex list), `exclude_private`, `exclude_forks`,
      `require_topic`
- [x] Regexes compiled once at startup; invalid patterns fail validation
- [x] `Filter.Decide(repo, ref)` returns decision + reason, order: branch →
      topic → visibility → fork → include → exclude (exclude wins over
      include); patterns are anchored to the whole value
- [x] Tests for each rule, the empty filter, and include/exclude precedence
- [x] Tags are not filtered: `git push --mirror` mirrors all refs on purpose

## Phase 3: Core mirroring

- [x] `internal/mirror`: `Mirrorer` (clone once) and `Workspace` (push to each
      target, safe for concurrent pushes), git implementation via
      `exec.CommandContext` with per-command timeout and `WaitDelay`
- [x] `git clone --mirror` into a temp dir → push → cleanup, also on failure
- [x] Push uses `+refs/heads/*` and `+refs/tags/*` with `--prune` instead of
      `--mirror`: Forgejo exposes `refs/pull/*`, which GitHub rejects
- [x] Credentials as a URL-scoped `http.<url>.extraHeader` via `GIT_CONFIG_*`
      env vars: never on disk (the clone config would store a URL with
      credentials), never in argv; inherited `GIT_*` vars and host/global git
      config are ignored
- [x] stdout/stderr captured into wrapped errors (`%w`), credentials redacted
- [x] Tests: fake git (args, env, failure, timeout, cancellation, cleanup,
      redaction) and real git (ref semantics, header scoping)
- [ ] Known limitation: Git LFS objects are not mirrored

## Phase 4: HTTP webhook server

- [x] `internal/webhook`: handler for `POST /webhook`
- [x] Body size limit; HMAC-SHA256 over the raw body checked with
      `hmac.Equal` against `X-Forgejo-Signature` (fallback `X-Gitea-*`)
- [x] Only `push` events are processed; others are acknowledged and ignored
- [x] Responses: 202 queued, 200 ignored/filtered, 400 malformed, 401
      bad/missing signature, 413 too large, 415 not JSON
- [x] Accepted events go to a non-blocking `Dispatcher` (Forgejo times out
      deliveries after a few seconds; mirrors take minutes)
- [x] `internal/server`: routing, `GET /healthz` (liveness), `GET /readyz`
      (readiness, 503 once shutdown begins), optional `GET /metrics`,
      read/write/idle timeouts, graceful HTTP shutdown
- [x] Tests with `httptest` and a real listener

## Phase 5: Concurrency & multi-target

- [x] `internal/dispatch`: one clone per event, one goroutine per target
      (`sync.WaitGroup.Go`), results per target; one failing target does not
      stop the others
- [x] Handler returns 202 immediately; work runs in the background with a
      context detached from the request but cancelled by the dispatcher
- [x] Per-repository coalescing: mutex-guarded map of running repositories;
      pushes during a run queue exactly one follow-up run with the newest event
- [x] Global limit on concurrent git processes (channel semaphore)
- [x] `internal/app` wires filter → webhook → dispatcher → git mirrorer;
      HTTP drain and mirror drain share one shutdown deadline
- [x] End-to-end test: signed push → clone + push with a fake git → clean
      shutdown
- [x] Known limitation: in-flight and queued work is lost if the process
      crashes (no persistent queue)

## Phase 6: Observability & graceful shutdown

- [ ] `log/slog`: JSON or text, level from config; every mirror log has
      target, repository, duration, outcome; no secrets
- [ ] OpenTelemetry metrics with the Prometheus exporter on `GET /metrics`:
      `forge_mirror_operations_total` (counter), `forge_mirror_duration_seconds`
      (histogram), `forge_mirror_webhook_requests_total` (counter),
      `forge_mirror_active_operations` (gauge)
- [ ] SIGTERM/SIGINT: stop accepting requests, drain running mirrors, bounded
      by the shutdown timeout

## Phase 7: Kubernetes deployment

- [ ] `deploy/`: Deployment (1 replica, requests/limits, liveness `/healthz`,
      readiness `/readyz`, non-root, read-only root fs + writable tmp),
      Service (ClusterIP), Secret template, ConfigMap
- [ ] `deploy/README.md`: apply, configure the Forgejo system webhook, verify
