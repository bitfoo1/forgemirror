---
title: Container and systemd
weight: 3
---

## Container

The image (`{{< param "project.image" >}}`) is built with ko:
- **Base:** `cgr.dev/chainguard/git`, which provides git and CA certificates.
- **User:** runs as UID 65532.
- **Entrypoint:** the ForgeMirror binary; the official Git LFS CLI is bundled.
- **Platforms:** linux/amd64 and linux/arm64.

```yaml {filename="compose.yaml"}
services:
  forgemirror:
    image: {{< param "project.image" >}}:<version>
    command: ["--config", "/etc/forgemirror/forgemirror.yaml"]
    # webhook; add "9090:9090" for probes and metrics, "8080:8080" for the
    # web UI (ui.enabled)
    ports: ["8081:8081"]
    read_only: true
    tmpfs: [/tmp]
    environment:
      FORGEMIRROR_STATE__DATABASE: /var/lib/forgemirror/state.db
    volumes:
      - ./forgemirror.yaml:/etc/forgemirror/forgemirror.yaml:ro
      - ./state:/var/lib/forgemirror
    secrets: [webhook-secret, github-token]
    restart: unless-stopped
    stop_grace_period: 75s   # longer than server.shutdown_timeout
secrets:
  webhook-secret: { file: ./secrets/webhook-secret }
  github-token: { file: ./secrets/github-token }
```

The database is mandatory. Before starting, create the bind-mounted directory:
`mkdir -p state` and `sudo chown 65532:65532 state` (adapt ownership for a
rootless runtime). Keep it across container replacement. There is no in-memory
fallback.

Compose mounts secrets at `/run/secrets/<name>`, so reference them as
`webhook.secret_file: /run/secrets/webhook-secret`.

## systemd

```ini {filename="/etc/systemd/system/forgemirror.service"}
[Unit]
Description=ForgeMirror
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/forgemirror --config /etc/forgemirror/forgemirror.yaml
DynamicUser=yes
StateDirectory=forgemirror
Environment=FORGEMIRROR_STATE__DATABASE=/var/lib/forgemirror/state.db
# Secrets: systemd-creds or LoadCredential; referenced via *_file.
LoadCredential=webhook-secret:/etc/forgemirror/webhook-secret
LoadCredential=github-token:/etc/forgemirror/github-token
Environment=FORGEMIRROR_WEBHOOK__SECRET_FILE=%d/webhook-secret
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
TimeoutStopSec=75
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Target tokens are paths in the config file. With `LoadCredential`, they live
under `$CREDENTIALS_DIRECTORY` (`%d`), for example
`/run/credentials/forgemirror.service/github-token`.
