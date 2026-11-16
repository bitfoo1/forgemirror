---
title: Operations
weight: 5
---

## HTTP endpoints

| Listener (default)  | Endpoint        | Purpose                                                                 |
|---------------------|-----------------|-------------------------------------------------------------------------|
| webhook (`:8081`)   | `POST /webhook` | Forgejo push and repository events; `404` when no `webhook.secret` is set (schedule-only) |
| ops (`:9090`)       | `GET /healthz`  | Liveness: `200` while the process serves HTTP                            |
| ops (`:9090`)       | `GET /readyz`   | Readiness: `503` before serving and once shutdown begins, so load balancers stop sending webhooks |
| ops (`:9090`)       | `GET /metrics`  | Prometheus metrics                                                      |
| web (`:8080`)       | `GET /`         | [Web UI](../web-ui/), only with `ui.enabled: true`                       |

See [Listeners](../configuration/#listeners) for why there are three.

## Metrics

forgemirror records metrics with OpenTelemetry and exposes them in the
Prometheus format. Go runtime (`go_*`) and process (`process_*`) metrics are
included as well.

| Metric                                  | Type      | Labels             | Meaning                                         |
|-----------------------------------------|-----------|--------------------|-------------------------------------------------|
| `forgemirror_operations_total`          | counter   | `target`, `status` | Mirror operations per target; `status` is `success` or `failure` |
| `forgemirror_duration_seconds`          | histogram | `target`           | Duration of each push to a target; buckets from 0.5 s to 10 min |
| `forgemirror_webhook_requests_total`    | counter   | `outcome`          | Webhook requests by [outcome](../forgejo-webhook/#responses) |
| `forgemirror_active_operations`         | gauge     |                    | Mirror runs in progress                          |
| `forgemirror_sync_operations_total`     | counter   | `target`, `operation`, `status` | Forge API operations of typed targets; `operation` is `create`, `settings`, `labels`, `releases`, `packages` or `delete`, `status` is `success` or `failure`. Runs without changes for an operation are not counted |
| `forgemirror_scheduled_runs_total`      | counter   | `status`           | Scheduled sweeps; `failure` means the source repositories could not be listed |
| `target_info`                           | gauge     | `service_name`, `service_version` | Build information                 |

A failed clone counts as a failure for every target, because none of them
could be updated. A typed target whose repository could not be read or
created counts as a failed mirror operation as well.

Example alerts:

```yaml
groups:
  - name: forgemirror
    rules:
      - alert: ForgemirrorTargetFailing
        expr: increase(forgemirror_operations_total{status="failure"}[30m]) > 0
        labels: { severity: warning }
        annotations:
          summary: "Mirroring to {{ $labels.target }} fails"
      - alert: ForgemirrorSyncFailing
        expr: increase(forgemirror_sync_operations_total{status="failure"}[30m]) > 0
        labels: { severity: warning }
        annotations:
          summary: "{{ $labels.operation }} on {{ $labels.target }} fails"
      - alert: ForgemirrorScheduledRunFailing
        expr: increase(forgemirror_scheduled_runs_total{status="failure"}[2h]) > 0
        labels: { severity: warning }
        annotations:
          summary: "Scheduled sync cannot list Forgejo repositories"
      - alert: ForgemirrorWebhookUnauthorized
        expr: increase(forgemirror_webhook_requests_total{outcome="unauthorized"}[15m]) > 0
        labels: { severity: warning }
        annotations:
          summary: "Webhook signature mismatch; check webhook.secret"
```

## Logs

forgemirror writes structured logs to stderr. They are JSON by default; set
`log.format: text` for logfmt-style lines. Mirror events carry `repository`,
`ref`, `delivery`, and per target also `target`, `outcome` and `duration`.

| Message                                           | Level | Meaning                                             |
|---------------------------------------------------|-------|-----------------------------------------------------|
| `mirror started` / `mirror finished`              | info  | One run for one repository                          |
| `target mirrored`                                 | info  | Push to one target succeeded                        |
| `target failed`                                   | error | Push to one target failed; includes git's output    |
| `mirror failed`                                   | error | Clone failed; no target was updated                 |
| `run already in progress; queued one follow-up run`| info  | An event arrived during a run, so a follow-up run is coalesced |
| `shutting down; event dropped`                    | warn  | Push arrived after shutdown began                   |
| `shutting down; queued follow-up run dropped` | warn | A coalesced follow-up run was not started because of shutdown |
| `scheduled sync enabled`                          | info  | The schedule is active; carries `interval`          |
| `scheduled run dispatched`                        | info  | A sweep queued runs; carries `repositories` and `listed` |
| `scheduled run failed`                            | error | The source repositories could not be listed; retried on the next tick |
| `plan: in sync` / `plan: out of sync`             | info  | The plan of a run; carries `apply` (false: only planned), `changes`, `targets`, `source_deleted`. Each planned change is logged at debug level (`plan: ref create`, `plan: setting`, …) |
| `plan: target cannot be synced`                   | warn  | The plan of one target failed, e.g. its repository is missing and `create` is false |
| `refs already in sync; clone and push skipped`    | info  | No target needs a push                              |
| `refs pushed`                                     | info  | Per target: counts of `created`, `updated`, `forced`, `deleted` refs |
| `ref pushed`                                      | info  | One ref with `change` and git's `summary` (e.g. `0e67ebf..2eca933`); the first 20 per target at info, the rest at debug |
| `repository created` / `repository deleted`       | info  | A target repository was created or deleted          |
| `setting changed`                                 | info  | One setting on a target, with `field`, `old` and `new` |
| `label created` / `label updated` / `label deleted` | info | One label on a target, with `old` and `new` colour and description |
| `release created` / `release updated` / `release deleted` | info | One release on a target, with `tag`, the changed `fields` and the attachments added, replaced and removed |
| `package tags copied`                             | info  | Tags copied for one package, with `image`, `count` and the first 20 `tags` |
| `forge operation failed`                          | error | One create, settings, labels, releases, packages or delete step failed; carries `target` and `operation` (`forge operation succeeded` is logged at debug level) |
| `sync requested from the UI` / `refresh requested from the UI` | info | A button was pressed; carries `repository` or `repositories` |
| `deletion started` / `deletion finished`          | info  | A repository deletion event was handled; `deletion finished with errors` is logged at error level |

git output in logs and errors is redacted: tokens and authorization headers
show up as `[REDACTED]`.

## Graceful shutdown

On `SIGTERM` or `SIGINT`:
1. `/readyz` returns `503` and the listener stops accepting connections.
   The scheduler stops, so no new scheduled runs start.
2. Open webhook requests are answered.
3. Running mirrors finish. Queued follow-up runs and new pushes are dropped
   and logged; the next push after the restart mirrors the latest state.
4. When `server.shutdown_timeout` (60 s by default) runs out, git processes
   are killed and the process exits.

The phases share one budget. Keep the orchestrator's kill timeout above it:
`terminationGracePeriodSeconds` (Kubernetes), `stop_grace_period` (Compose),
or `TimeoutStopSec` (systemd).

## Troubleshooting

| Symptom                                    | Cause and fix                                                  |
|--------------------------------------------|----------------------------------------------------------------|
| Forgejo delivery fails with "host not allowed" | Allow forgemirror in `[webhook] ALLOWED_HOST_LIST`; see [Forgejo webhook](../forgejo-webhook/#allow-forgejo-to-reach-forgemirror) |
| `401` responses                            | `webhook.secret` and the secret in Forgejo differ               |
| `415` responses                            | Webhook content type is form-encoded; set `application/json`    |
| `200 not mirrored: …`                      | A filter rule excludes the push; the body names the rule        |
| `target failed` … `Repository not found`   | Target repository does not exist, or the URL template renders a different name |
| `target failed` … `403` / `Authentication failed` | Token lacks push permission or has expired; GitHub fine-grained tokens need *Contents: read and write* |
| `target failed` … `protected branch`       | Force pushes are blocked on the target; allow them for the mirror account, because mirroring rewrites history like the source |
| `mirror failed` on private repositories    | `source.token` is missing or lacks read access                  |
| `target … does not exist and create is false` | The target repository is missing; create it or set `create: true` on the typed target |
| `cannot create repository in namespace …`  | The namespace is neither an organisation or group nor the token's user |
| Typed targets fail while Forgejo is down   | Expected: forgemirror starts anyway, connects on first use and waits 10 s after a failed attempt |
| Private repository is not mirrored         | No matching `filter.allow_private` (required with typed targets or `sync.schedule`; otherwise only with `filter.exclude_private`), or a source token with public-only repository access |
| Pod evicted                                | Clones exceed the `/work` emptyDir `sizeLimit`; raise it        |
