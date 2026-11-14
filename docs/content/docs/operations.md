---
title: Operations
weight: 5
---

## HTTP endpoints

| Endpoint        | Purpose                                                                 |
|-----------------|-------------------------------------------------------------------------|
| `POST /webhook` | Forgejo push events                                                     |
| `GET /healthz`  | Liveness: `200` while the process serves HTTP                            |
| `GET /readyz`   | Readiness: `503` before serving and once shutdown begins, so load balancers stop sending webhooks |
| `GET /metrics`  | Prometheus metrics                                                      |

## Metrics

forgemirror records metrics with OpenTelemetry and exposes them in the
Prometheus format. Go runtime (`go_*`) and process (`process_*`) metrics are
included as well.

| Metric                                  | Type      | Labels             | Meaning                                         |
|-----------------------------------------|-----------|--------------------|-------------------------------------------------|
| `forge_mirror_operations_total`         | counter   | `target`, `status` | Mirror operations per target; `status` is `success` or `failure` |
| `forge_mirror_duration_seconds`         | histogram | `target`           | Duration of each push to a target; buckets from 0.5 s to 10 min |
| `forge_mirror_webhook_requests_total`   | counter   | `outcome`          | Webhook requests by [outcome](../forgejo-webhook/#responses) |
| `forge_mirror_active_operations`        | gauge     |                    | Mirror runs in progress                          |
| `target_info`                           | gauge     | `service_name`, `service_version` | Build information                 |

A failed clone counts as a failure for every target, because none of them
could be updated.

Example alerts:

```yaml
groups:
  - name: forgemirror
    rules:
      - alert: ForgemirrorTargetFailing
        expr: increase(forge_mirror_operations_total{status="failure"}[30m]) > 0
        labels: { severity: warning }
        annotations:
          summary: "Mirroring to {{ $labels.target }} fails"
      - alert: ForgemirrorWebhookUnauthorized
        expr: increase(forge_mirror_webhook_requests_total{outcome="unauthorized"}[15m]) > 0
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
| `mirror already running; queued one follow-up run`| info  | Push arrived during a run, so a follow-up run is coalesced |
| `shutting down; event dropped`                    | warn  | Push arrived after shutdown began                   |
| `shutting down; queued follow-up run dropped` | warn | A coalesced follow-up run was not started because of shutdown |

git output in logs and errors is redacted: tokens and authorization headers
show up as `[REDACTED]`.

## Graceful shutdown

On `SIGTERM` or `SIGINT`:
1. `/readyz` returns `503` and the listener stops accepting connections.
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
| Pod evicted                                | Clones exceed the `/work` emptyDir `sizeLimit`; raise it        |
