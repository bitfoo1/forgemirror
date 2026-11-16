# forgemirror Helm chart

Deploys [forgemirror](https://git.bitfoo.de/dvonessen/forgemirror). It
receives Forgejo push webhooks (or runs on a schedule) and mirrors each
repository to GitHub, Codeberg, GitLab and similar forges.

The chart is built on the
[bjw-s common library](https://bjw-s-labs.github.io/helm-charts/docs/common-library/)
5.3.0, and its defaults are tuned for forgemirror:
- **Workload:** a single replica with the `Recreate` strategy.
- **Hardening:** runs non-root with a read-only root filesystem and all
  capabilities dropped.
- **Probes:** `/healthz` for liveness, `/readyz` for readiness, on the ops
  port.
- **Ports:** one Service with `webhook` (8081), `ops` (9090: probes,
  metrics) and `web` (8080: UI, listening only with `config.ui.enabled`).
- **Ingresses:** `ingress.webhook` and `ingress.web`, both disabled.
- **Shutdown:** a grace period longer than the shutdown timeout, so running
  mirrors can finish.
- **Rollouts:** a pod restart whenever the config or credentials change.

The chart version always equals the forgemirror version.

## Install

```sh
helm install forgemirror oci://git.bitfoo.de/dvonessen/charts/forgemirror \
  --version <version> --namespace forgemirror --create-namespace \
  -f my-values.yaml
```

A minimal `my-values.yaml`:

```yaml
config:
  source:
    base_url: https://git.example.org/
    token_file: /run/secrets/forgemirror/source-token
  filter:
    include_repos: "acme/.*"
    exclude_private: true
  targets:
    - name: github
      type: github # API access: create repositories, sync settings and labels
      create: true
      url: "https://github.com/acme/{{ .Name }}.git"
      token_file: /run/secrets/forgemirror/github-token

credentials:
  existingSecret: forgemirror # keys: webhook-secret, source-token, github-token, ...
```

A `type` makes forgemirror use the forge's API, which needs
`source.base_url` and a source token. Without a `type` a target is a plain
git push destination and its repository must already exist. Token rights are
listed in the [security docs](https://forgemirror.bitfoo.de/docs/security/#token-rights).

Rendering fails if `config.targets` is empty or if no credentials are given,
so a pod is never deployed in a state where it would just crash-loop.

## Values

| Key                                   | Default         | Description                                                                                       |
| ------------------------------------- | --------------- | ------------------------------------------------------------------------------------------------- |
| `config`                              | see values.yaml | Rendered verbatim into `forgemirror.yaml`; reference: `forgemirror.example.yaml`                  |
| `config.targets`                      | `[]`            | Required. `url` is a Go template, which the chart leaves for forgemirror to render                |
| `credentials.existingSecret`          | `""`            | Secret mounted at `/run/secrets/forgemirror/`; each key becomes a file                            |
| `credentials.data`                    | `{}`            | Key/value pairs for a chart-managed Secret, used when `existingSecret` is empty                   |
| `controllers.forgemirror.containers.app.image.tag` | appVersion | Image tag                                                                             |
| `controllers.forgemirror.containers.app.resources` | 50m / 64Mi, 512Mi limit | Raise the limit for large repositories or a higher `config.mirror.max_concurrent` |
| `persistence.work.sizeLimit`          | `5Gi`           | emptyDir for temporary clones                                                                     |
| `config.ui.enabled`                   | `false`         | Serve the web UI (status, diffs, refresh and sync) on service port `web` (8080)                   |
| `config.sync.auto`                    | `true`          | `false` = review mode: changes wait for *Sync* in the web UI; requires `config.ui.enabled` and source API credentials |
| `ingress.web.enabled`                 | `false`         | Ingress to the web UI; requires `config.ui.enabled`. Secure it with `annotations`                 |
| `ingress.webhook.enabled`             | `false`         | Ingress to the webhook, for a Forgejo outside the cluster                                         |
| `ingress.<name>.className`, `.annotations`, `.hosts`, `.tls` | see values.yaml | Plain bjw-s ingress values, no provider-specific settings           |
| `serviceMonitor.forgemirror.enabled`  | `false`         | Prometheus Operator ServiceMonitor for `/metrics` (port `ops`)                                    |

Every other bjw-s common library value applies as well, for example
`route`, `networkpolicies`, `defaultPodOptions.nodeSelector` and
`controllers.forgemirror.pod.imagePullSecrets`.

**Template evaluation:** bjw-s normally evaluates every string in the values
as a Helm template. This chart excludes `config` from that evaluation, so
forgemirror's own `{{ .Name }}` placeholders reach the application unchanged.
To use Helm templating inside `config` anyway, remove the exclusion under
`global.templateEvaluation.excludePaths`.

## Verify the signature

The chart is signed with cosign, using the same key as the container image:

```sh
cosign verify --key cosign.pub git.bitfoo.de/dvonessen/charts/forgemirror:<version>
```

## Development

`mise run chart:check` checks the chart with `ci/test-values.yaml` in three
ways:
1. `helm lint --strict`.
2. kubeconform validation of the rendered manifests, including the
   ServiceMonitor CRD.
3. `forgemirror validate` against the rendered config.

It is part of `mise run check`, so the pre-push hook and CI run it too.
