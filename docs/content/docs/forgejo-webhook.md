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
| Trigger on       | Push events                                              |
| Branch filter    | leave empty, and use forgemirror's `include_branches` instead |

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
| 202    | `accepted`                 | Mirror queued, or coalesced into a running one                 |
| 200    | `filtered`                 | Valid push, but a filter rule excludes it; the body names the rule |
| 200    | `ignored`                  | Not a push event (for example the test delivery of another event type) |
| 400    | `bad_request`              | Malformed JSON, required fields missing, or a clone URL outside `source.base_url` |
| 401    | `unauthorized`             | Missing or wrong signature, so the secrets differ              |
| 413    | `too_large`                | Body exceeds `webhook.max_body_bytes` (default 5 MiB)          |
| 415    | `unsupported_media_type`   | Content type is not `application/json`                         |

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
curl -i http://localhost:8080/webhook \
  -H 'Content-Type: application/json' -H 'X-Forgejo-Event: push' \
  -H "X-Forgejo-Signature: $sig" --data "$body"
```
