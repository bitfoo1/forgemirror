---
title: Security
weight: 6
---

## Threat model

- **Forgejo stays private.** Forgejo only makes outgoing webhook calls to
  forgemirror. forgemirror connects outward to Forgejo (clone) and to the
  targets (push). Nothing on the internet needs access to Forgejo.
- **forgemirror is not an internet service.** Expose `/webhook` only to
  Forgejo's network. The webhook secret authenticates requests, but there is
  no reason to make the endpoint publicly reachable.
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

Use tokens with the narrowest scope:
- **Forgejo:** `read:repository`.
- **GitHub:** a fine-grained token limited to the mirror repositories, with
  *Contents: read and write*.

Store tokens as files (`*_file`) from a secret manager rather than inline.

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
