# Web UI and port split — implementation plan

**Goal:** three listeners (web, webhook, ops), a read-only status UI, chart
ingresses for web and webhook.

**Spec:** `spec/2026-11-15-web-ui-design.md` (binding).

**Execution:** native, task by task; `mise run check` green before every
commit; one review of the whole branch at the end.

## Global constraints

- Defaults: `ui.listen_address` `:8080`, `server.listen_address` `:8081`,
  `server.ops_listen_address` `:9090`, `ui.enabled` false,
  `ui.log_entries` 1000.
- htmx 2.0.11, embedded; no CDN, no JS build, no inline script (CSP
  `default-src 'self'`).
- No secret value in any UI response; error text only from already-redacted
  sources.
- Commits: conventional + gitmoji; the port change is `feat!` with a
  `BREAKING CHANGE:` footer.

## Tasks

1. **Config** — `Server.OpsListenAddress`, `UI{Enabled, ListenAddress,
   LogEntries}`; defaults; validation (host:port, distinct addresses — the UI
   address only counts when enabled, `log_entries > 0`); flags
   `--ops-listen-address`, `--ui-listen-address`; JSON schema v1;
   `Config.Effective() map[string]any` (koanf names, secrets redacted,
   durations as strings) for the UI. Tests: defaults, each validation error,
   env/flag overrides, schema accepts/rejects, Effective hides secrets.
2. **Servers** — `server.Server` serves one named handler (timeouts, graceful
   shutdown, log `server=<name>`); `server.Health` owns readiness and builds
   the ops mux (`/healthz`, `/readyz`, `/metrics`); `server.WebhookHandler`
   builds the webhook mux. App starts webhook + ops (+ web) listeners; one
   failing listener stops all; readiness true once all serve, false when
   shutdown begins. Tests: routes per mux, bind error, shutdown, app e2e on
   the new ports.
3. **Status store** — dispatch/schedule observer methods gain the repository
   full name; `telemetry` ignores it; `internal/status`: `Store` implementing
   both observer interfaces plus `Queued/Running` from the dispatcher,
   `Snapshot()` for the UI; fan-out observer. Deletions remove the repository.
   Tests: concurrent updates (race), snapshot ordering, deletion.
4. **Log buffer** — `logging.Ring` (`slog.Handler` tee with fixed capacity,
   attributes resolved via `LogValue`); `logging.New` takes an optional ring.
   Tests: capacity/eviction, level filter, groups/attrs, secret stays redacted.
5. **UI** — `internal/ui`: `Handler(Options{Config, Store, Logs, Version,
   Started})`; page + fragments (`overview`, `repos`, `config`, `logs`);
   embedded templates, CSS, `htmx.min.js` (+ version/sha256 note, license);
   security headers; GET/HEAD only. Tests: every route 200 with expected
   content, 405 on POST, headers, no secret in any response, relative URLs.
6. **Wiring** — app builds store, ring and UI when `ui.enabled`.
   Test: app e2e GETs the UI and sees a mirrored repository.
7. **Deploy & docs** — chart (Service ports, probes, ServiceMonitor,
   `ingress.web`/`ingress.webhook`, fail when web ingress without UI),
   kustomize, NOTES, READMEs, docs pages (configuration, web UI page,
   upgrade note), example config.
