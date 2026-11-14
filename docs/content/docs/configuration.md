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

Each URL is checked at startup:
- It must render, so unknown fields are rejected.
- It must use `http` or `https` and include a host.
- It must not contain credentials. Those belong in `token`.

{{< callout type="info" >}}
Target repositories must already exist. forgemirror pushes all branches and
tags with `--prune`, so the target becomes an exact copy and branches deleted
on Forgejo disappear there too. Pull request refs are not pushed, because
GitHub rejects them.
{{< /callout >}}

## Filter

Without a `filter` section, every push is mirrored. Each configured rule must
pass; the first rule that fails is reported in the webhook response and the
log.

| Rule               | Effect                                                                         |
|--------------------|--------------------------------------------------------------------------------|
| `include_branches` | Only pushes to these branches trigger a mirror. Tag pushes always do.          |
| `require_topic`    | Only repositories with this Forgejo topic, for per-repository opt-in.          |
| `exclude_private`  | Skip private repositories.                                                     |
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
```

A filter only decides *whether* a push triggers a mirror. The mirror itself
always copies all branches and tags.

## Concurrency and timeouts

| Key                       | Default | Meaning                                                                  |
|---------------------------|---------|--------------------------------------------------------------------------|
| `mirror.max_concurrent`   | `4`     | Maximum git processes (clones and pushes) running at once, across all repositories |
| `mirror.timeout`          | `10m`   | Upper bound for each git command                                         |
| `server.shutdown_timeout` | `60s`   | Budget for a graceful shutdown, covering open requests and running mirrors |

Only one mirror run per repository is active at a time. Pushes that arrive
during a run are coalesced into one follow-up run.

## Full reference

The annotated example file from the repository, with all keys and their
defaults:

{{< repo-file "forgemirror.example.yaml" >}}
