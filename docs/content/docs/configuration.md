---
title: Configuration
weight: 2
---

ForgeMirror reads one YAML file. Any key can be overridden with an
environment variable or a CLI flag, and every secret can be read from a file.

## Sources and precedence

Later sources win:

1. Built-in defaults.
2. The configuration file.
3. Environment variables: prefix `FORGEMIRROR_`, upper case, with a double
   underscore for nesting.
4. CLI flags.

```sh
FORGEMIRROR_SERVER__LISTEN_ADDRESS=:9000
FORGEMIRROR_LOG__LEVEL=debug
FORGEMIRROR_WEBHOOK__SECRET_FILE=/run/secrets/forgemirror/webhook-secret
```

**Config file location:** the file comes from `--config` / `-c`, or from
`FORGEMIRROR_CONFIG`. Without either, ForgeMirror uses the first of these
that exists:
- `./forgemirror.yaml`
- `$XDG_CONFIG_HOME/forgemirror/forgemirror.yaml` on Linux, or
  `~/Library/Application Support/forgemirror/forgemirror.yaml` on macOS

**Unknown keys are errors.** A typo such as `max_concurent` stops the service
at startup instead of being silently ignored. All errors are reported
together, so one run shows everything that needs fixing:

```sh
forgemirror --config forgemirror.yaml validate
```

## CLI

| Flag / command           | Overrides            | Description                                    |
|--------------------------|----------------------|------------------------------------------------|
| `--config`, `-c`         |                      | Configuration file (env `FORGEMIRROR_CONFIG`)  |
| `--listen-address`       | `server.listen_address` | webhook listener, e.g. `:8081`              |
| `--ops-listen-address`   | `server.ops_listen_address` | health and metrics listener, e.g. `:9090` |
| `--ui-listen-address`    | `ui.listen_address`  | web UI listener, e.g. `:8080`                  |
| `--log-level`            | `log.level`          | `debug`, `info`, `warn`, `error`               |
| `--log-format`           | `log.format`         | `json`, `text`                                 |
| `validate`               |                      | Load and validate the configuration, then exit |
| `--version`              |                      | Version, commit and Go version                 |

## Listeners

ForgeMirror serves three audiences on three ports, so each one can be
exposed or locked down as a whole, without path rules in a proxy:

| Key                         | Default | Serves                                 | Audience                     |
|-----------------------------|---------|----------------------------------------|------------------------------|
| `server.listen_address`     | `:8081` | `POST /webhook`                        | Forgejo                      |
| `server.ops_listen_address` | `:9090` | `/healthz`, `/readyz`, `/metrics`      | kubelet, Prometheus          |
| `ui.listen_address`         | `:8080` | the [web UI](../web-ui/), only with `ui.enabled: true` | operators, behind a proxy or SSO |

The addresses must differ. Exposing the webhook port, e.g. through an
ingress for a Forgejo in another network, exposes nothing but `/webhook`,
and requests without a valid signature are rejected.

{{< callout type="warning" >}}
Upgrading from v0.2 or earlier: the webhook moved from `:8080` to `:8081`,
and probes and metrics moved to `:9090`. Update the Forgejo webhook URL,
probes, Prometheus scrape configs and NetworkPolicies, or set
`server.listen_address: ":8080"` to keep the old webhook port.
{{< /callout >}}

## Persistent state

```yaml
state:
  database: data/state.db # default; relative to the process working directory
  history_limit: 50       # retained attempts per repository; 1–1000
  history_retention: 720h # age limit; must be positive
```

The SQLite database is mandatory and enabled by default. An empty path is
rejected; there is no in-memory mode. Container and Helm/Kubernetes examples
use `/var/lib/forgemirror/state.db` on persistent storage instead.

SQLite retains bounded sync history, the latest sync attempt, its per-target results and the latest
read-only comparison per repository, independently of whether the UI is enabled.
Set `source.base_url` and `source.token` (or `source.token_file`) so startup
recovery can rediscover repositories under current filters. Webhook-only
operation without API credentials remains supported: saved history stays
stale/non-actionable until an admitted webhook arrives. Persistence alone
does not change your private-repository publication policy.

Use a writable **local persistent filesystem**, separate from `mirror.work_dir`.
The parent directory is created with private permissions; the container runs as
UID/GID 65532, so mount ownership must allow that user to write. SQLite also
creates `-wal`, `-shm` and `.lock` siblings. Run one ForgeMirror process per
database, not multiple replicas or writers; network filesystems such as NFS/SMB
are unsupported. Open, schema or ownership errors fail startup rather than
silently dropping back to memory. Storage failures block new applications until
their pre-sync checkpoint succeeds.

See [operations](../operations/#persistent-state-and-backups) for recovery and
backups, and the deployment READMEs for default persistent-volume configuration.

## First-publication protection

```yaml
ui:
  enabled: true
sync:
  first_publication_approval: true # default false
```

This policy requires explicit authorization before a repository can publish
to a destination. It applies to Git refs, LFS objects, releases, images,
repository creation and settings, regardless of automatic/manual entry point.
Read-only planning continues. Deletion removes exposure and needs no
first-publication grant, but still obeys pauses.

Override the global setting with `targets[].first_publication_approval` or
`targets[].overrides[].first_publication_approval` for matching repositories.
These policy-only overrides also work for untyped Git targets without a
`settings` section. Last matching override wins. Enabling protection requires
the UI, but does not by itself require source API credentials.

The target card shows its destination and **Approve destination and sync**.
Confirmation grants **ongoing** mirroring to this source/destination pair and
requests a fresh target-scoped sync. It does not approve an exact snapshot:
new source commits may be included. Grants survive restarts; source/destination
URL changes do not match an old grant, while credential rotation alone does
not change identity. Stale browser forms are rejected if the mapping changed.

Existing mirrors are not silently adopted when protection is enabled: every
unapproved scope requires a grant, even if Git refs already match. There is
no bulk approval. Set a scope's policy to false only when you intentionally
want ordinary automatic publication there. Approval never bypasses a pause.

## Outbound notifications

`notifications.webhook_url` (or `webhook_url_file`) enables a generic JSON
webhook with a durable outbox. The URL is secret/redacted. Optional `token`
(or `token_file`) supplies a dedicated bearer credential; `ui_url` supplies an
explicit public UI root for links. `timeout` defaults to `10s` and must be
positive. Notifications are disabled when no delivery URL is configured.
See [operations](../operations/#blocked-sync-notifications) for payload,
deduplication, retry and failure behavior.

## Secrets

Every secret has two forms: an inline value, or a `*_file` path to read it
from. Set one or the other; setting both is an error. Surrounding whitespace
in files is trimmed. Prefer the `_file` form wherever files can be mounted,
such as Kubernetes Secrets, Docker secrets or systemd credentials.

| Inline            | From file              |
|-------------------|------------------------|
| `webhook.secret`  | `webhook.secret_file`  |
| `source.token`    | `source.token_file`    |
| `targets[].token` | `targets[].token_file` |

Secrets never appear in logs, error messages or on disk.
For how tokens reach git, see [Security](../security/#credentials).

## Source

`source` is the Forgejo instance ForgeMirror clones from.

```yaml
source:
  base_url: https://git.example.org/   # recommended
  username: oauth2                     # default
  token_file: /run/secrets/forgemirror/source-token
```

The clone URL comes from the webhook payload, and the token is sent to
whatever host it names. With `base_url` set, payloads whose clone URL is not
below that URL (same scheme, host, port and path prefix) are rejected with
`400`, so the token only ever reaches your Forgejo, even if the webhook secret
leaks. Without it, every clone URL is accepted.

The token is only needed for private repositories; leave it unset if only
public ones are mirrored. It becomes required, together with `base_url`, as
soon as a target has a `type`, `sync.schedule` is set or `sync.auto` is
`false`, because ForgeMirror then reads repositories, settings and labels
through the Forgejo API
(`base_url` + `api/v1`). Required scopes are listed in
[Token permissions](#token-permissions).

Forgejo clients connect on first use, not at startup, and a failed connection
attempt is not repeated for 10 seconds. ForgeMirror therefore starts even
when Forgejo is unreachable, and a scheduled sweep does not hammer an
unreachable instance once per repository.

## Token permissions

This is the token setup reference. Permissions depend on the enabled features,
not just the forge: a plain Git destination needs repository push access,
whereas a typed target also synchronizes settings and, by default, labels,
releases and packages for public repositories.

Configure source credentials in `source.token` / `source.token_file` and one
destination credential in each `targets[].token` / `token_file`. That target
credential is also used for its registry; there is no separate registry-token
setting. Use the actual token account for `username` when the registry requires
it. Provider roles, repository coverage, organisation approval/SSO policies,
protected branches and force-push rules still apply: token scopes do not
implicitly grant membership or bypass those restrictions.

### Forgejo source permissions

The source token is read-only. Add the rows needed by your setup:

| Feature | Required source permission | Configuration / notes |
|---------|----------------------------|-----------------------|
| Git clone and LFS download | `read:repository` | Same repository credential for Git and LFS; anonymous public access can be used where the instance permits it |
| Repository details, topics, published releases and attachments | `read:repository` | Typed targets read these through the Forgejo API |
| Repository discovery for schedules, review mode and startup recovery | `read:repository` | Listing only returns repositories visible to the token's account |
| Label synchronization | `read:issue` | Label routes belong to Forgejo's issue scope; labels default to enabled |
| Container package discovery and authenticated pulls | `read:package` | Needed when copying source packages; target `settings.packages` defaults to enabled for public repositories |
| Private repository visibility | Repository access **All (public, private, and limited)** plus account access | A public-only token hides private repositories, regardless of `filter.allow_private` |

Use a bot account with read access to the owners you mirror. An administrator
account can make discovery return every repository; admin scopes are not a
requirement. Forgejo's **Specific repositories** token mode supports only
repository/issue scopes, not package scopes: choose an access mode that covers
all enabled features.

The source token is optional for anonymous public Git/LFS mirroring when
there are no typed targets, no schedule and no review-mode repository listing.
The mandatory database and first-publication policy do not by themselves
require source API credentials. Supplying source API credentials enables
startup rediscovery; webhook-only setups wait for newly admitted events.

### GitHub destination permissions

For a fine-grained PAT, choose the destination's resource owner and repository
coverage. If ForgeMirror creates repositories, the token must cover newly
created repositories too, not only a fixed list of existing ones.

| Feature | Fine-grained PAT repository permission | Classic PAT scope |
|---------|----------------------------------------|-------------------|
| Git push/prune and repository-content access, including LFS | **Contents: read and write** | `repo`, or `public_repo` when every destination remains public |
| Settings, topics, mirror identity, repository creation and deletion | **Administration: read and write** | `repo` / `public_repo`; add `delete_repo` for `delete: true` |
| Label synchronization (default on) | **Issues: read and write**; GitHub also permits the equivalent Pull requests permission set | Covered by `repo` / `public_repo` |
| Published releases and release attachments (default on) | **Contents: read and write** | Covered by `repo` / `public_repo` |
| Changes under `.github/workflows/` | **Workflows: read and write** in addition to Contents | Add `workflow` |
| Container images in `ghcr.io` | **Not supported by fine-grained PATs** | Add `write:packages` (includes package download/upload); account must have package access |

GitHub's required Metadata read permission is included automatically. A
fine-grained token is restricted to one resource owner; organisation approval
or SSO authorization may also be necessary. Ensure the bot is an appropriate
member/owner, not an unsupported outside-collaborator token arrangement.

**Package sync is on by default for public repositories.** Use a classic token
with repository permissions and `write:packages` when copying images to
GHCR. If choosing a fine-grained token, explicitly set `settings.packages:
false` for that target. ForgeMirror never deletes registry tags, so it does
not require `delete:packages`.

Provider references: [PAT types and limitations](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens),
[label API permissions](https://docs.github.com/en/rest/issues/labels#create-a-label),
and [GHCR authentication](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry#authenticating-to-the-container-registry).

### GitLab destination permissions

Use a personal, group or project access token appropriate to the destination
namespace. A project token is scoped to its existing project; it is not a
credential for creating arbitrary sibling projects.

| Feature | Required scope / access |
|---------|-------------------------|
| Plain Git push/prune and LFS | `write_repository`; a personal access token's `api` scope also grants Git-over-HTTP write access |
| Typed-target settings, labels, releases/attachments, creation and deletion | `api`, with the project/group role needed for each operation |
| Git with a group/project access token | Include `write_repository`; do not assume the personal-token `api` Git-over-HTTP implication applies to every token type |
| Container image comparison and push | `read_registry` and `write_registry`, or broad `api` access that includes the registry; the registry must be enabled |

For a typical typed destination using a **personal access token**, `api`
covers Git, API and registry access. For group/project tokens, include explicit
Git-over-HTTP rights and verify namespace coverage. GitLab's registry-only
scope profile is `read_registry` + `write_registry`; its current fine-grained
PAT registry permissions do not support image push. A correctly scoped token
can still be denied by branch protection or the account's role.

Provider references: [token scope matrix](https://docs.gitlab.com/security/tokens/access_token_scopes/),
[Git-over-HTTP authentication](https://docs.gitlab.com/user/profile/personal_access_tokens/#use-git-over-https),
and [registry authentication](https://docs.gitlab.com/user/packages/container_registry/authenticate_with_container_registry/).

### Forgejo and Codeberg destination permissions

These scope names apply to Forgejo-based targets. Features such as registries
may be disabled on a particular instance; disable the corresponding sync
feature instead of adding unrelated permissions.

| Feature | Required target permission |
|---------|----------------------------|
| Git push/prune, LFS, repository metadata, topics, mirror identity and releases/attachments | `write:repository` |
| Label synchronization (default on) | `write:issue` |
| Create in an organisation | `write:organization`, in addition to repository permission and an account allowed to create there |
| Create in the token account's personal namespace | `write:user`, plus `read:organization` for ForgeMirror's namespace probe, in addition to repository permission |
| Delete a repository (`delete: true`) | `write:repository` plus the account's permission to delete it |
| Container package/registry writes | `write:package`, with access to that package owner/registry |

Write scopes include their corresponding read operations. Creation first
checks whether the namespace is an organisation; personal creation then checks
the authenticated user. ForgeMirror does not create repositories in an
unrelated user's personal namespace. **Specific repositories** tokens cannot
carry organisation/user/package scopes and restrict administrative operations;
use an access mode that covers creation, visibility changes and package sync
when those features are enabled.

Provider reference: [Forgejo access-token scopes and repository access modes](https://forgejo.org/docs/latest/user/token-scope/).

### How the new controls affect token permissions

- LFS mirroring and **Repair LFS** need source read and destination write access;
  repair uploads objects even when refs already match. Provider LFS enablement,
  storage quotas and object-size limits are separate from token permissions.
- Public mirror identity uses existing repository settings permissions; it does
  not edit README files or commit history.
- Pause/resume, retries, history and publication explanations are local control
  features and need no extra forge scopes.
- First-publication approval authorizes ForgeMirror's ongoing destination
  mapping; it does not grant rights on the forge or replace token permissions.
- Outbound notifications use their own optional bearer token and secret URL,
  never a source or destination forge token.

Disable unused `settings.labels`, `settings.releases` or `settings.packages`
features before omitting their permissions. A temporary pause or review mode
is not a reason to provision an incomplete token: planning still performs
read-only API calls, and a later approved sync needs its write permissions.

## Sync

`sync` selects what starts a mirror run.

```yaml
sync:
  auto: true      # default; false enables review mode (see below)
  on_push: true   # default; Forgejo push webhooks start runs
  schedule: 1h    # Go duration; 0s (default) disables scheduled runs
```

| Mode               | Configuration                          | Needs                                  |
|--------------------|----------------------------------------|----------------------------------------|
| Push only          | default                                | `webhook.secret`                       |
| Push and schedule  | `schedule: 1h`                         | `webhook.secret`, `source.base_url`, source token |
| Schedule only      | `on_push: false`, `schedule: 1h`       | `source.base_url`, source token; no webhook |
| Review mode        | `auto: false`                          | `ui.enabled`, `source.base_url`, source token |

- **Schedule:** the first run starts right after startup, then one per
  interval. Each run covers every repository the source token can see that
  passes the [filter](#filter); branch rules do not apply. Runs are named
  `schedule-<RFC3339 time>` in the logs (`delivery`). A repository that is
  still being mirrored is not mirrored twice in parallel.
- **Skip when equal:** every run first compares branches and tags
  (`git ls-remote`) of the source and every target. Targets that are
  identical get no push; when all are, the clone is skipped too. Settings
  and labels are still synchronised.
- **Review mode:** with `auto: false`, pushes, scheduled runs and deletions
  only compute what a sync would change. The [web UI](../web-ui/) shows the
  diff per repository. Publication requires an explicit UI authorization:
  *Sync*, *Sync all*, *Resume*, *Approve destination and sync*, or *Repair LFS*
  (objects only). Previously authorized explicit retries can continue while
  still obeying pause and approval policy. `on_push` and `schedule` decide when
  the diffs are refreshed; neither is required. At startup all diffs are
  computed once. Use it to review the first run of a new deployment.
- **Schedule-only:** with `on_push: false`, `webhook.secret` is only
  required if a target sets `delete`. Without a secret, `/webhook` answers
  `404`. With a secret but `on_push: false`, push events are answered with
  `200` and ignored.
- `on_push: false` without a schedule is an error, unless `auto` is `false`.
- **Deletion is explicit policy, not listing reconciliation.** A sweep does
  not infer deletion merely because a repository is absent from its listing.
  With `delete: true`, an admitted deletion event or a run whose fresh source
  lookup reports not found can plan target deletion; review mode still needs
  explicit application, and admitted deletion retries can run later. Verify
  source visibility before enabling deletion; see [`delete`](#typed-targets).

## Targets

A target is a forge that receives the mirror. `url` is a
[Go template](https://pkg.go.dev/text/template), rendered for each repository
with these fields:

| Field           | Example    |
|-----------------|------------|
| `{{ .Owner }}`    | `acme`     |
| `{{ .Name }}`     | `app`      |
| `{{ .FullName }}` | `acme/app` |

```yaml
targets:
  - name: github            # unique; used in logs and metrics
    url: "https://github.com/acme/{{ .Name }}.git"
    username: oauth2        # default; GitHub, Codeberg and GitLab accept it
    token_file: /run/secrets/forgemirror/github-token
  - name: codeberg
    url: "https://codeberg.org/{{ .Owner }}/{{ .Name }}.git"
    token_file: /run/secrets/forgemirror/codeberg-token
```

{{< callout type="warning" >}}
A URL without `{{ .Owner }}` maps repositories of different owners to the same
target: `acme/app` and `bob/app` would both push, with `--prune`, to
`github.com/acme/app`. On a Forgejo instance with more than one user, combine
such a target with `filter.include_repos`, e.g. `acme/.*`.
{{< /callout >}}

Each URL is checked at startup:
- It must render, so unknown fields are rejected.
- It must use `http` or `https` and include a host.
- It must not contain credentials. Those belong in `token`.

{{< callout type="info" >}}
Without a `type`, target repositories must already exist (or be created by
[`create`](#typed-targets) on a typed target). ForgeMirror pushes all
branches and tags with `--prune`, so the target becomes an exact copy and
branches deleted on Forgejo disappear there too. Pull request refs are not
pushed, because GitHub rejects them.
{{< /callout >}}

### Typed targets

A target without `type` is a plain git push destination, exactly as before.
With a `type`, ForgeMirror also talks to the forge's API: it can create the
repository, keep its settings and labels in line with Forgejo, copy releases
and container packages, and delete it.

```yaml
targets:
  - name: github
    type: github                       # github | gitlab | forgejo
    url: "https://github.com/acme/{{ .Name }}.git"
    token_file: /run/secrets/forgemirror/github-token
    create: true                       # default false
    settings:                          # defaults for this target
      has_issues: false
      has_wiki: false
    overrides:                         # per repository, in order
      - repos: "acme/forgemirror"
        settings:
          has_issues: true
      - repos: "acme/homelab"
        settings:
          private: true
```

| Key            | Default | Meaning                                                                 |
|----------------|---------|-------------------------------------------------------------------------|
| `type`         |         | `github`, `gitlab` or `forgejo`; enables the keys below                  |
| `api_url`      | derived | API base URL. Derived from `type` and the host of `url`: `https://api.github.com` for github.com, `https://<host>/api/v3` for other GitHub hosts, `https://<host>/api/v4` for GitLab, `https://<host>/api/v1` for Forgejo |
| `create`       | `false` | Create the repository when it is missing; otherwise a missing repository fails the target |
| `delete`       | `false` | Delete the repository when Forgejo reports the source as deleted        |
| `prune_labels` | `false` | Delete labels that do not exist on the source                           |
| `prune_releases` | `false` | Delete releases whose tag has no published release on the source      |
| `registry`     | derived | Container registry for package sync, as `host[:port]` or `http(s)://host[:port]`. Derived: `ghcr.io` for github.com, `registry.gitlab.com` for gitlab.com, the host of `url` for Forgejo. Required for other GitHub and GitLab hosts when `settings.packages: true` is set explicitly; with the default, their packages are skipped with a warning |
| `settings`     |         | Target-wide settings, see below                                         |
| `overrides`    |         | Settings for matching repositories, see below                           |

`create`, `delete`, `prune_labels`, `prune_releases`, `registry`, `api_url`,
`settings` and `overrides` require a `type`; the namespace and name of the repository come from the
rendered `url` (`/group/sub/name.git` is namespace `group/sub`, name `name`).

**Creating repositories:** ForgeMirror creates a repository in an
organisation or group, or in the token's own account. Any other namespace
is refused with `cannot create repository in namespace …: not an
organisation and not the token's user`, because the forge would otherwise
create it somewhere else.
On Forgejo, organisation repositories are created through the deprecated but
working `/api/v1/org/{org}/repos` route.

**Order of a run, per target:**
1. Read the source repository and labels from Forgejo; releases and
   packages too, when a target syncs them.
2. Read the target repository; create it if it is missing and `create` is
   set. The default branch is not sent, because it does not exist yet. An
   existing public repository that must be private is made private here, on
   its own; if that fails, the target is not pushed.
3. Skip the push if all branches and tags are already equal.
4. Mirror push.
5. Compare settings and update only what differs; the default branch is set
   here, after the push.
6. Synchronise labels (created and updated; deleted only with
   `prune_labels: true`). `settings.labels: false` disables this step.
7. Synchronise releases, see [Releases and packages](#releases-and-packages).
   `settings.releases: false` disables this step.
8. Copy container packages. `settings.packages: false` disables this step.

A failed create, settings, label, release or package step is logged with the
target and counted in [metrics](../operations/#metrics). Steps 7 and 8 are
bounded by `mirror.timeout`, since attachments and images can be large.

**Settings.** All keys are optional; an unset key keeps the value of the
previous layer.

| Key                 | Meaning                                                              |
|---------------------|----------------------------------------------------------------------|
| `description`       | Repository description                                               |
| `website`           | Website; ignored on GitLab, which has no such field                  |
| `topics`            | List of topics; always lower-cased, because GitHub requires it       |
| `default_branch`    | Default branch                                                       |
| `private`           | Only `true` is accepted: force a public source to be private here    |
| `has_issues`        | Issue tracker on or off                                              |
| `has_wiki`          | Wiki on or off                                                       |
| `has_pull_requests` | Pull or merge requests on or off; ignored on GitHub, which cannot disable them (logged once at startup) |
| `labels`            | `false` disables label sync for this scope; default `true`           |
| `releases`          | `false` disables release sync for this scope; default `true`         |
| `packages`          | `false` disables container package sync for this scope; default `true` |
| `mirror_note`       | Note appended to the description, see [Mirror marker](#mirror-marker); `""` disables it |
| `mirror_topic`      | Topic added to the topics, see [Mirror marker](#mirror-marker); `""` disables it; default `forgemirror` |
| `mirror_url` | Explicit public contribution URL template; also sets website where supported; `""` disables it |
| `mirror_description_limit` | UTF-8 byte limit for the resulting description, default 350; range 1–4096 |

**Precedence, last wins:** the value from the Forgejo repository, then the
target's `settings`, then every entry of `overrides` whose `repos` pattern
matches `owner/name`, in the order listed. `repos` uses the same whole-value
regular expression semantics as the [filter](#filter).

**Visibility only gets more private.** A private source repository is private
on every target, whatever the configuration says; `settings.private: false`
is rejected at startup. Label colours are compared case-insensitively
without a leading `#`.

**Forge differences:**

| Setting             | GitHub           | GitLab         | Forgejo |
|---------------------|------------------|----------------|---------|
| `website`           | synced           | ignored        | synced  |
| `has_pull_requests` | ignored, logged once at startup | synced | synced |
| `topics`            | lower-cased; only `a-z`, `0-9` and `-`, starting with a letter or digit, at most 50 characters; others (e.g. `node.js`) are skipped with a warning | lower-cased    | lower-cased |
| Create in           | organisation or own user | group or own user | organisation or own user |

`delete: true` permits target deletion for an admitted source-deletion event
or when an admitted run's fresh Forgejo lookup reports the source not found.
A missing item in a listing, a filter change or a configuration edit alone
is not deletion evidence. Review mode plans deletion until explicitly applied;
retries may repeat already admitted deletion work. A target already gone counts
as success. Deletion events intentionally bypass ordinary push filters, so
scope webhook delivery and target URL ownership carefully rather than relying
on push filters to protect unrelated destination names. Also verify source
visibility: insufficient token access can hide a repository behind a not-found
response. See [Forgejo webhook](../forgejo-webhook/#repository-events).
`prune_labels` is off by default because deleting a label strips it from every
issue and pull request, and new GitHub repositories start with default
labels.

### Mirror marker

So that visitors know a repository is a copy and that changes made there are
lost on the next push, ForgeMirror marks every repository of a typed target
as a mirror:

- `mirror_note` is appended to the description as `<description> · <note>`,
  or stands alone when the description is empty. Default:
  `Mirror, synced by ForgeMirror. Do not push here.` It is a Go
  [text/template](https://pkg.go.dev/text/template) with the same fields as
  `url` (`{{ .Owner }}`, `{{ .Name }}`, `{{ .FullName }}`) and must render to
  a single line.
- `mirror_topic` is added to the topics, also when `topics` is set. Default:
  `forgemirror`.

- `mirror_url` is an explicit public HTTP(S) contribution URL template with
  the same repository fields. It is appended as `Contribute: <url>` and sets
  the website where the forge supports one. GitLab receives the link in the
  description even though its API lacks a website field. Empty disables it.
  ForgeMirror never infers this URL from your private source address.
- `mirror_description_limit` defaults to 350 UTF-8 bytes, conservatively
  fitting supported forges. It preserves the marker and public link, truncating
  only source/overridden description text at a UTF-8 boundary. Configure
  1–4096 bytes for a destination's limits. An oversized marker produces a
  planning error instead of a repeatedly rejected settings update.

Markers are added after all settings layers are applied. A nonempty
`mirror_url` takes precedence over `website`; override/disable `mirror_url`
explicitly when changing that identity per repository. Empty strings turn
individual text markers off for that scope:

```yaml
settings:
  mirror_note: "Read-only mirror of {{ .FullName }}."
  mirror_url: "https://public.example/projects/{{ .Name }}/contribute"
  mirror_description_limit: 350
overrides:
  - repos: "acme/website"
    settings:
      mirror_note: ""
      mirror_topic: ""
      mirror_url: ""
```

Because the marker is on by default, upgrading from a version without it
changes the description and topics of every mirror on its next run; in
[review mode](#sync) the change shows up in the diff first. A topic the forge
rejects is skipped with a warning, like any other topic. Untyped targets have
no API access and are never marked. Identity changes appear in the normal
review diff and history; no README or Git commit is modified. On upgrade,
descriptions exceeding the default byte limit may be shortened.

### Releases and packages

Release and package sync are on by default for typed targets, like labels.
They copy files that may be large and need extra token scopes (see
[Token permissions](#token-permissions)); without them, the step is skipped
with a plan warning or fails and shows up in the
[metrics](../operations/#metrics). Disable them per target or
per repository:

```yaml
targets:
  - name: github
    type: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token_file: /run/secrets/forgemirror/github-token
    username: acme-bot            # registry login; see below
    settings:
      packages: false             # no container images for any repository
    overrides:
      - repos: "acme/big-binaries"
        settings:
          releases: false         # keep releases off for this repository
```

**Releases** are matched by tag. ForgeMirror creates missing releases (oldest
first, so the target's "latest" release matches), and updates the name, the
notes and the pre-release flag (GitLab has none) when they differ.
Attachments are compared by file name and size: missing ones are uploaded, a
different size replaces the file, and attachments the source no longer has
are removed. Draft releases are never copied. A release whose tag the source
does not list is skipped with a warning. Target releases without a source
release stay, unless `prune_releases: true`. On GitLab, attachments are
stored in the project's generic package `release-assets` (version = tag) and
linked from the release.

**Packages** are the container images Forgejo lists for the repository (a
package must be linked to the repository; Forgejo links container images
with the repository's name on its own). Every tag is copied with its digest,
so signatures (`sha256-….sig`) and attestations stay valid; they are copied
after the images they refer to. The image goes to
`<registry>/<target namespace>/<package name>`; on GitLab to
`<registry>/<group>/<project>/<package name>`, or the project's own image
when the package is named like the repository. Names are lower-cased.
Nothing is ever deleted from a target registry, and other package types
(npm, Helm, generic, …) are skipped with a warning. When the registry denies
access, the remaining tags of that package are skipped, since they would
fail the same way; the error names how many.

Packages are only synced for public repositories: on GitHub and Forgejo a
package's visibility does not follow the repository, so a private
repository's images could become public.

The registry login uses the target's `username` and token. Set `username`
to the account the token belongs to: ghcr.io and GitLab check it, while
forgejo-style targets accept any name.

{{< callout type="warning" >}}
**GitHub (ghcr.io):** a newly copied package is private and not linked to the
repository. GitHub links images only through the
`org.opencontainers.image.source` label, which ForgeMirror cannot add without
changing the digest. After the first copy, open the package on GitHub once,
link it to the repository and make it public.
{{< /callout >}}

## Filter

Without a `filter` section, every push is mirrored. Each configured rule must
pass; the first rule that fails is reported in the webhook response and the
log.

| Rule               | Effect                                                                         |
|--------------------|--------------------------------------------------------------------------------|
| `include_branches` | Only pushes to these branches trigger a mirror. Tag pushes always do.          |
| `require_topic`    | Only repositories with this Forgejo topic, for per-repository opt-in. Not an access control: every repository owner can set topics. |
| `exclude_private`  | Skip private repositories. Only with untyped targets, no schedule and automatic mode; see below. |
| `allow_private`    | Private repositories whose `owner/name` matches are synced, see below. They stay private on every target. |
| `exclude_forks`    | Skip forks.                                                                    |
| `include_repos`    | Only repositories whose `owner/name` matches.                                  |
| `exclude_repos`    | Skip repositories whose `owner/name` matches; this wins over `include_repos`.   |

Patterns are [Go regular expressions](https://pkg.go.dev/regexp/syntax) that
must match the whole value: `app` matches only `app`, not `my-app`; use
`.*app` for that.

```yaml
filter:
  include_repos: "acme/.*"
  exclude_repos: "acme/(secrets|infra-.*)"
  include_branches: ["main", "release/.*"]
  exclude_forks: true
  require_topic: mirror
  exclude_private: true
  allow_private: "acme/notes"   # private, but mirrored anyway; stays private
```

**Private repositories.** With any [typed target](#typed-targets), a
`sync.schedule`, or review mode (`sync.auto: false`), a private repository is
synced only if `allow_private` matches it, and `exclude_private` has no effect.
With only untyped targets, no schedule and automatic mode, `exclude_private` skips private repositories and
`allow_private` makes exceptions; without `exclude_private` they are
mirrored, as before typed targets existed. Either way `allow_private` needs a
source token with access to private repositories (see
[Token permissions](#token-permissions)). A filter only decides *whether* a push
triggers a mirror. The mirror itself
always copies all branches and tags.

## Git LFS

Git LFS mirroring is automatic, using the official `git-lfs` CLI; there is no
opt-in switch. Binary installations need Git and Git LFS on PATH. Images bundle
both. Standard HTTP(S) LFS endpoints use the existing source/target repository
credentials; custom repository-supplied endpoints and transfer agents are not
supported.

Transfers cover LFS objects in history reachable from published branches and
tags, not hidden forge refs. Source objects are fetched once per workspace and
uploaded to each destination before its Git refs. An LFS failure holds back
that destination's push. Temporary objects live under `mirror.work_dir`; allow
sufficient disk space and tune `mirror.timeout` for large initial transfers.

Unchanged Git-ref checks stay cheap and do not audit LFS. **Repair LFS** is an
explicit write action that reconciles source-history objects on existing
destinations without pushing refs or unrelated changes, including in review
mode. It still obeys pauses and destination approval. See
[operations](../operations/#git-lfs) for scope, failure handling and limitations.

## Concurrency and timeouts

| Key                       | Default | Meaning                                                                  |
|---------------------------|---------|--------------------------------------------------------------------------|
| `mirror.max_concurrent`   | `4`     | Maximum concurrent Git/LFS operations (clones, transfers and pushes), across all repositories; forge API steps of typed targets (reading the source, creating, syncing settings and labels, deleting) are capped separately by the same number |
| `mirror.timeout`          | `10m`   | Upper bound for each Git/LFS command and each release/package transfer step                                         |
| `server.shutdown_timeout` | `60s`   | Budget for a graceful shutdown, covering open requests and running mirrors |

Only one mirror run per repository is active at a time. Pushes that arrive
during a run are coalesced into one follow-up run.

## Editor support

A [JSON Schema](https://json-schema.org/) for the configuration file is
published with this documentation:
[`schema/v1/forgemirror.schema.json`](../../schema/v1/forgemirror.schema.json).
Editors use it for completion, hover documentation and checks for unknown
keys, wrong types and invalid values while you type.

The schema is a convenience for editors. `forgemirror validate` remains the
authoritative check, because some rules can only be checked at runtime:
regular expressions must compile, target names must be unique, and values
can also come from environment variables.

**YAML language server** (VS Code with the Red Hat YAML extension, Neovim,
Helix, Zed and others): add this as the first line of `forgemirror.yaml`. The
example file already starts with it.

```yaml
# yaml-language-server: $schema={{< schema-url >}}
```

**By file name**, for all projects at once, in VS Code `settings.json`:

```json
{
  "yaml.schemas": {
    "{{< schema-url >}}": "forgemirror*.yaml"
  }
}
```

JetBrains IDEs: *Settings → Languages & Frameworks → Schemas and DTDs → JSON
Schema Mappings*. Add the URL and the file pattern `forgemirror*.yaml`.

**In CI**, validate with any JSON Schema tool, e.g.
[check-jsonschema](https://check-jsonschema.readthedocs.io/):

```sh
check-jsonschema --schemafile {{< schema-url >}} forgemirror.yaml
```

The `v1` in the path stays compatible: new optional keys may be added, and a
breaking change gets a new path. The forge-sync keys were added this way
without a new version, so existing files keep validating. The schema
describes `sync` (`on_push`, `schedule`), `filter.allow_private`, and for
targets `type`, `api_url`, `create`, `delete`, `prune_labels`, `settings`
(`description`, `website`, `topics`, `default_branch`, `private`,
`has_issues`, `has_wiki`, `has_pull_requests`, `labels`, `releases`,
`packages`), `prune_releases`, `registry` and `overrides`
(`repos`, `settings`). `type` is limited to `github`, `gitlab` and `forgejo`,
and `settings.private` accepts only `true`.

## Full reference

The annotated example file from the repository, with all keys and their
defaults:

{{< repo-file "forgemirror.example.yaml" >}}
