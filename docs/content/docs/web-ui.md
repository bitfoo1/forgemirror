---
title: Web UI
weight: 6
---

A status page on its own port, where you can also review and start syncs.

{{< screenshot name="web-ui" alt="The ForgeMirror web UI: overview tiles with the configured target types, and a mirrored repository with the last result of every sync step on its Forgejo target." >}}

It shows:
- **Overview:** version, uptime, targets, whether push webhooks are on, the
  schedule with its last sweep, and how many repositories are being
  mirrored right now. The header says whether ForgeMirror syncs
  automatically (*Auto sync*) or waits for you (*Review mode*).
- **Repositories:** every repository planned or mirrored since the start,
  with its state (*out of sync*, *failed*, *synced*, *in sync*, *not checked
  yet*) and the diff of its newest plan per target: repository creation or
  deletion, branches and tags (`+` new, `~` updated, `−` deleted), settings
  (old → new), labels, releases (with their attachment changes) and the
  container tags to copy per package. Below each diff: the last push and the last result
  of each forge operation. Repositories that need attention come first.
- **Logs:** the most recent log records, filterable by level.
- **Configuration:** the effective configuration after defaults, file,
  environment and flags, with secrets redacted.

The theme follows your system's light or dark setting; the button in the
header switches between system, light and dark and is remembered per
browser. The colours are [Tokyo Night](https://github.com/folke/tokyonight.nvim)
Day and Night.

The page refreshes its sections every 5 seconds with
[htmx](https://htmx.org), which is embedded in the binary; nothing is loaded
from the internet. Without JavaScript the page still works: it does not
refresh by itself, and the buttons reload the page.

## Review and sync

Every run first computes a plan: what a sync would change on each target,
read without changing anything (`git ls-remote` and the forge APIs). The UI
shows that plan as the diff.

| Button          | Does                                                          |
|-----------------|---------------------------------------------------------------|
| **Refresh**     | Recomputes the diff of one repository. Changes nothing.       |
| **Sync**        | Recomputes the plan of one repository and applies it.         |
| **Refresh all** | Recomputes the diff of every repository the source lists and the [filter](../configuration/#filter) admits. |
| **Sync all**    | Applies every repository whose diff shows changes, after a confirmation. Repositories never checked are not synced. |

Sync applies the state at the time you press it: if someone pushed after
the diff was computed, that push is synced too. The diff shows when it was
computed.

### Review mode

With `sync.auto: false` nothing reaches a target until you press *Sync* or
*Sync all*. Pushes, scheduled runs and deletion events only refresh the
diff. At startup every repository is planned once, so after a restart the
page shows what is pending. Use it to check the first run of a new
deployment before dozens of repositories are created on a public forge; see
[Sync](../configuration/#sync).

```yaml
sync:
  auto: false
ui:
  enabled: true   # required in review mode
```

In auto mode (the default) the buttons work too: *Sync* is "sync now".

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

ForgeMirror deliberately leaves authentication to the infrastructure you
already run: a reverse proxy with basic auth, an SSO proxy such as
oauth2-proxy or Authelia, or your ingress controller's auth middleware. The
UI runs on its own listener, so the whole port can be protected as one unit;
no path rules are needed, and the webhook and ops ports are unaffected.

Only the buttons change anything; they send `POST` requests, everything
else is `GET` or `HEAD`. Without a login of its own, a button would be open
to cross-site request forgery: another website could make your browser
press it. ForgeMirror rejects such requests with Go's cross-origin
protection, which checks the browser's `Sec-Fetch-Site` header and, for
older browsers, compares `Origin` with `Host`. Your proxy must therefore
pass `Host` and `Origin` through unchanged. Requests without either header
(e.g. `curl`) are not browser requests and are let through; your proxy's
authentication covers them. Every action is logged (`sync requested from
the UI`) with the repository; who pressed it is known to your proxy, not to
ForgeMirror.

Responses carry a strict Content-Security-Policy, forbid framing and are
never cached.

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
