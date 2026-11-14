# forgemirror — Agent Instructions

## Project

forgemirror is a webhook-driven Go service. It receives push events from a
private Forgejo instance (`git.bitfoo.de`, not internet-exposed) and mirrors
the affected repository to one or more public forges (GitHub, Codeberg,
GitLab, …) with `git clone --mirror` + `git push --mirror`.

- Roadmap and acceptance criteria: `spec/initial-project-spec.md`
- Deferred ideas: `spec/advanced-use-cases.md`
- Module path: `go.bitfoo.de/forgemirror` (vanity import)

## Layout

- `cmd/forgemirror/` — `main` only: signals, exit code. No logic.
- `internal/<pkg>/` — all code; one package per concern (app, config, filter,
  mirror, webhook, …). Tests live next to the code in `<pkg>_test` packages.
- `mise-tasks/` — every task as a file task (`mise tasks` lists them).
- `.githooks/` — thin shims that call `mise run hook:*`.
- `.forgejo/workflows/` — CI; it calls the same mise tasks.
- `deploy/` — Kubernetes manifests (kustomize); see `deploy/README.md`.

## Tooling — mise owns everything

- Tools are pinned in `mise.toml` and installed with `mise install`. Never
  install or call a tool outside mise; add it to `[tools]` instead.
- Tasks are file tasks in `mise-tasks/`. Add new tasks there, not in
  `mise.toml`. CI and git hooks must call tasks, never ad-hoc commands, so the
  three cannot drift apart.
- The mise `enter`/`postinstall` hooks set `core.hooksPath=.githooks`.

Common tasks:

- `mise run check` — the full gate (fmt, tidy, vet, lint, tests with race
  detector and ≥80 % coverage of `internal/`, govulncheck, Kubernetes manifest
  validation). Pre-push and CI
  run exactly this. It must be green before every commit.
- `mise run fmt` — format (gofumpt + goimports + golines via golangci-lint).
- `mise run test -- -run TestName` — run selected tests.
- `mise run build` / `mise run run -- <args>`

## Go conventions

- Target the Go version pinned in `mise.toml`; use modern idioms (`for range
  n`, `t.Context()`, `slices`/`maps`, `log/slog`, `errors.Is/As`, `%w`).
  When unsure about an idiom, check go.dev documentation instead of guessing.
- `.golangci.yml` is the maratori golden config (plus gofumpt). Fix findings;
  `//nolint` only with a specific linter and a reason.
- No package-level mutable state. Dependencies (logger, clock, runners) are
  passed explicitly; `context.Context` is the first parameter and is never
  stored in structs.
- Secrets (HMAC secret, tokens) never reach logs, error messages or disk.
- Comments explain *why* (constraints, rejected alternatives), not *what*.
- Tests are table-driven, call `t.Parallel()`, and use fakes instead of
  network or real git remotes.

## Commits and releases

- Conventional Commits with gitmoji: `type(scope): :gitmoji: description`.
  Enforced by the commit-msg hook, pre-push and CI (`mise run commits:check`).
- Allowed types: feat, fix, perf, refactor, docs, test, build, ci, chore,
  style, revert. Keep this list in sync with `cliff.toml`.
- Versions are plain semver tags `vX.Y.Z`, derived by git-cliff from the
  commit types. Releases happen by merging the bot's release PR; never tag by
  hand.
