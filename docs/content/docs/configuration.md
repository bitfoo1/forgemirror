---
title: Configuration
weight: 2
---

forgemirror reads one YAML file. Any key can be overridden with an
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
`FORGEMIRROR_CONFIG`. Without either, forgemirror uses the first of these
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
| `--listen-address`       | `server.listen_address` | e.g. `:8080`                                |
| `--log-level`            | `log.level`          | `debug`, `info`, `warn`, `error`               |
| `--log-format`           | `log.format`         | `json`, `text`                                 |
| `validate`               |                      | Load and validate the configuration, then exit |
| `--version`              |                      | Version, commit and Go version                 |

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

`source` is the Forgejo instance forgemirror clones from.

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
soon as a target has a `type` or `sync.schedule` is set, because forgemirror
then reads repositories, settings and labels through the Forgejo API
(`base_url` + `api/v1`). The rights it needs are listed under
[Security](../security/#token-rights).

Forgejo clients connect on first use, not at startup, and a failed connection
attempt is not repeated for 10 seconds. forgemirror therefore starts even
when Forgejo is unreachable, and a scheduled sweep does not hammer an
unreachable instance once per repository.

## Sync

`sync` selects what starts a mirror run.

```yaml
sync:
  on_push: true   # default; Forgejo push webhooks start runs
  schedule: 1h    # Go duration; 0s (default) disables scheduled runs
```

| Mode               | Configuration                          | Needs                                  |
|--------------------|----------------------------------------|----------------------------------------|
| Push only          | default                                | `webhook.secret`                       |
| Push and schedule  | `schedule: 1h`                         | `webhook.secret`, `source.base_url`, source token |
| Schedule only      | `on_push: false`, `schedule: 1h`       | `source.base_url`, source token; no webhook |

- **Schedule:** the first run starts right after startup, then one per
  interval. Each run covers every repository the source token can see that
  passes the [filter](#filter); branch rules do not apply. Runs are named
  `schedule-<RFC3339 time>` in the logs (`delivery`). A repository that is
  still being mirrored is not mirrored twice in parallel.
- **Skip when equal:** a scheduled run first compares branches and tags
  (`git ls-remote`) of the source and every target. When all are identical,
  the clone and push are skipped. Settings and labels are still synchronised.
- **Schedule-only:** with `on_push: false`, `webhook.secret` is only
  required if a target sets `delete`. Without a secret, `/webhook` answers
  `404`. With a secret but `on_push: false`, push events are answered with
  `200` and ignored.
- `on_push: false` without a schedule is an error.
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
[`create`](#typed-targets) on a typed target). forgemirror pushes all
branches and tags with `--prune`, so the target becomes an exact copy and
branches deleted on Forgejo disappear there too. Pull request refs are not
pushed, because GitHub rejects them.
{{< /callout >}}

### Typed targets

A target without `type` is a plain git push destination, exactly as before.
With a `type`, forgemirror also talks to the forge's API: it can create the
repository, keep its settings and labels in line with Forgejo, and delete it.

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
| `settings`     |         | Target-wide settings, see below                                         |
| `overrides`    |         | Settings for matching repositories, see below                           |

`create`, `delete`, `prune_labels`, `api_url`, `settings` and `overrides`
require a `type`; the namespace and name of the repository come from the
rendered `url` (`/group/sub/name.git` is namespace `group/sub`, name `name`).

**Creating repositories:** forgemirror creates a repository in an
organisation or group, or in the token's own account. Any other namespace
is refused with `cannot create repository in namespace …: not an
organisation and not the token's user`, because the forge would otherwise
create it somewhere else.
On Forgejo, organisation repositories are created through the deprecated but
working `/api/v1/org/{org}/repos` route.

**Order of a run, per target:**
1. Read the source repository and labels from Forgejo.
2. Read the target repository; create it if it is missing and `create` is
   set. The default branch is not sent, because it does not exist yet. An
   existing public repository that must be private is made private here, on
   its own; if that fails, the target is not pushed.
3. Scheduled runs only: skip the push if all branches and tags are equal.
4. Mirror push.
5. Compare settings and update only what differs; the default branch is set
   here, after the push.
6. Synchronise labels (created and updated; deleted only with
   `prune_labels: true`). `settings.labels: false` disables this step.

A failed create, settings or label step is logged with the target and counted
in [metrics](../operations/#metrics).

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
| `has_pull_requests` | Pull or merge requests on or off; ignored on GitHub, which cannot disable them |
| `labels`            | `false` disables label sync for this scope; default `true`           |

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
| `has_pull_requests` | ignored          | synced         | synced  |
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
`has_issues`, `has_wiki`, `has_pull_requests`, `labels`) and `overrides`
(`repos`, `settings`). `type` is limited to `github`, `gitlab` and `forgejo`,
and `settings.private` accepts only `true`.

## Full reference

The annotated example file from the repository, with all keys and their
defaults:

{{< repo-file "forgemirror.example.yaml" >}}
