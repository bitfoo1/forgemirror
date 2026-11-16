---
title: Getting started
weight: 1
---

This guide runs forgemirror locally or in a container and mirrors a first
repository. For production, see [Deployment](../deployment/).

## Prerequisites

- **Forgejo:** a Forgejo instance where you are an administrator (for a system
  webhook) or a repository owner (for a repository webhook).
- **Target tokens:** a token with push access for each target forge, for
  example a GitHub fine-grained token with *Contents: read and write* on the
  destination repositories.
- **Source token:** a Forgejo token with read access, if private repositories
  should be mirrored.
- **Existing target repositories:** a plain target pushes to repositories
  that already exist. Only a [typed target](../configuration/#typed-targets)
  with `create: true` creates them.

{{% steps %}}

### Install

{{< tabs >}}
  {{< tab name="Container" >}}
  Multi-arch images (linux/amd64, linux/arm64) are published with every
  release. The image runs as a non-root user and already contains `git`.

  ```sh
  docker pull {{< param "project.image" >}}:<version>
  ```
  {{< /tab >}}
  {{< tab name="Binary" >}}
  Download the archive for your platform (linux or darwin, amd64 or arm64)
  from the [releases page]({{< param "project.releases" >}})
  and put `forgemirror` on your `PATH`. `git` must be installed.
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
# configuration is valid (1 targets)
```

All options, including filters and secrets read from files, are covered in
[Configuration](../configuration/).

### Start the service

{{< tabs >}}
  {{< tab name="Container" >}}
  ```sh
  docker run --rm -p 8081:8081 -p 9090:9090 \
    -v "$PWD/forgemirror.yaml:/etc/forgemirror/forgemirror.yaml:ro" \
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
