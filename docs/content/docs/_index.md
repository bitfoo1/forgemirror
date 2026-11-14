---
title: Documentation
linkTitle: Docs
weight: 1
---

forgemirror is a small Go service. It receives Forgejo push webhooks and
mirrors the pushed repository to one or more public forges. Your Forgejo
instance can stay private: the only connection it needs is the outgoing
webhook to forgemirror.

## How it works

```mermaid
sequenceDiagram
    participant F as Forgejo
    participant M as forgemirror
    participant T as Targets (GitHub, Codeberg, …)
    F->>M: POST /webhook (push event, HMAC-signed)
    M->>M: verify signature, apply filter
    M-->>F: 202 Accepted
    M->>F: git clone --mirror
    par every target
        M->>T: git push --prune (branches + tags)
    end
```

1. A push to Forgejo sends a signed webhook to forgemirror.
2. forgemirror verifies the HMAC signature and checks the [filter
   rules](configuration/#filter). It answers immediately, and mirroring runs
   in the background.
3. It clones the repository once into a temporary directory and pushes all
   branches and tags to every target in parallel. Branches and tags that were
   deleted on Forgejo are deleted on the targets as well.
4. Pushes that arrive while a repository is being mirrored are coalesced into
   exactly one follow-up run. That run mirrors the latest state, so no push is
   lost and no work is repeated.

## Where to go next

{{< cards >}}
  {{< card link="getting-started" title="Getting started" icon="play" subtitle="Run forgemirror and mirror the first repository." >}}
  {{< card link="configuration" title="Configuration" icon="adjustments" subtitle="Every setting, environment variable and CLI flag." >}}
  {{< card link="forgejo-webhook" title="Forgejo webhook" icon="link" subtitle="Connect Forgejo and understand the responses." >}}
  {{< card link="deployment" title="Deployment" icon="server" subtitle="Kubernetes with kustomize or Helm, or a container." >}}
  {{< card link="operations" title="Operations" icon="chart-bar" subtitle="Metrics, logs, health checks, shutdown, troubleshooting." >}}
  {{< card link="security" title="Security" icon="shield-check" subtitle="How credentials are handled; verifying releases." >}}
  {{< card link="development" title="Development" icon="code" subtitle="Tooling, conventions, CI/CD and releases." >}}
{{< /cards >}}
