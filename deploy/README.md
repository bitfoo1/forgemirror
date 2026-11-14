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
| `service.yaml`        | ClusterIP service on port 80                                 |

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
kubectl -n forgemirror port-forward svc/forgemirror 8080:80 &
curl -fsS localhost:8080/readyz     # ready
curl -fsS localhost:8080/metrics | grep forge_mirror_
kubectl -n forgemirror logs deploy/forgemirror -f
```

Push to a repository that passes the filter, or use **Test delivery** on the
webhook. The log shows `mirror started`, one `target mirrored` per target and
`mirror finished`. Deliveries are counted in
`forge_mirror_webhook_requests_total{outcome="…"}`:

- `unauthorized`: the webhook secret does not match.
- `filtered`: the repository or branch is excluded by the filter rules.

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
  `http`, path `/metrics`.
