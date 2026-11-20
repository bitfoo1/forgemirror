# Deploying forgemirror on Kubernetes

Plain manifests, assembled with kustomize (built into `kubectl`). For Helm,
use the chart in [`charts/forgemirror`](../charts/forgemirror/README.md)
instead. Both deploy the same hardened setup.

| File                  | Purpose                                                      |
| --------------------- | ------------------------------------------------------------ |
| `namespace.yaml`      | `forgemirror` namespace, Pod Security Standard `restricted`  |
| `configmap.yaml`      | `forgemirror.yaml` without secrets                           |
| `secret.example.yaml` | Template for the credentials; **not** applied by kustomize    |
| `deployment.yaml`     | One replica, non-root, read-only root filesystem, probes      |
| `service.yaml`        | ClusterIP service: `webhook` 80, `ops` 9090, `web` 8080      |

## 1. Configure

Edit `configmap.yaml`: targets, filter rules, timeouts. Every target needs a
key in the Secret that its `token_file` points to. The full reference is
[`forgemirror.example.yaml`](../forgemirror.example.yaml).

Pin the image in `kustomization.yaml` (`newTag`, or a `digest`) to a released
version instead of `latest`.

## 2. Create the Secret

```sh
kubectl create namespace forgemirror
kubectl -n forgemirror create secret generic forgemirror \
  --from-literal=webhook-secret="$(openssl rand -hex 32)" \
  --from-literal=source-token=<forgejo-token> \
  --from-literal=github-token=<github-token>
```

- `webhook-secret`: shared HMAC secret; you need the same value in step 4.
- `source-token`: Forgejo token with read access to the repositories (needed
  for private ones).
- One `<target>-token` per target with push access to the destination.

The registry `git.bitfoo.de` needs credentials if the package is private: add
an `imagePullSecrets` entry to the Deployment.

## 3. Apply

```sh
kubectl apply -k deploy/
kubectl -n forgemirror rollout status deployment/forgemirror
```

Validate changes before applying with `mise run deploy:check`. It renders the
kustomization and checks it against the Kubernetes API schemas. The same check
runs in `mise run check`, in the pre-push hook and in CI.

## 4. Configure the Forgejo webhook

As a Forgejo administrator, open **Site administration → Webhooks** and add a
**system webhook** of type *Forgejo*. A system webhook fires for every
repository on the instance, and the `filter` section decides which ones get
mirrored.

- **Target URL**: `http://forgemirror.forgemirror.svc.cluster.local/webhook`
  if Forgejo runs in the same cluster, otherwise the URL of your ingress.
- **HTTP method**: `POST`. **Content type**: `application/json`. Other
  content types are rejected with 415.
- **Secret**: the `webhook-secret` value from step 2.
- **Trigger on**: *Push events*.

Forgejo refuses to deliver webhooks to private addresses by default
(`[webhook] ALLOWED_HOST_LIST = external`). Cluster-internal service IPs are
private, so allow them in Forgejo's `app.ini`, either broadly or just for this
host:

```ini
[webhook]
ALLOWED_HOST_LIST = external,forgemirror.forgemirror.svc.cluster.local
```

## 5. Verify

```sh
kubectl -n forgemirror port-forward svc/forgemirror 9090:9090 &
curl -fsS localhost:9090/readyz     # ready
curl -fsS localhost:9090/metrics | grep forgemirror_
kubectl -n forgemirror logs deploy/forgemirror -f
```

Push to a repository that passes the filter, or use **Test delivery** on the
webhook. The log shows `mirror started`, one `target mirrored` per target and
`mirror finished`. Deliveries are counted in
`forgemirror_webhook_requests_total{outcome="…"}`:

- `unauthorized`: the webhook secret does not match.
- `filtered`: the repository or branch is excluded by the filter rules.

## Persistent review history

To retain the last sync attempt and current diff through pod replacement, use
`kubectl apply -k examples/deploy-persistent/` instead of the base. This overlay enables
`state.database` through an environment override and mounts a separate 1 GiB
ReadWriteOnce PVC at `/var/lib/forgemirror`. It inherits the source API credentials
from the base configuration and UID/GID/fsGroup 65532 from the Deployment.

Select a local/block-backed StorageClass in `examples/deploy-persistent/pvc.yaml` if your default
is network-backed: SQLite on NFS/SMB is unsupported. Keep one replica with
`Recreate`, ensure the volume is writable by UID 65532, and back it up separately
from disposable `/work` clones. See the
[operations guide](https://forgemirror.bitfoo.de/docs/operations/#persistent-state-and-backups).

## Operational notes

- **Single replica, `Recreate` strategy.** Coalescing of concurrent pushes is
  in-memory. Two replicas could push the same repository at the same time.
- **Graceful shutdown.** On SIGTERM the pod reports not-ready and drains
  requests and running mirrors for up to `server.shutdown_timeout` (60 s).
  `terminationGracePeriodSeconds` (75 s) must stay above it.
- **Disk.** Clones live in the `/work` emptyDir (limit 5 GiB). Raise
  `sizeLimit` and the memory limit for large repositories or a higher
  `mirror.max_concurrent`.
- **Metrics.** The pod carries the common `prometheus.io/*` scrape
  annotations. With the Prometheus Operator, add a `PodMonitor` for port
  `ops`, path `/metrics`.
- **Ports.** `webhook` (Service port 80, container 8081) is for Forgejo;
  expose it with an ingress only if Forgejo runs outside the cluster.
  `ops` (9090) serves probes and metrics; keep it cluster-internal. `web`
  (8080) serves the UI once `ui.enabled` is set; it has no authentication,
  so put a proxy or SSO in front of it.
