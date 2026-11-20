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

### Forge status badges and grouped changes

Forge-name badges sit immediately after each repository name. GitHub, GitLab,
and Codeberg use their proper names and official icons throughout the UI.
Icons are served locally and work without internet access; Codeberg uses its
blue mark in light mode and white mark in dark mode. Custom target aliases
remain distinct, and configuration identifiers and form values are unchanged.
Codeberg is identified by its public host, not by treating every Forgejo
instance as Codeberg. Hover a badge for its status; the same information is
provided to assistive technology.
Green means the latest Git/metadata comparison matches, yellow means pending
changes, red means a comparison, push, or feature-operation error, and gray
means not checked, not verified, or awaiting fresh verification. Green does
not audit LFS object availability. Pause and approval holds are included in
the status explanation.

**Current diff** lists identical changes once, followed immediately by badges
for every affected forge. Different old/new values remain separate, even when
shortened commit IDs look alike. Its change count counts shared changes once;
the same change can be applied to multiple targets. Omitted changes remain
separate because their equality cannot be verified.

One **Sync** action applies the repository to all eligible configured targets;
there are no separate per-forge Sync buttons. Expand **Target details** for
publication explanations, approval controls, pauses, and previous operation
results. Historical applied plans keep their per-target detail.

### Action notifications

Refresh/Sync acknowledgements appear as a small drop-down notification at the
top center, not as a persistent message in the repository list. Individual
actions name the repository; **Refresh all** and **Sync all** describe the bulk
scope and queued count. If no work was queued, the notification says so rather
than claiming a sync started.

Successful acknowledgements disappear after about five seconds. Hovering over
or focusing the notification pauses its timer; **Dismiss notification** closes
it immediately. A newer acknowledgement replaces the previous one and resets
the timer. Errors remain until dismissed. Reduced-motion preferences disable
the entrance movement, and notifications never steal keyboard focus.

An acknowledgement confirms request acceptance, not background completion.
Spinners continue independently. Notifications are delivered outside the polled
repository fragment, so they still appear while text selection defers a live
update and are not replayed by polling. They are local UI feedback, separate
from configured outbound webhook notifications. Toasts require JavaScript;
native non-JavaScript form redirects keep their existing behavior.

### Refresh and sync progress

Refresh/Sync buttons, including **Refresh all** and **Sync all**, show a small
wheel while their HTTP request is being submitted. An acknowledgement only
means work was accepted, not that mirroring finished.

Each active repository then shows a spinning **Refreshing**, **Syncing** or
**Starting** badge. Starting covers accepted work before its running status is
available. A global indicator summarizes all active repositories, including
work initiated elsewhere; mixed work says **N repositories active**. The count
falls as individual jobs finish, and clears after the last job completes or
fails. Waiting for retry, pause or approval is not presented as active work.

Indicators reflect the latest live snapshot, normally refreshed every five
seconds. They may retain the last observed state while text selection defers
updates. Without JavaScript, form submissions still work, but reload the page
to observe later background completion. Reduced-motion preferences keep the
status text and a static indicator without rotation. Background polling never
activates the action-button wheels.

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

### First-publication approval

With [first-publication protection](../configuration/#first-publication-protection)
enabled for a scope, expand **Target details** to see **Awaiting first-publication
approval** and the destination. **Approve destination and sync** confirms
ongoing authorization, persists the grant, then syncs fresh source state.
It does not approve one exact reviewed snapshot. A source/destination remap
requires a matching new grant; a stale browser form gets a conflict and must
be refreshed. Existing targets are not automatically adopted. Pauses still
apply after approval.

### Review mode

With `sync.auto: false`, pushes, scheduled runs and deletion events only
refresh the diff. Publication requires an explicit UI authorization: *Sync*,
*Sync all*, *Resume*, *Approve destination and sync*, or *Repair LFS* (objects
only). Previously authorized explicit retries can continue, but still obey
pauses and destination approval. At startup every repository is planned once, so after a restart the
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

## Why was this not published?

Each destination has a **Publication status** explanation, separate from the
Git/metadata comparison and historical attempt results, under **Target details**.
Held destinations include their hold in the forge badge's hover explanation,
and their current diff opens by default. Each target's details show:

- A primary status and **What happens next**: automatic retry eligibility,
  manual Sync, approval, resume, refresh or storage repair.
- **All publication reasons**, so a pause does not hide a missing approval,
  review requirement or failed comparison.
- Current comparison time, last successful retained target run, and the last
  LFS step's historical result when available.

A missing success timestamp means no success is retained within the configured
history window, not that the destination was never published. A successful
no-op run can provide evidence too. Partial failures are evaluated per target;
interrupted runs are not invented as successes.

**Git and metadata match** does not certify LFS object availability. A failed
LFS step remains visible even after matching refs; **Repair LFS** reconciles
objects. A successful past LFS step is historical evidence, not continuous
health verification. Historical push failures stay in results/history and do
not override a fresh verified comparison by themselves. A comparison with
warnings is identified separately: skipped artifact checks or normalized
provider fields are not silently presented as a complete feature match.

Reasons are derived from structured state, not parsed error text. HTML
`data-reason` attributes expose codes including `publication_paused`,
`approval_required`, `state_unavailable`, `not_admitted`, `target_not_configured`,
`retry_scheduled`, `retry_exhausted`, `retry_review_hold`, `comparison_failed`,
`verification_pending`, `comparison_warning`, `review_required`, `lfs_transfer_failed` and
`lfs_not_verified`.

Automatic-origin retries are visibly held in review mode. Explicit operator
retry intent remains eligible, but never bypasses pauses or approval. A
read-only Refresh finding changes does not itself queue publication; the
explanation says to Sync or wait for the next enabled trigger rather than
promising an immediate automatic push. Removed destinations are identified as
retained history, not silently treated as configured mirrors.

## Sync history timeline

Each repository's **History** link opens a read-only timeline of sync and LFS
repair attempts, newest first, with 20 attempts per page. It shows the trigger,
stable repository-local attempt number, duration, outcome and retry parent when
known. Expand an attempt to see intended changes alongside the actual target
operation results, including partial failures and LFS. Destination links use
the configured Git URL; no forge-specific commit URL is guessed for custom
servers.

History is durable, not a work queue. Read-only refreshes are not recorded as
applications. Interrupted work is labelled **interrupted**, never invented as
success. A retry parent may be outside the retention window. Existing latest
attempts are adopted when older state is loaded.

Defaults retain the most recent 50 attempts per repository, with a 30-day age
limit. Configure `state.history_limit` (1–1000) and positive
`state.history_retention` (for example `720h`). Retention is applied when state
is written or loaded; the page also hides expired entries. Idle on-disk records
are not continuously erased. Logs remain separate and in memory.

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
action buttons after current source discovery and filters admit them. The latest attempt, current comparison and bounded timeline are retained per
repository, including deletion history. Logs remain in memory; metrics stay in
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
