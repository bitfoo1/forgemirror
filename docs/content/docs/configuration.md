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
(`base_url` + `api/v1`). The rights it needs are listed under
[Security](../security/#token-rights).

Forgejo clients connect on first use, not at startup, and a failed connection
attempt is not repeated for 10 seconds. ForgeMirror therefore starts even
when Forgejo is unreachable, and a scheduled sweep does not hammer an
unreachable instance once per repository.

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
  diff per repository, and nothing reaches a target until you press *Sync*
  (per repository) or *Sync all*. `on_push` and `schedule` then decide when
  the diffs are refreshed; neither is required. At startup all diffs are
  computed once. Use it to review the first run of a new deployment.
- **Schedule-only:** with `on_push: false`, `webhook.secret` is only
  required if a target sets `delete`. Without a secret, `/webhook` answers
  `404`. With a secret but `on_push: false`, push events are answered with
  `200` and ignored.
- `on_push: false` without a schedule is an error, unless `auto` is `false`.
- **Deletions need a webhook.** Scheduled runs never delete; see
  [`delete`](#typed-targets).

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

`delete: true` removes the target repository when Forgejo sends a
`repository` event with action `deleted`. Only that event deletes: not an
exclusion by the filter, not a repository turning private, not a
configuration change, and not a scheduled run. The event goes through the
[filter](#filter) first, and a target repository that is already gone counts
as success. See [Forgejo webhook](../forgejo-webhook/#repository-events).
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
[Security](../security/#token-rights)); without them, the step is skipped
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
| `exclude_private`  | Skip private repositories. Only with untyped targets and no `sync.schedule`, see below. |
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

**Private repositories.** With any [typed target](#typed-targets) or a
`sync.schedule`, a private repository is synced only if `allow_private`
matches it, and `exclude_private` has no effect. With only untyped targets
and no schedule, `exclude_private` skips private repositories and
`allow_private` makes exceptions; without `exclude_private` they are
mirrored, as before typed targets existed. Either way `allow_private` needs a
source token with access to private repositories (see
[Security](../security/#token-rights)). A filter only decides *whether* a push
triggers a mirror. The mirror itself
always copies all branches and tags.

## Concurrency and timeouts

| Key                       | Default | Meaning                                                                  |
|---------------------------|---------|--------------------------------------------------------------------------|
| `mirror.max_concurrent`   | `4`     | Maximum git processes (clones and pushes) running at once, across all repositories; forge API steps of typed targets (reading the source, creating, syncing settings and labels, deleting) are capped separately by the same number |
| `mirror.timeout`          | `10m`   | Upper bound for each git command                                         |
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
