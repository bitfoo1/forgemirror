---
title: forge-mirror - Project Spec
type: spec
permalink: main/projects/forge-mirror/forge-mirror-project-spec
tags:
- golang
- git
- forgejo
- mirror
- kubernetes
- otel
- prometheus
---

A webhook-driven service written in Go that listens for Forgejo push events and immediately mirrors the affected repository to one or more remote forges (GitHub, Codeberg, GitLab, etc.).

**Learning goals per phase are listed explicitly. Complete them in order — each phase teaches concepts you will need in the next.**

---

## Status

- [ ] Phase 1: Project Setup & Go Basics
- [ ] Phase 2: Configuration
- [ ] Phase 2.5: Filtering & Metadata
- [ ] Phase 3: Core Mirroring Logic
- [ ] Phase 4: HTTP Webhook Server
- [ ] Phase 5: Concurrency & Multi-Target
- [ ] Phase 6: Observability (Logging + Metrics)
- [ ] Phase 7: Kubernetes Deployment

---

## Phase 1: Project Setup & Go Basics

> **What you will learn:** Go module system, project layout conventions, how to build and run a Go binary, how the `main` package works, basic CLI flags.

- [ ] Install Go and verify the installation with `go version`
- [ ] Read about Go's standard project layout (there is no enforced structure, but community conventions exist — research `cmd/` and `internal/` directories)
- [ ] Create a new directory for the project
- [ ] Initialize a Go module: understand what `go.mod` is and what the module path means
- [ ] Create a `main.go` in `cmd/forge-mirror/` that prints "forge-mirror starting" and exits cleanly
- [ ] Build the binary with `go build` and run it
- [ ] Add a `-version` flag using the standard `flag` package that prints a version string and exits
- [ ] Create a `Makefile` with the following targets:
  - [ ] `build` — compiles the binary
  - [ ] `test` — runs all tests
  - [ ] `lint` — runs `golangci-lint` (research how to install it)
  - [ ] `run` — builds and runs the binary
- [ ] Understand the difference between `go run`, `go build`, and `go install`

---

## Phase 2: Configuration

> **What you will learn:** Go structs, interfaces, reading files, JSON/YAML unmarshalling, environment variables, error handling patterns (`if err != nil`), and the concept of zero values.

- [ ] Define a `Config` struct that holds all runtime configuration (research what a struct is in Go)
- [ ] The config struct must contain:
  - [ ] Server listen address (e.g. `0.0.0.0:8080`)
  - [ ] Webhook HMAC secret
  - [ ] Log level (`debug`, `info`, `warn`, `error`)
  - [ ] A slice of `Target` structs, each with:
    - [ ] A human-readable name
    - [ ] The remote URL to push to
    - [ ] An access token (credential)
- [ ] Support loading config from a YAML file (research the `gopkg.in/yaml.v3` package)
- [ ] Support overriding any value via environment variables (research the `github.com/kelseyhightower/envconfig` package or implement manually)
- [ ] Validate the loaded config: return a descriptive error if required fields are missing
- [ ] Write a `config_test.go` file — understand how Go test files work (`_test.go` suffix, `TestXxx` function naming)
- [ ] Write tests for:
  - [ ] A valid config loads correctly
  - [ ] Missing required fields produce an error
  - [ ] An empty targets list produces an error
- [ ] Learn how to run a single test with `go test -run TestName ./...`

---

## Phase 2.5: Filtering & Metadata

> **What you will learn:** Working with regular expressions in Go (`regexp` package), boolean logic in config validation, and how to model optional behaviour cleanly in a config struct.

- [ ] Extend the `Config` struct with a `Filter` section containing:
  - [ ] `include_repos` — optional regex; only mirror repositories whose name matches
  - [ ] `exclude_repos` — optional regex; never mirror repositories whose name matches
  - [ ] `include_branches` — optional list of branch name patterns to mirror (supports regex); if empty, mirror all branches
  - [ ] `exclude_private` — boolean; skip repositories marked as private in the Forgejo webhook payload
  - [ ] `exclude_forks` — boolean; skip repositories that are forks
  - [ ] `require_topic` — optional string; only mirror repositories that have this Forgejo topic set (opt-in model)
- [ ] Compile all regex patterns once at startup and return an error if any pattern is invalid — research why compiling once is better than compiling on every event
- [ ] Implement a `Filter.Matches(repo) bool` function that applies all rules and returns whether a repository should be mirrored
- [ ] The function must apply rules in a defined order: topic check → visibility check → fork check → name include → name exclude
- [ ] Write unit tests for `Filter.Matches` covering:
  - [ ] Repo passes all filters
  - [ ] Repo blocked by each individual filter rule
  - [ ] Empty filter (no rules set) passes everything
  - [ ] Both include and exclude regex set — document and test the precedence
- [ ] Extend the webhook payload struct (Phase 4) to carry: repository visibility (private/public), fork status, and topics
- [ ] Note: `git push --mirror` mirrors all refs including tags — document this as intentional behaviour, no filtering needed for tags

---

## Phase 3: Core Mirroring Logic

> **What you will learn:** Packages and package visibility, `os/exec` for running subprocesses, `context` for cancellation and timeouts, `defer` for cleanup, interfaces as contracts, error wrapping with `fmt.Errorf`.

- [ ] Create a new package `internal/mirror` — understand why `internal/` prevents external packages from importing it
- [ ] Define a `Mirrorer` interface with a single method: mirror a source URL to a target URL
- [ ] Implement the interface using `os/exec` to call the `git` binary:
  - [ ] Understand how `exec.CommandContext` works and why context matters
  - [ ] Step 1: `git clone --mirror <source-url> <temp-dir>`
  - [ ] Step 2: `git push --mirror <target-url>` from inside the temp dir
  - [ ] Step 3: delete the temp dir (use `defer` — research what defer does and when it runs)
- [ ] Inject credentials into the remote URL at runtime (e.g. `https://token@github.com/user/repo`) — never write them to disk
- [ ] Capture both stdout and stderr from git commands and include them in error messages
- [ ] Wrap errors with context using `fmt.Errorf("cloning %s: %w", url, err)` — research error wrapping and `errors.Is` / `errors.As`
- [ ] Write tests for the mirror package:
  - [ ] Test that a context timeout causes the operation to fail with an appropriate error
  - [ ] Test that cleanup (temp dir removal) happens even when the operation fails
  - [ ] Use a fake/stub git command to avoid real network calls in tests (research how to make `exec.Command` testable)

---

## Phase 4: HTTP Webhook Server

> **What you will learn:** Go's `net/http` package, HTTP handlers, reading request bodies, JSON decoding, HMAC signature verification using `crypto/hmac`, goroutines (briefly), and HTTP response codes.

- [ ] Create a new package `internal/webhook`
- [ ] Implement an HTTP handler for `POST /webhook`:
  - [ ] Read the full request body (research why you must read and close `r.Body`)
  - [ ] Extract the `X-Gitea-Signature` header (Forgejo uses the same header as Gitea)
  - [ ] Compute the expected HMAC-SHA256 signature of the body using the configured secret
  - [ ] Compare signatures using `hmac.Equal` — research why you must NOT use `==` for this (timing attacks)
  - [ ] Return `401` if the signature is invalid
  - [ ] Decode the JSON body into a struct representing a Forgejo push event — look up the Forgejo webhook payload format in their documentation
  - [ ] Extract the repository clone URL from the payload
  - [ ] Trigger the mirror operation — for now, just call it synchronously (you will make it async in Phase 5)
  - [ ] Return `200` on success, `500` on mirror failure
- [ ] Implement `GET /healthz` that always returns `200 OK` with body `ok`
- [ ] Wire the handlers into an `http.ServeMux` in `main.go`
- [ ] Start the HTTP server and verify it works with `curl`
- [ ] Write tests for the webhook handler:
  - [ ] Valid signature + valid payload → `200`
  - [ ] Invalid signature → `401`
  - [ ] Missing signature header → `401`
  - [ ] Valid signature + malformed JSON → `400`
  - [ ] Research `httptest.NewRecorder` and `httptest.NewRequest` for testing HTTP handlers without a real server

---

## Phase 5: Concurrency & Multi-Target

> **What you will learn:** Goroutines, `sync.WaitGroup`, channels (briefly), the fan-out pattern, and how to handle errors from concurrent operations.

- [ ] Update the config to support multiple targets per repository (already done in Phase 2 — now use it)
- [ ] When a push event arrives, mirror to all configured targets:
  - [ ] Launch one goroutine per target (research what a goroutine is)
  - [ ] Use `sync.WaitGroup` to wait for all goroutines to finish before returning
  - [ ] Collect errors from each goroutine using a channel or a mutex-protected slice
  - [ ] Log each target's outcome independently — a failure on one target must not stop others
- [ ] Make the webhook handler non-blocking:
  - [ ] The HTTP handler should return `202 Accepted` immediately
  - [ ] The mirror work happens in a background goroutine
  - [ ] Research the tradeoff: if the process crashes, in-flight work is lost — acknowledge this limitation
- [ ] Handle the case where a push event arrives for a repo that is already being mirrored:
  - [ ] Choose a strategy: **skip** (simplest — implement this first), queue, or replace
  - [ ] Use a `sync.Mutex` and a map to track in-progress repositories
  - [ ] Research what a mutex is and why it is needed when multiple goroutines share data

---

## Phase 6: Observability (Logging + Metrics)

> **What you will learn:** Structured logging with `log/slog` (Go 1.21+), OpenTelemetry concepts (traces, metrics, the OTel SDK), Prometheus exposition format, and how to instrument code without coupling it to a specific backend.

### 6a — Structured Logging

- [ ] Replace any `fmt.Println` / `log.Printf` calls with `log/slog` (built into Go 1.21+)
- [ ] Configure slog to output JSON in production and human-readable text in development
- [ ] Every log entry for a mirror operation must include: target name, repository URL, duration, outcome
- [ ] Ensure no log entry ever contains a token or secret — audit every log call

### 6b — OpenTelemetry Setup

- [ ] Research what OpenTelemetry is: a vendor-neutral observability framework. Understand the difference between traces, metrics, and logs in OTel
- [ ] Add the OTel Go SDK dependencies:
  - [ ] `go.opentelemetry.io/otel`
  - [ ] `go.opentelemetry.io/otel/metric`
  - [ ] `go.opentelemetry.io/otel/exporters/prometheus` (exports OTel metrics in Prometheus format)
  - [ ] `github.com/prometheus/client_golang/prometheus/promhttp` (serves the `/metrics` HTTP endpoint)
- [ ] Initialize an OTel `MeterProvider` at startup using the Prometheus exporter
- [ ] Understand the difference between a `MeterProvider` (global factory) and a `Meter` (per-package instrument creator)

### 6c — Instrument the Code

- [ ] In `internal/mirror`, create the following OTel metric instruments:
  - [ ] A **Counter** named `forge_mirror_operations_total` with attributes `target` (name) and `status` (`success` / `failure`)
    - Research: a counter only goes up, never resets
  - [ ] A **Histogram** named `forge_mirror_duration_seconds` with attribute `target`
    - Research: a histogram records the distribution of values (e.g. how long operations take)
    - Use `time.Now()` before the operation and record the elapsed duration after
- [ ] In `internal/webhook`, create:
  - [ ] A **Counter** named `forge_mirror_webhook_requests_total` with attribute `validation` (`valid` / `invalid`)
  - [ ] A **Gauge** named `forge_mirror_active_operations` that increments when a mirror starts and decrements when it finishes
    - Research: a gauge can go up and down (unlike a counter)
- [ ] Expose the metrics endpoint at `GET /metrics` using the Prometheus HTTP handler
- [ ] Verify metrics appear by running the service and calling `curl localhost:8080/metrics`

### 6d — Graceful Shutdown

- [ ] Listen for `SIGTERM` and `SIGINT` signals using `os/signal` and `signal.NotifyContext`
- [ ] On signal: stop accepting new webhook requests, wait for all in-progress mirror goroutines to finish, then exit
- [ ] Use a `sync.WaitGroup` to track all active goroutines
- [ ] Set a maximum shutdown timeout (e.g. 60 seconds) — if goroutines do not finish in time, force exit

---

## Phase 7: Kubernetes Deployment

> **What you will learn:** Multi-stage Docker builds, Kubernetes core resources (Deployment, Service, Secret), and how to configure a running service via environment variables and mounted secrets.

- [ ] Write a `Dockerfile` using a multi-stage build:
  - [ ] Stage 1 (`builder`): use the official Go image, copy source, run `go build`
  - [ ] Stage 2 (`final`): use a minimal base image (e.g. `debian:bookworm-slim`), install `git`, copy only the compiled binary
  - [ ] Research why multi-stage builds keep the final image small
- [ ] Build the image locally and verify it runs with `docker run`
- [ ] Write a `deploy/deployment.yaml` Kubernetes manifest:
  - [ ] 1 replica to start
  - [ ] Resource requests and limits for CPU and memory
  - [ ] Liveness probe pointing at `GET /healthz`
  - [ ] Readiness probe pointing at `GET /healthz`
  - [ ] Environment variables sourced from a Kubernetes Secret
- [ ] Write a `deploy/service.yaml` with type `ClusterIP` (internal only, no public exposure)
- [ ] Write a `deploy/secret.yaml` template with placeholder values and a comment explaining how to fill it in
- [ ] Write a short `deploy/README.md` explaining:
  - [ ] How to apply the manifests with `kubectl apply`
  - [ ] How to configure Forgejo system webhooks (Admin → System Webhooks) to point at the service's cluster-internal DNS name
  - [ ] How to verify the service is receiving webhooks by checking logs with `kubectl logs`
