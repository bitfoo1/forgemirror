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
- [ ] Phase 3: Core mirroring
- [ ] Phase 4: HTTP webhook server
- [ ] Phase 5: Concurrency & multi-target
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

- [ ] `internal/mirror`: `Mirrorer` interface, git implementation via
      `exec.CommandContext`
- [ ] `git clone --mirror` into a temp dir → `git push --mirror` → cleanup
      with `defer`, also on failure
- [ ] Credentials injected via URL userinfo at runtime; redacted from errors
- [ ] stdout/stderr captured into wrapped errors (`%w`)
- [ ] Tests with a fake git binary: success, timeout, cleanup on failure,
      redaction

## Phase 4: HTTP webhook server

- [ ] `internal/webhook`: `POST /webhook`
- [ ] Body size limit; HMAC-SHA256 over the raw body checked with
      `hmac.Equal` against `X-Forgejo-Signature` / `X-Gitea-Signature`
- [ ] Only `push` events are processed; others are acknowledged and ignored
- [ ] Responses: 401 bad/missing signature, 400 malformed JSON, 202 accepted
- [ ] `GET /healthz` (liveness) and `GET /readyz` (readiness)
- [ ] `http.Server` with read/write/idle timeouts
- [ ] Tests with `httptest`

## Phase 5: Concurrency & multi-target

- [ ] One goroutine per target, `sync.WaitGroup`, errors collected and
      joined; one failing target does not stop the others
- [ ] Handler returns 202 immediately; work runs in the background
- [ ] Per-repository coalescing (see design decisions), mutex-guarded
- [ ] Global concurrency limit
- [ ] Known limitation: in-flight work is lost if the process crashes

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
