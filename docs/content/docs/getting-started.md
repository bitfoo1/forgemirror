---
title: Getting started
weight: 1
---

This guide runs ForgeMirror locally or in a container and mirrors a first
repository. For production, see [Deployment](../deployment/).

## Prerequisites

- **Forgejo:** a Forgejo instance where you are an administrator (for a system
  webhook) or a repository owner (for a repository webhook).
- **Target tokens:** a token with push access for each target forge, for
  example a GitHub fine-grained token with *Contents: read and write* on the
  destination repositories. A [typed target](../configuration/#typed-targets)
  that creates repositories or syncs settings, releases or packages needs
  more; see [Token permissions](../configuration/#token-permissions).
- **Source token:** a read-only Forgejo token for private clones and API-driven
  features (typed targets, schedules, review mode and startup rediscovery).
  The [source permission table](../configuration/#forgejo-source-permissions)
  lists feature-specific scopes.
- **Existing target repositories:** a plain target pushes to repositories
  that already exist. Only a [typed target](../configuration/#typed-targets)
  with `create: true` creates them.

{{% steps %}}

### Install

{{< tabs >}}
  {{< tab name="Container" >}}
  Multi-arch images (linux/amd64, linux/arm64) are published with every
  release. The image runs as a non-root user and contains Git and the official
  Git LFS CLI.

  ```sh
  docker pull {{< param "project.image" >}}:<version>
  ```
  {{< /tab >}}
  {{< tab name="Binary" >}}
  Download the archive for your platform (linux or darwin, amd64 or arm64)
  from the [releases page]({{< param "project.releases" >}})
  and put `forgemirror` on your `PATH`. Git and the official `git-lfs` CLI must
  be installed. The mandatory database defaults to `data/state.db`, so the
  process needs a writable persistent working directory or a configured state
  path.
  {{< /tab >}}
  {{< tab name="From source" >}}
  Requires [mise](https://mise.jdx.dev); it installs the pinned Go toolchain.

  ```sh
  git clone {{< param "project.repo" >}}.git && cd forgemirror
  mise install && mise run build   # writes bin/forgemirror
  ```
  {{< /tab >}}
{{< /tabs >}}

### Write a configuration

Create `forgemirror.yaml`. This minimal setup mirrors every repository to the
GitHub organization `acme`, under the same repository name:

```yaml
webhook:
  secret: a-long-random-string  # openssl rand -hex 32
targets:
  - name: github
    url: "https://github.com/acme/{{ .Name }}.git"
    token: github_pat_...
```

Check it:

```sh
forgemirror --config forgemirror.yaml validate
# configuration is valid (1 target)
```

All options, including filters and secrets read from files, are covered in
[Configuration](../configuration/).

{{< callout type="info" >}}
  **Try it in review mode first.** Automatic events only compare. Explicit
  actions such as *Sync*, *Resume*, *Approve destination and sync*, or object-only
  *Repair LFS* authorize writes through the [web UI](../web-ui/) at
  `http://localhost:8080`, so you can check what would be published before
  it is. Review mode reads the repository list from Forgejo, hence the
  source settings; add `-p 8080:8080` to `docker run`.

  ```yaml
  source:
    base_url: https://git.example.org/  # your Forgejo
    token: forgejo-read-token
  sync:
    auto: false
  ui:
    enabled: true
  ```
{{< /callout >}}

### Start the service

{{< tabs >}}
  {{< tab name="Container" >}}
  ```sh
  mkdir -p state
  sudo chown 65532:65532 state # container runtime UID/GID
  docker run --rm -p 8081:8081 -p 9090:9090 \
    -v "$PWD/forgemirror.yaml:/etc/forgemirror/forgemirror.yaml:ro" \
    -v "$PWD/state:/var/lib/forgemirror" \
    -e FORGEMIRROR_STATE__DATABASE=/var/lib/forgemirror/state.db \
    {{< param "project.image" >}}:<version> \
    --config /etc/forgemirror/forgemirror.yaml
  ```
  {{< /tab >}}
  {{< tab name="Binary" >}}
  ```sh
  forgemirror --config forgemirror.yaml --log-format text
  ```
  {{< /tab >}}
{{< /tabs >}}

`curl localhost:9090/readyz` answers `ready`.

### Point Forgejo at it

In Forgejo, add a webhook of type *Forgejo*:
- **Target URL:** `http://<forgemirror-host>:8081/webhook`
- **Content type:** `application/json`
- **Secret:** the value of `webhook.secret`
- **Trigger:** push events

[Forgejo webhook](../forgejo-webhook/) explains system webhooks and Forgejo's
`ALLOWED_HOST_LIST`, which blocks private addresses by default.

### Push

Push to any repository. The log shows:

```text
level=INFO msg="plan: out of sync" repository=acme/app apply=true changes=1 targets=1 …
level=INFO msg="mirror started" repository=acme/app ref=refs/heads/main …
level=INFO msg="target mirrored" target=github duration=1.2s outcome=success …
level=INFO msg="refs pushed" target=github created=0 updated=1 forced=0 deleted=0 …
level=INFO msg="ref pushed" target=github ref=refs/heads/main change=update summary=0e67ebf..2eca933 …
level=INFO msg="mirror finished" repository=acme/app duration=1.9s …
```

If nothing happens, look at the webhook's *Recent deliveries* in Forgejo and
the [troubleshooting table](../operations/#troubleshooting).

{{% /steps %}}
