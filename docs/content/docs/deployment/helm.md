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
- **Probes:** `/healthz` for liveness and `/readyz` for readiness.
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
    token_file: /run/secrets/forgemirror/source-token
  filter:
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

## Common additions

All bjw-s common library values work, for example:

```yaml
serviceMonitor:
  forgemirror:
    enabled: true          # requires the Prometheus Operator

ingress:
  forgemirror:             # only if Forgejo is outside the cluster
    className: nginx
    hosts:
      - host: forgemirror.example.org
        paths:
          - path: /webhook
            service:
              identifier: forgemirror
              port: http

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
