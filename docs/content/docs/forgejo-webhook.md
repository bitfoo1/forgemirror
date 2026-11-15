---
title: Forgejo webhook
weight: 3
---

## System or repository webhook

| Kind                   | Where                                      | Fires for                         |
|------------------------|--------------------------------------------|-----------------------------------|
| **System webhook**     | Site administration → Webhooks → System webhooks | every repository on the instance |
| Repository webhook     | Repository → Settings → Webhooks           | that repository only              |
| Organization webhook   | Organization → Settings → Webhooks         | all repositories of the organization |

A system webhook combined with [filter rules](../configuration/#filter)
covers the whole instance in one place. A filter on `require_topic` lets
repository owners opt in by adding a topic.

## Settings

| Field            | Value                                                    |
|------------------|----------------------------------------------------------|
| Type             | Forgejo                                                  |
| Target URL       | `http://<forgemirror>/webhook`                           |
| HTTP method      | `POST`                                                   |
| POST content type| `application/json` (other types are rejected with 415)   |
| Secret           | the value of `webhook.secret`                            |
| Trigger on       | Push events; add Repository events to propagate deletions |
| Branch filter    | leave empty, and use forgemirror's `include_branches` instead |

## Repository events

Tick *Repository* events as well when a target sets
[`delete: true`](../configuration/#typed-targets). forgemirror reacts to the
action `deleted` only: it passes the repository through the
[filter](../configuration/#filter) and then deletes the repository on every
target with `delete: true`. A target repository that no longer exists counts
as success. The action `created` is ignored with `200`; the repository is
created on the first push or scheduled run instead. Other event types are
ignored with `200` as well.

## Schedule-only needs no webhook

With `sync.on_push: false` and a `sync.schedule`, forgemirror pulls the
repository list from Forgejo and no webhook is required. Without a
`webhook.secret` the `/webhook` endpoint answers `404`. A secret is only
needed if a target sets `delete`, because deletions arrive as webhook events;
scheduled runs never delete. See [Sync](../configuration/#sync).

## Allow Forgejo to reach forgemirror

Forgejo refuses to deliver webhooks to private addresses by default
(`ALLOWED_HOST_LIST = external`). If forgemirror runs in the same network or
cluster, allow it in Forgejo's `app.ini` and restart Forgejo:

```ini
[webhook]
; keep "external" for other webhooks; add only forgemirror's host
ALLOWED_HOST_LIST = external,forgemirror.forgemirror.svc.cluster.local
```

Blocked deliveries appear in the webhook's *Recent deliveries* with an error
that mentions the host not being allowed.

## Responses

forgemirror answers right away, and mirroring happens in the background. A
`202` only means "queued"; the logs and
[metrics](../operations/#metrics) show the outcome.

| Status | Outcome (`outcome` label)  | Meaning                                                        |
|--------|----------------------------|----------------------------------------------------------------|
| 202    | `accepted`                 | Mirror queued, or coalesced into a running one (`mirror queued`); or a repository deletion queued (`delete queued`) |
| 200    | `filtered`                 | Valid push or deletion, but a filter rule excludes it; the body names the rule |
| 200    | `ignored`                  | Not a push or repository event, a repository event other than `deleted`, or a push while `sync.on_push` is `false` (`push events disabled (sync.on_push is false)`) |
| 400    | `bad_request`              | Malformed JSON, required fields missing, a clone URL outside `source.base_url`, or a repository event whose `full_name` is not `owner.login/name` |
| 401    | `unauthorized`             | Missing or wrong signature, so the secrets differ              |
| 413    | `too_large`                | Body exceeds `webhook.max_body_bytes` (default 5 MiB)          |
| 415    | `unsupported_media_type`   | Content type is not `application/json`                         |
| 404    |                            | No `webhook.secret` is configured, so the endpoint does not exist (not counted in the metric) |

## Signature

Forgejo signs the raw request body with HMAC-SHA256 and sends the hex digest
in `X-Forgejo-Signature`. forgemirror compares the digests in constant time.
It also accepts the `X-Gitea-*` headers, which Forgejo sends alongside its own.
`X-Forgejo-Delivery` is logged as `delivery`, so a log line can be matched to
the delivery shown in Forgejo.

To send a signed test request by hand:

```sh
body='{"ref":"refs/heads/main","repository":{"name":"app","full_name":"acme/app","owner":{"login":"acme"},"clone_url":"https://forgejo.example/acme/app.git"}}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')
curl -i http://localhost:8081/webhook \
  -H 'Content-Type: application/json' -H 'X-Forgejo-Event: push' \
  -H "X-Forgejo-Signature: $sig" --data "$body"
```
