# Web UI and port split — design

Status: approved in conversation 2026-11-15; this document records it.

## Intent

Operators want to see what forgemirror is doing without reading pod logs:
the effective configuration, the state of every repository and target, and
recent log lines. forgemirror stays free of user management. Access control
is the operator's choice (reverse proxy, SSO such as oauth2-proxy or
Authelia, Traefik middleware), so the UI must be safe to put behind any of
them and must not be reachable by accident.

forgemirror may run in a different cluster than Forgejo, so the webhook must
be exposable through an ingress too, without exposing anything else.

## Goals

- Read-only status page: config, per-repository/per-target state, scheduler
  state, recent logs.
- UI can be switched off; off is the default and then its port is not opened.
- Three listeners with one audience each:

  | Port    | Config key                  | Default | Serves                                    |
  |---------|-----------------------------|---------|-------------------------------------------|
  | web     | `ui.listen_address`         | `:8080` | the UI, only when `ui.enabled: true`      |
  | webhook | `server.listen_address`     | `:8081` | `POST /webhook`                           |
  | ops     | `server.ops_listen_address` | `:9090` | `GET /healthz`, `/readyz`, `/metrics`     |

- Helm chart: one Service with ports `web`, `webhook`, `ops`; two ingresses
  `web` and `webhook`, both disabled by default, each with its own
  `className`, `annotations`, `hosts`, `tls`. No provider-specific settings.

## Non-goals

- Authentication, authorization, user accounts in forgemirror.
- Actions from the UI (sync now, retry). Without own authentication every
  state-changing request is a CSRF target; a proxy does not prevent that.
  Possible later behind a CSRF token, not now. *Superseded by
  `2026-11-16-review-mode-design.md`: refresh and sync actions behind Go's
  cross-origin protection.*
- Persistence: state and logs live in memory and are lost on restart.
- Live log streaming (SSE/WebSocket). Polling is enough for a status page.
- An ingress for the ops port.

## Anti-goals

- Any secret (webhook secret, tokens, credentials in URLs) reaching the page.
- The UI or ops endpoints being exposed by enabling the webhook ingress.
- A JavaScript build step, npm, or assets loaded from a CDN at runtime.

## Approaches considered

1. **Argo CD style: UI, webhook and health on one port, metrics apart.**
   Fewer ports, one host. Argo CD can do this because it authenticates
   every UI/API request itself. forgemirror does not, so the proxy would
   have to exempt `/webhook` (and probes) from SSO by path; a wrong path rule
   either breaks webhooks or opens the UI, and nothing in forgemirror
   notices. Rejected.
2. **Two ports: web, and webhook + health + metrics.** The webhook ingress
   must then route only `/webhook` to keep metrics private. Workable, one
   path rule more fragile. Rejected in favour of 3.
3. **Three ports, one audience each (chosen).** Each port is secured as a
   whole: SSO annotations on the web ingress, none (or an IP allowlist) on
   the webhook ingress, ops reachable only in-cluster and restrictable per
   port with a NetworkPolicy. Same idea as Flux notification-controller
   (webhook receiver 9292, metrics 8080, health 9440). Cost: two more
   ports.

Frontend: server-rendered `html/template` with htmx for partial refreshes
(chosen) over a full-page `<meta refresh>`, which would reload and lose
scroll position every few seconds. htmx costs one embedded file and a few
fragment routes.

## Decisions

### Configuration (breaking)

```yaml
server:
  listen_address: ":8081"      # was :8080; webhook only
  ops_listen_address: ":9090"  # new; healthz, readyz, metrics
ui:
  enabled: false
  listen_address: ":8080"
  log_entries: 1000            # size of the in-memory log buffer
```

- The three addresses must be distinct (validation error otherwise).
- `--listen-address` keeps overriding `server.listen_address`; new flags
  `--ops-listen-address` and `--ui-listen-address`; env vars follow the
  existing `FORGEMIRROR_<SECTION>__<KEY>` scheme.
- JSON schema v1 gains the keys; the upgrade note in the docs lists the
  port change.
- Without a webhook secret the webhook listener still starts (404 on
  `/webhook`), so the port layout does not depend on configuration.

### Versioning

`cliff.toml`: `breaking_always_bump_major = false`, so a breaking change
below 1.0 bumps the minor version. This change ships as v0.3.0. Separate
commit before the feature.

### Status data

- A status store records, per repository and target: last run start, finish,
  duration, outcome, error text, and whether a run is in progress or a
  follow-up run is queued; plus forge sync operations per target
  (create/settings/labels/delete with outcome), and for the scheduler the
  interval, last sweep time, repository count and error.
- The store receives the same events as the metrics. The dispatcher and
  scheduler observer interfaces gain the repository (full name) where it is
  missing today; telemetry ignores it (no new metric labels — cardinality).
  A fan-out observer feeds both telemetry and the store.
- Error text shown is the same text that is logged. It is already redacted
  at its source (`mirror.redactURL`, `redact`); the UI adds no other source
  of error text.
- Bounded memory: one entry per repository×target; repositories deleted on
  the source are removed from the store.

### Logs

- A `slog.Handler` wrapper tees every record that passes the configured
  level into a fixed-size ring buffer (`ui.log_entries`), besides the normal
  output. Attributes are resolved through `LogValue`, so `config.Secret`
  stays redacted exactly as in the normal logs.
- Only installed when the UI is enabled.

### Web UI

- `internal/ui`: handlers, `html/template` templates and static files via
  `embed`. Sections: overview (version, uptime, targets, schedule),
  repositories × targets table, configuration, logs (newest first, level
  filter).
- htmx 2.0.11 (0BSD), vendored and embedded, served from the binary; its
  version and SHA-256 are recorded next to the file. Sections refresh via
  `hx-get` + `hx-trigger="every 5s"` against fragment routes. The page is
  usable without JavaScript (manual reload).
- Configuration view renders the effective config; `config.Secret` already
  marshals as redacted. A test asserts that no configured secret value
  appears in any UI response.
- Only `GET`/`HEAD`; everything else 405.
- Response headers: strict CSP (`default-src 'self'`, no inline script),
  `frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `Cache-Control: no-store`.
- Works under a path prefix set by a proxy only if all links are relative;
  links and htmx URLs are relative so no prefix option is needed.

### Servers and shutdown

- `internal/server` serves the three handlers on three listeners with the
  same timeouts and graceful shutdown. `/readyz` turns 503 when shutdown
  begins, as today; it now lives on the ops listener.
- A failing listener (bind error) fails startup.

### Helm chart and kustomize

- Service ports `web` 8080, `webhook` 8081, `ops` 9090. Probes and the
  ServiceMonitor use `ops`.
- `ingress.web` → service port `web`, path `/`; `ingress.webhook` → service
  port `webhook`, path `/`. Both `enabled: false`; `className`,
  `annotations`, `hosts`, `tls` are plain bjw-s ingress values.
- The chart fails rendering when `ingress.web.enabled` is true but
  `config.ui.enabled` is not, instead of creating an ingress to a closed port.
- Disabling the web ingress leaves the UI reachable only in-cluster, e.g. for
  Traefik IngressRoute with middleware managed outside the chart.
- `deploy/` (kustomize) gets the same three ports; NOTES.txt, chart README,
  README, getting-started, container, configuration and webhook docs and
  `forgemirror.example.yaml` follow the new ports.

## Left to the builder

- Exact page layout and CSS; refresh interval constant.
- Store data structures; how "next sweep" is displayed (approximation from
  interval and last sweep is fine).
- Test split between store, handler and app wiring tests.
