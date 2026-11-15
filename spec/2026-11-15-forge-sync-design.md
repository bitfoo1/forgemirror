---
title: forgemirror - Forge sync (create repositories, sync settings, schedule, delete)
type: spec
tags:
- golang
- forgejo
- github
- gitlab
---

## Intent

The owner keeps every repository on a private Forgejo and wants all of them
visible on GitHub (main public home) and GitLab, so others can find them,
contribute, and so a compatible code backup exists outside the homelab.
Setting up and maintaining copies by hand per repository and per forge does
not scale, so forgemirror itself must:

1. create a missing target repository,
2. keep its settings in line with the Forgejo source, with per-target
   defaults and per-repository overrides,
3. optionally run on a schedule instead of (or in addition to) push webhooks,
4. optionally delete a target repository when its source repository was
   deleted on Forgejo.

"Backup" means **code backup**: branches, tags, the repository settings
listed below and labels. Issues, PR discussions, releases and wikis are not
copied.

## Goals

- Targets gain an optional `type` (`github`, `gitlab`, `forgejo`). With a
  type, forgemirror talks to that forge's REST API; without one, the target
  behaves exactly as today (git push only).
- Missing repositories are created when the target sets `create: true`.
- Synced settings: description, website, topics, default branch,
  visibility, issues on/off, wiki on/off, pull/merge requests on/off,
  labels (name, color, description).
- Settings precedence, last wins: Forgejo source value → target `settings`
  (defaults for that target) → every matching entry of target `overrides`,
  in order.
- Visibility can only get more private: a private source is always private
  on every target; config may force a public source to private on a target,
  never the reverse.
- Private repositories are synced only when explicitly allowed
  (`filter.allow_private`), and stay private.
- `sync.on_push` (default `true`) and `sync.schedule` (interval, default
  off) select push-driven, scheduled, or both. Schedule-only needs no
  inbound webhook for mirroring.
- Scheduled runs skip the clone/push when every target already has the same
  branches and tags as the source.
- `delete: true` on a target (default `false`) deletes the target repository
  when Forgejo reports the source repository as deleted.
- Labels are created and updated; with `prune_labels: true` on a target
  (default `false`) labels missing on the source are deleted there too.
  Off by default because deleting a label strips it from every issue and PR
  on that forge, and new GitHub repositories start with default labels.
- An end-to-end test runs forgemirror against a real Forgejo container that
  acts as source and as a `forgejo`-type target.
- The JSON schema (`docs/static/schema/v1/forgemirror.schema.json`) covers
  every new key and stays `v1` (all changes are additive); the docs'
  editor-support section lists the new keys.
- All existing guarantees stay: secrets never in logs/errors/argv/disk,
  ≥ 80 % coverage, table-driven tests with fakes (httptest servers for the
  APIs), strict config validation, JSON schema in sync.

## Non-goals

- Copying issues, PRs, comments, releases, release assets, wikis.
- Branch protection sync. Merges happen on Forgejo only; protection on a
  mirror would block forgemirror's own force pushes.
- Deletion triggered by a repository becoming excluded, private, or by a
  config change. Only a source deletion counts.
- Scheduled deletion detection (needs a marker or state; see "Left open").
- A Forgejo API for live settings-change notifications (none exists).

## Anti-goals (would make it a failure even if it works)

- A private Forgejo repository ever becoming public on a target.
- forgemirror deleting a target repository because of a filter or config
  mistake.
- Any token reaching logs, error messages, process arguments or disk.
- Silent partial behaviour: a failed create/settings/label step must be
  logged with the target and counted in metrics.

## Constraints

- Forgejo 15 (gitea 1.22 API). Push payloads carry visibility, topics, fork
  flag, clone URL; labels and full settings need `GET /api/v1/repos/{o}/{r}`
  and `/labels`. Repository events use header `X-Forgejo-Event: repository`
  with payload `{"action": "created"|"deleted", "repository": {...}}`.
- The source API base is `source.base_url` + `api/v1`. `source.base_url` and
  `source.token` therefore become required as soon as any target has a
  `type` or `sync.schedule` is set.
- Forge differences the clients must absorb:
  - GitHub cannot disable pull requests → `has_pull_requests` is ignored for
    GitHub (logged once at startup at info level).
  - GitLab has no website field → `website` is ignored for GitLab.
  - Label colors: compare case-insensitively without a leading `#`; send
    each forge its own format.
  - GitHub topics must be lowercase; topics are normalised to lowercase for
    every target.
  - Default branch can only be set once the branch exists: order is
    create → push → settings → labels.
- HTTP: one shared `http.Client` with a timeout; redirects to another host
  are refused (custom auth headers would otherwise follow them); auth via
  `Authorization` header only; error messages carry status and the API's
  message, never request headers.
- API clients use the forges' Go libraries, pinned to releases older than
  two weeks at planning time: `github.com/google/go-github/v93` v93.0.0,
  `gitlab.com/gitlab-org/api/client-go/v3` v3.40.0,
  `codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v4` v4.0.0 (community SDK; no
  official Forgejo SDK exists). Each library gets forgemirror's own
  `http.Client`, so the redirect policy applies; tests point the libraries at
  `httptest` fakes. (Owner's decision: typed models and maintained field
  mappings over a smaller dependency footprint.)

## Approaches considered

1. **Extend forgemirror (chosen).** Per-forge API clients plus a sync step
   in the existing dispatcher; push and schedule trigger the same run. One
   binary, one config, existing filters/metrics/chart reused. Costs:
   forgemirror grows from a git mirror into a sync service.
2. **Separate tool next to forgemirror.** Two programs and configs; a push
   for a new repository fails until the other tool created it; filters and
   overrides duplicated. Rejected.
3. **Terraform/OpenTofu generated from Forgejo.** State files, no reaction to
   pushes, works against the push-or-schedule model. Rejected.

## Design

### Configuration

```yaml
source:
  base_url: https://git.example.org/        # required with type/schedule
  token_file: /run/secrets/forgemirror/source-token

sync:
  on_push: true          # push webhooks start runs (default true)
  schedule: 1h           # Go duration; empty/0 = no scheduled runs

filter:
  exclude_private: true
  allow_private: "dvonessen/notes"   # regex; private repos synced anyway, kept private
  exclude_repos: "dvonessen/(secrets|infra-.*)"

targets:
  - name: github
    type: github                     # github | gitlab | forgejo | "" (git only)
    url: "https://github.com/dvonessen/{{ .Name }}.git"
    api_url: ""                      # optional; derived from type and host
    token_file: /run/secrets/forgemirror/github-token
    create: true                     # default false
    delete: false                    # default false
    prune_labels: false              # default false
    settings:                        # target defaults, override source values
      has_issues: false
      has_wiki: false
    overrides:                       # matched against source owner/name, in order
      - repos: "dvonessen/forgemirror"
        settings:
          has_issues: true
      - repos: "dvonessen/homelab"
        settings:
          private: true
```

Settings keys (all optional, pointer semantics = "unset keeps the value from
the previous layer"): `description`, `website`, `topics` (list),
`default_branch`, `private` (only `true` is accepted), `has_issues`,
`has_wiki`, `has_pull_requests`, `labels` (bool, default `true`; `false`
disables label sync for that scope).

Validation rules (all reported together, as today):
- `type` ∈ {"", github, gitlab, forgejo}.
- `create`, `delete`, `prune_labels`, `settings`, `overrides`, `api_url`
  require a `type`.
- `settings.private: false` is an error ("can only force private").
- `overrides[].repos` must compile (same anchored regex semantics as
  filters).
- `source.base_url` and a source token are required when any target has a
  `type` or `sync.schedule` > 0.
- `sync.on_push: false` with no schedule is an error.
- `webhook.secret` is required when `sync.on_push` is true or any target has
  `delete: true` (deletion arrives as a webhook event). Without a secret the
  `/webhook` endpoint answers 404.
- `filter.allow_private` must compile.

API base URL defaults: github.com → `https://api.github.com`, other GitHub
hosts → `https://<host>/api/v3`; gitlab → `https://<host>/api/v4`; forgejo →
`https://<host>/api/v1`. Owner/namespace and name come from the rendered
target URL path (`/group/sub/name.git` → namespace `group/sub`, name `name`).

### Filter change

`filter.allow_private` (regex on `owner/name`): with any typed target or a
`sync.schedule`, a private repository is synced only if it matches, and
`exclude_private` has no effect. Push-only configurations with only untyped
targets keep the previous behaviour: `exclude_private` decides and a match
passes it. Private repositories are always created and kept private on every
target regardless of overrides.

### Components

- `internal/forge` — forge-neutral model and interfaces:
  - `Settings` (pointer fields as above, plus `Labels []Label` for source
    state), `Label{Name, Color, Description}`.
  - `Source` interface: `Repo(ctx, owner, name) (State, error)`,
    `Labels(ctx, owner, name)`, `Repos(ctx) iter/[]` (all repos visible to
    the token, paged).
  - `Target` interface: `Get(ctx, Address) (State, error)` returning
    `ErrNotFound`; `Create(ctx, Address, Settings)`; `Update(ctx, Address,
    Settings)` (only changed fields); `Labels`/`CreateLabel`/`UpdateLabel`;
    `Delete(ctx, Address)`.
  - `Address{APIURL, Namespace, Name}` derived from the rendered URL.
  - `Resolve(source State, target defaults, overrides, fullName) Settings`
    — the precedence merge including the visibility rule, pure and fully
    unit-tested.
  - `Diff(current, desired) Settings` — only fields that differ.
- `internal/forge/httpclient` — the shared `http.Client` (timeout,
  cross-host redirect refusal) handed to every library.
- `internal/forge/forgejo`, `internal/forge/github`, `internal/forge/gitlab`
  — thin adapters over the libraries, mapping to the forge-neutral model and
  `ErrNotFound`. Forgejo implements both `Source` and `Target`.
- `internal/dispatch` — a run becomes: (1) if any typed target: load source
  state + labels; (2) per typed target: get; if missing and `create` →
  create (without default branch); if missing and not `create` → target
  fails; (3) clone + push as today (scheduled runs: skip when refs equal on
  all targets); (4) per typed target: update settings diff, then create /
  update labels. Steps per target run in parallel as pushes do today.
  Deletion runs (`delete` events) call `Delete` on every target with
  `delete: true` and nothing else.
- `internal/mirror` — `Mirrorer.Refs(ctx, Remote) (map[string]string, error)`
  via `git ls-remote --heads --tags` with the same credential handling as
  clone/push; used for the scheduled skip check.
- `internal/schedule` (or inside `app`) — a ticker started by `app` when
  `sync.schedule` > 0: lists source repos, applies filter, dispatches one
  synthetic event per repository (ref empty, `Scheduled: true`). The
  dispatcher's per-repo coalescing already prevents overlap. The first run
  starts right after startup.
- `internal/webhook` — push events are ignored with `200 ignored` when
  `sync.on_push` is false; `repository` events with `action: deleted` are
  dispatched as delete events (after the same signature check, clone-URL
  check does not apply); `created` is ignored.
- `internal/telemetry` — new counter
  `forgemirror_sync_operations_total{target, operation, status}` with
  operation ∈ create, settings, labels, delete, and
  `forgemirror_scheduled_runs_total{status}`. Existing metrics unchanged.

### Docs to update

Configuration (sync, target type/create/delete/settings/overrides,
allow_private), Security (token scopes per forge; private-stays-private;
delete is opt-in and source-deletion-only), Forgejo webhook (repository
events for deletion; schedule-only needs no webhook), Operations (new
metrics), example config, kustomize ConfigMap, chart README/values comments,
JSON schema, README feature list. Record "scheduled deletion detection" in
`spec/advanced-use-cases.md`.

### Token scopes (to document)

- Forgejo source (verified in the Forgejo v15 API routes):
  - `read:repository`: repository details, topics, `GET /repos/search` (the
    schedule's repository list), cloning.
  - `read:issue`: labels (Forgejo files label routes under the issue scope).
  - Repository access "all (public, private and limited)"; a public-only
    token hides private repositories, so `allow_private` cannot work.
  - The repository list contains what the token's user can read; an admin
    user sees every repository. Recommended: a dedicated bot user with read
    access to the mirrored organisations. No write scope on the source.
- GitHub: fine-grained token on the owner with Administration (create,
  delete, settings) and Contents read/write; classic: `repo` (+
  `delete_repo` for delete).
- GitLab: `api` scope.
- Forgejo target: `write:repository`, `write:issue` (labels),
  `write:organization` to create in an organisation or `write:user` to create
  in a user account (Forgejo checks these at creation).

## Decided by the owner

- Approach 1; three releasable steps: (1) create + settings + labels on
  push, (2) schedule, (3) delete.
- Deletion off by default; only when the source repository was deleted
  (option A).
- Private repositories only when explicitly allowed; always private on
  targets. Public repositories may be forced private per target/repo.
- Periodic sync with configurable interval; push and/or schedule selectable;
  webhook not needed in schedule-only mode.
- Code backup only.
- Issues off on GitHub/GitLab but on in Forgejo must be expressible
  (target `settings`).

## Calls made by the builder-side (owner may revise)

- `prune_labels` opt-in per target (owner agreed).
- Branch protection out of scope.
- Deletion only via the Forgejo repository-deleted webhook event in this
  iteration; schedule-only deployments do not delete.
- `has_pull_requests` ignored on GitHub, `website` ignored on GitLab.
- Topics lower-cased everywhere.

## Left open

- Scheduled deletion detection (would need a marker topic or persistent
  state) → `spec/advanced-use-cases.md`.
- Rate-limit back-off beyond failing the run and retrying on the next
  trigger.
