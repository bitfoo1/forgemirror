---
title: Security
weight: 7
---

## Threat model

- **Forgejo stays private.** Forgejo only makes outgoing webhook calls to
  forgemirror. forgemirror connects outward to Forgejo (clone) and to the
  targets (push). Nothing on the internet needs access to Forgejo.
- **forgemirror is not an internet service.** Expose `/webhook` only to
  Forgejo's network. The webhook secret authenticates requests, but there is
  no reason to make the endpoint publicly reachable.
- **The web UI has no login.** It is off by default and runs on its own
  port. When enabled, protect that port with a proxy or SSO; see
  [Web UI](../web-ui/#secure-it). It shows no secrets; its only actions
  are refresh and sync, protected against cross-site requests.
- **Probes and metrics stay internal.** They are served on the ops port,
  separate from the webhook, so exposing the webhook exposes nothing else.
- **Targets get everything.** The mirror copies all branches and tags. Use
  [filters](../configuration/#filter) to keep repositories with sensitive
  history from being mirrored at all.
- **Every Forgejo user can trigger a mirror.** With a system or organization
  webhook, any user who can push to a covered repository starts a mirror run.
  `require_topic` does not restrict this, because repository owners set
  topics themselves. Restrict by owner with `include_repos`, and include
  `{{ .Owner }}` in target URLs (see [Targets](../configuration/#targets)).

## Webhook authentication

Every request must carry an HMAC-SHA256 signature of its body, made with
`webhook.secret`. forgemirror checks the signature before it parses the body.
Signatures are compared in constant time, and request bodies are capped at
`webhook.max_body_bytes`. Use a long random secret:

```sh
openssl rand -hex 32
```

## Credentials

Tokens are needed for cloning (`source.token`) and pushing
(`targets[].token`). They are handled so that they cannot leak through the
usual channels:

| Channel                     | Protection                                                      |
|-----------------------------|-----------------------------------------------------------------|
| Process list (`ps`, `/proc`) | Tokens are never put in git's arguments or URLs                |
| Disk                        | The clone's `.git/config` never contains credentials; clones are deleted after each run |
| Logs and error messages     | Tokens are redacted from git output and never logged            |
| Configuration dumps         | The secret type renders as `[REDACTED]` in any formatted output |
| Other hosts                 | A credential is scoped to its exact remote URL, so it is never sent to another host, including on redirects |

Technically, each git command receives its credential as a URL-scoped
`http.<url>.extraHeader` through `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/
`GIT_CONFIG_VALUE_0` environment variables. git runs isolated from the
host's configuration (`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`,
inherited `GIT_*` variables removed) and never prompts
(`GIT_TERMINAL_PROMPT=0`).

Set [`source.base_url`](../configuration/#source). The clone URL comes from
the webhook payload; with `base_url`, forgemirror rejects payloads whose
clone URL points anywhere else, so not even a leaked webhook secret can send
the Forgejo token to another host.

Use tokens with the narrowest scope; see [Token rights](#token-rights).

## Token rights

Grant only what the features you enable need. All tokens are sent as an
`Authorization` header and only to their own host; API redirects to another
host are refused. Release attachment downloads may be redirected to object
storage: such a redirect is followed without the token, and a redirect from
HTTPS to HTTP is refused.

### Forgejo source (`source.token`)

Needed for cloning, and for the API reads of typed targets and the schedule.
It never needs a write scope.

| Scope             | Why                                                                  |
|-------------------|----------------------------------------------------------------------|
| `read:repository` | Repository details, topics, `GET /repos/search` (the schedule's repository list) and cloning |
| `read:issue`      | Labels: Forgejo files its label routes under the issue scope          |
| `read:package`    | Only with `settings.packages`: list the repository's packages and pull images from the registry |
| Repository access: all (public, private and limited) | A public-only token hides private repositories, so `filter.allow_private` cannot work |

The repository list contains what the token's user can read, and an admin
user sees every repository. Use a dedicated bot user with read access to the
organisations you mirror rather than an admin or personal account.

With only public repositories, no typed target and no schedule, the token is
optional.

### Targets (`targets[].token`)

Plain git targets (no `type`) need push permission only. Typed targets need
API rights as well:

| Target  | Token                                                                 |
|---------|-----------------------------------------------------------------------|
| GitHub  | Fine-grained token on the owner: *Administration: read and write* (create, delete, settings) and *Contents: read and write* (also releases). Classic token: `repo`, plus `delete_repo` when `delete: true`. Package sync needs a **classic** token with `write:packages`, because fine-grained tokens cannot write to ghcr.io. |
| GitLab  | `api` scope (also releases); `write_registry` for package sync        |
| Forgejo | `write:repository` (also releases), `write:issue` (labels), and `write:organization` to create in an organisation or `write:user` to create in a user account; `write:package` for package sync |

Store tokens as files (`*_file`) from a secret manager rather than inline.

### Safety rules for typed targets

- **Private stays private.** A private source repository is created and kept
  private on every target. `settings.private` accepts only `true`, so
  configuration can hide a public repository but never expose a private one.
- **Private repositories are opt-in.** With any typed target or a
  `sync.schedule`, a private repository is synced only if
  `filter.allow_private` matches it, and `filter.exclude_private` has no
  effect. With only untyped targets and no schedule, `filter.exclude_private`
  skips private repositories and `filter.allow_private` makes exceptions;
  set `exclude_private: true` there to keep them out.
- **Deletion is opt-in and narrow.** `delete` defaults to `false` and only a
  signed `repository` event with action `deleted` from Forgejo triggers it,
  after the filter. A filter or configuration change never deletes anything.
  Because deletion is destructive, give the target token delete rights only
  on targets where you set `delete: true`.
- **Label pruning is opt-in.** `prune_labels` defaults to `false`, because a
  deleted label disappears from every issue and pull request.
- **Releases and packages are opt-in.** `settings.releases` and
  `settings.packages` default to `false`. Draft releases are never copied,
  and release pruning (`prune_releases`) is opt-in as well. Packages of
  private repositories are never copied, because package visibility on
  GitHub and Forgejo does not follow the repository, and nothing is ever
  deleted from a target registry.

## Runtime hardening

The container image and both Kubernetes deployments run with:
- non-root UID 65532 (`runAsNonRoot`)
- a read-only root filesystem, with clones on an `emptyDir` that has a size
  limit
- all Linux capabilities dropped and `allowPrivilegeEscalation: false`
- the `RuntimeDefault` seccomp profile
- no service account token mounted
- the namespace enforcing the `restricted` Pod Security Standard (kustomize)

## Supply chain

Each release publishes:
- binaries and `checksums.txt`, signed with cosign (Sigstore bundle)
- SPDX SBOMs for archives and the container image
- a multi-arch image signed with cosign
- the Helm chart as an OCI artifact signed with cosign

Signatures are recorded in the public Rekor transparency log. Verify them with
the public key `cosign.pub` from the repository:

```sh
cosign verify-blob --key cosign.pub \
  --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --check --ignore-missing checksums.txt

cosign verify --key cosign.pub {{< param "project.image" >}}:<version>
cosign verify --key cosign.pub {{< param "project.chartRef" >}}:<version>
```

Every change goes through `govulncheck`, the race detector and golangci-lint
(the maratori "golden" config) before it can be merged. Renovate keeps
dependencies and tools current.

## Reporting a vulnerability

Please report vulnerabilities privately to the maintainer rather than in a
public issue.
