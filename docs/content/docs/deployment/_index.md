---
title: Deployment
weight: 4
---

ForgeMirror is a single-writer process with mandatory SQLite state. It needs
a persistent local filesystem for the database, separate from temporary clones,
and:
- **Disk:** a writable directory for temporary clones (`mirror.work_dir`,
  default: the OS temp directory).
- **Network:** outbound HTTPS to Forgejo and to the targets, and an inbound
  port reachable by Forgejo.

Run **exactly one instance.** Coalescing of concurrent pushes lives in memory,
so two instances could push the same repository at the same time.

{{< cards >}}
  {{< card link="helm" title="Helm" icon="cube" subtitle="Signed OCI chart on the bjw-s common library, preconfigured." >}}
  {{< card link="kubernetes" title="Kubernetes (kustomize)" icon="template" subtitle="Plain manifests for kubectl apply -k." >}}
  {{< card link="container" title="Container and systemd" icon="terminal" subtitle="docker, podman or a plain binary." >}}
{{< /cards >}}
