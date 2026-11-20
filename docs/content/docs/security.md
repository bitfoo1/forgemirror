---
title: Security
weight: 7
---

## Threat model

- **Forgejo stays private.** Forgejo only makes outgoing webhook calls to
  ForgeMirror. ForgeMirror connects outward: to Forgejo to clone, read its
  API and pull container images, and to the targets to push, call their
  forge APIs and push container images. Nothing on the internet needs access
  to Forgejo.
- **ForgeMirror is not an internet service.** Expose `/webhook` only to
  Forgejo's network. The webhook secret authenticates requests, but there is
  no reason to make the endpoint publicly reachable.
- **The web UI has no login.** It is off by default and runs on its own
  port. When enabled, protect that port with a proxy or SSO; see
  [Web UI](../web-ui/#secure-it). It shows no configured secrets. Refresh,
  sync, LFS repair, pause/resume and first-publication approval actions are
  protected against cross-site requests.
- **Probes and metrics stay internal.** They are served on the ops port,
  separate from the webhook, so exposing the webhook exposes nothing else.
- **Targets get everything.** The mirror copies all branches and tags. A
  [typed target](../configuration/#typed-targets) also gets the repository
  settings and labels, and, when enabled, every published release with its
  attachments and, for public repositories, every container image tag. Use
  [filters](../configuration/#filter) to keep repositories with sensitive
  history or artifacts from being mirrored at all.
- **Every Forgejo user can trigger a mirror.** With a system or organization
  webhook, any user who can push to a covered repository starts a mirror run.
  `require_topic` does not restrict this, because repository owners set
  topics themselves. Restrict by owner with `include_repos`, and include
  `{{ .Owner }}` in target URLs (see [Targets](../configuration/#targets)).

## Webhook authentication

Every request must carry an HMAC-SHA256 signature of its body, made with
`webhook.secret`. ForgeMirror checks the signature before it parses the body.
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
the webhook payload; with `base_url`, ForgeMirror rejects payloads whose
clone URL points anywhere else, so not even a leaked webhook secret can send
the Forgejo token to another host.

Token types, scopes and per-feature permission requirements are part of the
setup reference: see [Configuration → Token permissions](../configuration/#token-permissions).

Forge API redirects to another host are refused. Release attachment downloads
may be redirected to object storage without forwarding the forge token, and
HTTPS-to-HTTP redirects are refused. Git LFS similarly keeps forge credentials
out of cross-origin object-storage requests; raw CLI diagnostics and diagnostic
files are suppressed so they cannot persist authentication headers. Outbound
notification URLs and bearer tokens are separate credentials, not reused forge
tokens.

## Safety rules for typed targets

- **Private stays private.** A private source repository is created and kept
  private on every target. `settings.private` accepts only `true`, so
  configuration can hide a public repository but never expose a private one.
- **Private repositories are opt-in.** With any typed target, a
  `sync.schedule`, or review mode, a private repository is synced only if
  `filter.allow_private` matches it, and `filter.exclude_private` has no
  effect. With only untyped targets, no schedule and automatic mode, `filter.exclude_private`
  skips private repositories and `filter.allow_private` makes exceptions;
  set `exclude_private: true` there to keep them out.
- **Deletion is opt-in.** `delete` defaults to `false`. An admitted deletion
  event or a fresh source lookup reporting not found can plan target deletion;
  review mode still requires application, and admitted work may be retried.
  Absence from a repository listing or a filter change alone does not delete
  targets. Keep source visibility reliable: a hidden repository can also be
  reported as not found. Deletion events bypass ordinary push filters; scope
  webhook coverage and destination naming accordingly. Enable `delete` only
  where this behavior is intended.
- **Label pruning is opt-in.** `prune_labels` defaults to `false`, because a
  deleted label disappears from every issue and pull request.
- **Releases and packages are on by default, deletion stays opt-in.**
  `settings.releases` and `settings.packages` default to `true`; set them to
  `false` to keep a target's token free of release or package scopes. Draft
  releases are never copied, and release pruning (`prune_releases`) is
  opt-in. Packages of
  private repositories are never copied, because package visibility on
  GitHub and Forgejo does not follow the repository, and nothing is ever
  deleted from a target registry.

## Persistent review state

Mandatory SQLite state contains repository names, plan contents and results,
including metadata from private repositories. Protect its directory and backups
like other operational data. Configured credentials are redacted before state
is written; release asset/download and upload URLs are excluded from persisted
plans. Tokens are still supplied separately at runtime, never restored from
state. Historical plans/results never authorize execution: current source
admission and filters still apply. Explicit destination grants and retry intent
are separate durable control records; they do not bypass current pause,
first-publication or review policies. The UI still needs proxy/SSO protection.

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
