# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-15

### Features

- **cli:** Add project skeleton with version from build info (d86994e)
- **config:** Load layered configuration with koanf (31feb56)
- **filter:** Decide per push event whether to mirror (88ad664)
- **mirror:** Clone once and push branches and tags to targets (9194e02)
- **webhook:** Authenticate push events and serve HTTP endpoints (a586ecb)
- **dispatch:** Mirror in the background with fan-out and coalescing (3fd56a3)
- **telemetry:** Expose Prometheus metrics via OpenTelemetry (1433ff5)
- **deploy:** Add Kubernetes manifests with schema validation (17ec8ce)
- **chart:** Add Helm chart on the bjw-s common library (67dd416)
- **config:** Publish a JSON Schema for the configuration file (5a2564c)

### Bug fixes

- **docs:** Stop publishing placeholder .gitkeep files (e0702b4)
- **release:** Require publishing secrets only when a release is due (9cc07aa)
- **docs:** Reject local rsync targets when publishing the docs (2f552f9)
- **release:** Keep git hooks out of CI and the release bot push (15a8ffa)
- **docs:** Stop the template URL from splitting a home page card (1784dca)

### Documentation

- **spec:** Turn the learning spec into an implementation roadmap (01c1a11)
- **site:** Add Hugo + Hextra documentation site with rsync publishing (5a92603)
- **ci:** Document the rsync target for rrsync-restricted deploy keys (e3a140c)
- **site:** Give the documentation a forge-to-mirror color palette (e17a41a)
- **site:** Tone the color palette down to a single accent (d69ab61)

### CI

- **forgejo:** Add pipeline, release PR flow and goreleaser (e6a98eb)
- **forgejo:** Cache mise tools and Go builds in the runner cache (9a45122)

### Miscellaneous

- **license:** Added license file (d593fff)
- **agents:** Added AGENTS.md with instructions, how agents should behave (b7c13b0)
- **mise:** Added initial mise configuration (85b43e1)
- **init:** Added initial go module (a8966a4)
- **lint:** Added initial linter rules for golangci-lint (3c3fd93)
- **tooling:** Manage tools, tasks and git hooks with mise (dabd3e2)
- **git:** Ignore stray root build outputs (93e69fc)


