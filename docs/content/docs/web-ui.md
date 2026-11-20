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
- **Repositories:** a full-width **Current diff** per target and a separate
  **Last applied plan** with that sync's results. Failed, partial or interrupted
  applications are labelled **Last sync attempt**, not presented as completed
  changes. Diffs include repository creation/deletion, branches and tags
  (`+` new, `~` updated, `−` deleted), settings, labels, releases and container
  tags. Repositories that need attention come first.
- **Logs:** the most recent log records, filterable by level.
- **Configuration:** the effective configuration after defaults, file,
  environment and flags, with secrets redacted.

The theme follows your system's light or dark setting; the button in the
header switches between system, light and dark and is remembered per
browser. The colours are [Tokyo Night](https://github.com/folke/tokyonight.nvim)
Day and Night.

The page checks live data asynchronously every 5 seconds with embedded
[htmx](https://htmx.org); it does not reload the page. Incremental updates
preserve unchanged elements, expanded diffs, focus and scroll positions.
Updates to a section pause while you select text in it and catch up when the
selection clears. Nothing is loaded from the internet. Without JavaScript,
plain forms still work, but the page does not update automatically.

## Review and sync

Every run first computes a plan: what a sync would change on each target,
read without changing anything (`git ls-remote` and the forge APIs). The UI
shows that plan as the diff.

| Button          | Does                                                          |
|-----------------|---------------------------------------------------------------|
| **Refresh**     | Recomputes the diff of one repository. Changes nothing.       |
| **Sync**        | Recomputes the plan of one repository and applies it.         |
| **Repair LFS** | Reconciles source-history LFS objects on existing targets, even when refs match. Does not publish Git refs or unrelated changes; may transfer large files. |
| **Refresh all** | Recomputes the diff of every repository the source lists and the [filter](../configuration/#filter) admits. |
| **Sync all**    | Applies every repository whose diff shows changes, after a confirmation. Repositories never checked are not synced. |

Sync applies the state at the time you press it: if someone pushed after
the diff was computed, that push is synced too. The diff shows when it was
computed. After every sync, ForgeMirror performs a read-only comparison and
updates **Current diff** automatically, even if some operations failed.
The applied plan stays available separately. While verification is pending,
saved diffs are marked stale; a failed comparison says **Could not verify**
rather than assuming everything is in sync.

### Pause and resume targets

Use **Pause or resume a target** to hold an entire destination or one known
`owner/name` on that destination. Supply an optional reason and duration such
as `30m` or `2h`; empty duration holds indefinitely. Active pauses and their
reasons appear beside the controls and on affected targets.

Planning continues while paused. A pause acknowledges only after active
remote operations finish; subsequent Git, LFS, settings, release, package,
creation and deletion steps cannot start. It does not roll back completed
steps. Manual Sync and Repair LFS obey the same hold.

Resume removes only the selected scope and requests a fresh comparison and
sync for that destination, including in review mode. Resuming a repository
does not bypass a whole-target hold. An expiry removes the hold automatically
for subsequent runs; it does not create an independent scheduled run. Enable
scheduled sync if publication should catch up without another push.

Controls survive restarts in the mandatory SQLite database. A failed durable
write leaves the previous control state intact and reports an error.

### Review mode

With `sync.auto: false` nothing reaches a target until you press *Sync*,
*Sync all*, or explicitly request *Repair LFS* (objects only). Pushes, scheduled runs and deletion events only refresh the
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

Both review sections are retained across restarts in the mandatory database.
The default is `data/state.db`; deployments can override the path on their
[persistent volume](../configuration/#persistent-state):

```yaml
state:
  database: /var/lib/forgemirror/state.db
```

Use a persistent local volume. Source API credentials enable automatic
rediscovery; webhook-only setups wait for newly admitted webhooks instead.
On startup, saved diffs are marked stale and rechecked without applying writes;
uncompleted attempts become **interrupted**. Restored repositories only regain
action buttons after current source discovery and filters admit them. One latest
attempt and one comparison are retained per repository, including deletion
history, not unlimited history. Logs remain in memory; metrics stay in
[Prometheus](../operations/#metrics).

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
