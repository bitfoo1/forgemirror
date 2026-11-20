---
title: Development
weight: 7
---

## Setup

[mise](https://mise.jdx.dev) manages every tool (Go, golangci-lint,
goreleaser, helm, hugo, …) at pinned versions, every task and the git hooks:

```sh
git clone {{< param "project.repo" >}}.git && cd forgemirror
mise install        # tools; also points core.hooksPath at .githooks
mise tasks          # list all tasks
```

| Task                         | Purpose                                                     |
|------------------------------|-------------------------------------------------------------|
| `mise run check`             | Full gate: fmt, tidy, vet, lint, tests (race, ≥ 80 % coverage), govulncheck, manifests, chart, docs |
| `mise run fmt`               | Format (gofumpt, goimports, golines)                        |
| `mise run test -- -run Name` | Run selected tests                                          |
| `mise run test:e2e`          | End-to-end test against Forgejo LTS and latest in Docker (`e2e/`; needs a Docker daemon; `E2E_FORGEJO=lts\|latest` runs one; `E2E_HOLD=10m` pauses before the delete step to inspect the web UI, raise the deadline with `-- -timeout 20m`) |
| `mise run run -- <args>`     | Run the service from source                                 |
| `mise run build`             | Build `bin/forgemirror`                                     |
| `mise run snapshot`          | Full goreleaser build without publishing                    |
| `mise run docs:serve`        | Live preview of this documentation                          |

The `pre-push` hook validates commit messages and runs `mise run check`.
CI runs exactly the same tasks, so a green push predicts a green pipeline.

## Layout

| Path                   | Content                                                    |
|------------------------|------------------------------------------------------------|
| `cmd/forgemirror/`     | `main` only: signals and exit code                         |
| `internal/app`         | CLI and wiring                                             |
| `internal/config`      | Layered configuration, secrets, validation                 |
| `internal/webhook`     | Signature check, payload parsing, filter decision          |
| `internal/filter`      | Mirror rules                                               |
| `internal/dispatch`    | Background runs: coalescing, fan-out, concurrency limit    |
| `internal/mirror`      | Git and official Git LFS orchestration with isolated credentials |
| `internal/status`, `internal/state` | Bounded history, durable SQLite records and publication controls |
| `internal/retry`, `internal/notify` | Failure classification and an independent durable notification outbox |
| `internal/publication`, `internal/ui` | Evidence-based publication explanations and operator actions |
| `internal/server`      | HTTP routing, health, graceful shutdown                    |
| `internal/telemetry`   | OpenTelemetry metrics and Prometheus exporter              |
| `deploy/`, `charts/`   | kustomize manifests, Helm chart                            |
| `docs/`                | This site (Hugo + Hextra)                                  |

## Conventions

- **Go:** modern idioms for the pinned Go version. There is no package-level
  mutable state, `context.Context` comes first, and errors are wrapped with
  `%w`.
- **Tests:** table-driven and parallel, without external networks or real
  remotes. Local HTTP fixtures exercise SDKs, the real pinned Git LFS CLI,
  notifications and UI actions. Real Git is used in the integration tests of
  `internal/mirror` and in the end-to-end test (`e2e/`, build tag `e2e`),
  which also runs a real Forgejo and is not part of `mise run check`.
- **Comments:** explain *why*, not *what*.
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org)
  with gitmoji, for example
  `feat(webhook): :sparkles: accept organization hooks`. This is enforced by
  the `commit-msg` hook, the `pre-push` hook and CI. Commit types determine
  the next version; see [CI/CD and releases](ci-cd/).

## Documentation

The site lives in `docs/`:
- **Content:** `docs/content/`.
- **Configuration:** `docs/hugo.yaml`, which also holds the project URLs used
  across the pages.
- **Repository files:** reference pages embed files such as
  `forgemirror.example.yaml` and the chart's `values.yaml` straight from the
  repository with the `repo-file` shortcode, so they cannot drift.

```sh
mise run docs:serve   # http://localhost:1313
mise run docs:check   # strict build, as in CI
```
