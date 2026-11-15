---
title: Helm
weight: 1
---

The chart builds on the
[bjw-s common library](https://bjw-s-labs.github.io/helm-charts/docs/common-library/)
and is published as a signed OCI artifact with every release. The chart
version always equals the forgemirror version.

Defaults:
- **Workload:** one replica with the `Recreate` strategy.
- **Hardening:** non-root, read-only root filesystem, all capabilities
  dropped, `RuntimeDefault` seccomp profile.
- **Probes:** `/healthz` for liveness and `/readyz` for readiness, on the
  ops port.
- **Ports:** one Service with `webhook` (8081), `ops` (9090: probes,
  metrics) and `web` (8080: UI, listening only with `config.ui.enabled`).
- **Ingresses:** `ingress.webhook` and `ingress.web`, both disabled.
- **Shutdown:** a grace period longer than the shutdown timeout.
- **Rollouts:** a pod restart whenever the config or credentials change.

{{% steps %}}

### Create the credentials

```sh
kubectl create namespace forgemirror
kubectl -n forgemirror create secret generic forgemirror \
  --from-literal=webhook-secret="$(openssl rand -hex 32)" \
  --from-literal=source-token=<forgejo-token> \
  --from-literal=github-token=<github-token>
```

Each key is mounted as a file under `/run/secrets/forgemirror/`. Instead of
`existingSecret`, you can also let the chart create the Secret from
`credentials.data`, for example together with helm-secrets or SOPS.

### Write values

```yaml {filename="my-values.yaml"}
config:
  source:
    base_url: https://git.example.org/
    token_file: /run/secrets/forgemirror/source-token
  filter:
    include_repos: "acme/.*"
    exclude_private: true
  targets:
    - name: github
      url: "https://github.com/acme/{{ .Name }}.git"
      token_file: /run/secrets/forgemirror/github-token

credentials:
  existingSecret: forgemirror
```

`config` is rendered verbatim into `forgemirror.yaml`; see
[Configuration](../../configuration/). The chart excludes it from bjw-s
template evaluation, so `{{ .Name }}` reaches forgemirror unchanged. Rendering
fails if `config.targets` is empty or no credentials are given, so a pod is
never deployed in a state where it would just crash-loop.

### Install

```sh
helm install forgemirror {{< param "project.chart" >}} \
  --version <version> --namespace forgemirror -f my-values.yaml
```

The output prints the webhook URL to configure in
[Forgejo](../../forgejo-webhook/).

{{% /steps %}}

## Ingresses

The chart has two ingresses, both disabled by default. Each takes the plain
bjw-s values `className`, `annotations`, `hosts` and `tls`; there are no
provider-specific settings, so any controller, proxy or SSO works through
annotations.

```yaml
ingress:
  # Only needed when Forgejo cannot reach the cluster-internal Service,
  # e.g. when it runs in another cluster. The webhook port serves nothing
  # but /webhook, and unsigned requests are rejected.
  webhook:
    enabled: true
    className: nginx
    hosts:
      - host: forgemirror-webhook.example.org
        paths:
          - path: /
            service: {identifier: forgemirror, port: webhook}
    tls:
      - hosts: [forgemirror-webhook.example.org]
        secretName: forgemirror-webhook-tls

  # The web UI has no authentication of its own: secure it here.
  web:
    enabled: true
    className: nginx
    annotations:
      nginx.ingress.kubernetes.io/auth-url: https://auth.example.org/oauth2/auth
      nginx.ingress.kubernetes.io/auth-signin: https://auth.example.org/oauth2/start?rd=$scheme://$host$escaped_request_uri
    hosts:
      - host: forgemirror.example.org
        paths:
          - path: /
            service: {identifier: forgemirror, port: web}

config:
  ui:
    enabled: true          # required by ingress.web
```

Rendering fails when `ingress.web` is enabled without `config.ui.enabled`.
To use Traefik's `IngressRoute` with a middleware (or any other resource you
manage yourself), leave `ingress.web` disabled: the UI then stays reachable
only inside the cluster, at service port `web`. The ops port has no ingress;
probes and metrics stay cluster-internal.

## Common additions

All bjw-s common library values work, for example:

```yaml
serviceMonitor:
  forgemirror:
    enabled: true          # requires the Prometheus Operator

controllers:
  forgemirror:
    pod:
      imagePullSecrets:
        - name: registry-credentials
```

## Verify the chart signature

```sh
cosign verify --key cosign.pub {{< param "project.chartRef" >}}:<version>
```

## Default values

{{< repo-file "chart-values.yaml" >}}
