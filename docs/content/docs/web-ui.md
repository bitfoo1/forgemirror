---
title: Web UI
weight: 6
---

A read-only status page on its own port. It shows:
- **Overview:** version, uptime, targets, whether push webhooks are on, the
  schedule with its last sweep, and how many repositories are being
  mirrored right now.
- **Repositories:** every repository mirrored since the start, with the
  last push per target (time, duration, error) and the last result of each
  forge operation (create, settings, labels, delete).
- **Logs:** the most recent log records, filterable by level.
- **Configuration:** the effective configuration after defaults, file,
  environment and flags, with secrets redacted.

The page refreshes its sections every 5 seconds with
[htmx](https://htmx.org), which is embedded in the binary; nothing is loaded
from the internet. Without JavaScript the page still works, it just does not
refresh by itself.

## Enable it

```yaml
ui:
  enabled: true           # default: false, and then the port is not opened
  listen_address: ":8080" # default
  log_entries: 1000       # log records kept in memory
```

State and logs live in memory only: after a restart the page starts empty
and fills with the next runs. The repository's metrics stay in
[Prometheus](../operations/#metrics) for history.

## Secure it

{{< callout type="warning" >}}
The UI has no login of its own. Anyone who can reach its port sees your
repository names, error messages and configuration (without secrets).
{{< /callout >}}

forgemirror deliberately leaves authentication to the infrastructure you
already run: a reverse proxy with basic auth, an SSO proxy such as
oauth2-proxy or Authelia, or your ingress controller's auth middleware. The
UI runs on its own listener, so the whole port can be protected as one unit;
no path rules are needed, and the webhook and ops ports are unaffected.

The UI never changes anything: every request other than `GET` and `HEAD` is
rejected. Without a login of its own, any button would be open to
cross-site request forgery. Responses carry a strict
Content-Security-Policy, forbid framing and are never cached.

All links are relative, so a proxy may serve the UI below a path prefix,
e.g. `https://tools.example.org/forgemirror/` (note the trailing slash).

### Kubernetes

With the [Helm chart](../deployment/helm/#ingresses), enable
`config.ui.enabled` and either:
- `ingress.web` with your controller's auth annotations, or
- no ingress at all, and expose service port `web` with your own resources
  (e.g. a Traefik `IngressRoute` with a forward-auth middleware). Until you
  do, the UI is only reachable inside the cluster, e.g. with
  `kubectl port-forward svc/forgemirror 8080:8080`.

With the plain manifests in `deploy/`, the Service already has the `web`
port; add your own ingress for it.
