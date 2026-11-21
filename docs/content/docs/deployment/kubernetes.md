---
title: Kubernetes (kustomize)
linkTitle: Kubernetes
weight: 2
---

Plain manifests in [`deploy/`]({{< param "project.repo" >}}/tree/main/deploy),
assembled with the kustomize built into `kubectl`. They produce the same
hardened setup as the [Helm chart](../helm/).

| File                  | Purpose                                                     |
|-----------------------|-------------------------------------------------------------|
| `namespace.yaml`      | `forgemirror` namespace, Pod Security Standard `restricted` enforced |
| `configmap.yaml`      | `forgemirror.yaml` without secrets                          |
| `secret.example.yaml` | Template for the credentials. Not applied by kustomize, so secrets stay out of git |
| `deployment.yaml`     | One replica, `Recreate`, non-root, read-only root filesystem, probes |
| `pvc.yaml` | Default 1 GiB ReadWriteOnce storage for the mandatory SQLite state |
| `service.yaml`        | ClusterIP service on port 80                                |

## Install

{{% steps %}}

### Configure

Edit `deploy/configmap.yaml` (targets, filter, timeouts). In
`kustomization.yaml`, pin the image tag or a digest instead of `latest`.
The base enables SQLite at `/var/lib/forgemirror/state.db` with a persistent
volume. Select local/block-backed storage in `deploy/pvc.yaml`, not NFS/SMB;
UID/GID 65532 must be able to write it. LFS temporary objects share `/work`
with Git clones, so size that separate scratch volume for your repositories.

### Create the Secret

```sh
kubectl create namespace forgemirror
kubectl -n forgemirror create secret generic forgemirror \
  --from-literal=webhook-secret="$(openssl rand -hex 32)" \
  --from-literal=source-token=<forgejo-token> \
  --from-literal=github-token=<github-token>
```

Choose the source and destination token scopes from
[Configuration → Token permissions](../../configuration/#token-permissions).
Optional notification URL/token files must also be keys in the mounted Secret.

### Apply

```sh
kubectl apply -k deploy/
kubectl -n forgemirror rollout status deployment/forgemirror
```

Webhook URL: `http://forgemirror.forgemirror.svc.cluster.local/webhook`

{{% /steps %}}

`mise run deploy:check` validates the rendered manifests against the
Kubernetes API schemas, and runs `forgemirror validate` on the embedded
configuration. CI runs the same check on every change.

## Default ConfigMap

{{< repo-file "configmap.yaml" >}}
