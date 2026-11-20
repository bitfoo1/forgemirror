# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.7.0] - 2026-11-20

### Features

- **ui:** Persist sync review and update live diffs (952dd93)

### Bug fixes

- **ui:** Remove duplicate target names beneath forge tiles (8239617)

## [0.6.1] - 2026-11-20

### Bug fixes

- **forge:** Give forge HTTP clients their own transport (287fa9a)
- **dispatch:** Stop copying a package after an access denial (6106515)

### Documentation

- Explain registry denials during package sync (6ec3da0)

## [0.6.0] - 2026-11-16

### Features

- **forge:** [**breaking**] Sync releases and packages by default (8775692)

## [0.5.0] - 2026-11-16

### Features

- **config:** Add release and package sync settings (1a1ba94)
- **forge:** Read and write releases on Forgejo, GitHub and GitLab (70ba836)
- **registry:** Copy OCI tags between registries with their digests (be076a2)
- **dispatch:** Plan and apply release and package sync (6f0fbd4)
- **webhook:** Start a run on release and package events (9e273dc)
- **ui:** Show target types as chips in the targets tile (72ade3c)
- **forge:** Mark mirrored repositories with a note and topic (716b837)

### Bug fixes

- **telemetry:** [**breaking**] Use forgemirror_ prefix for all metrics (0463b9f)
- **ui:** Spell ForgeMirror and use singular counts (1ec401e)

### Documentation

- **spec:** Design release and package sync (4402d89)
- Document release and package sync (9449743)
- Theme the site with Tokyo Night and add web UI screenshots (fd9da24)
- Update for ForgeMirror, current features and GitHub (58bb171)

### Tests

- **e2e:** Mirror a release and a container image (3825a06)
- **e2e:** Keep the shared-instance target out of the source list (9af8c01)
- **e2e:** Test against Forgejo LTS and latest, kept current by Renovate (6ecf665)
- **mirror:** Drop inherited GIT_* vars in real-git test helpers (7f53eea)
- **e2e:** Hold the run with E2E_HOLD to inspect the web UI (9feadbc)

### Miscellaneous

- **merge:** Merge main into feat/release-package-sync (fd0254e)

## [0.4.0] - 2026-11-16

### Features

- **plan:** Describe what a run changes on its targets (e2f2e95)
- **config:** Add sync.auto to switch to review mode (737c4e3)
- **dispatch:** Plan every run before applying it and log each change (f7105a1)
- **ui:** Review diffs and sync from a Tokyo Night themed UI (97663d8)

### Documentation

- **spec:** Design review mode, change log and UI redesign (01a31a8)
- Document review mode, the UI actions and the change log (ec3fbdd)

### Miscellaneous

- **deps:** Update dependency jdx/mise to v2026.11.8 (af7c599)
- **deps:** Update https://github.com/jdx/mise-action action to v5.2.9 (0e98e21)
- **mise:** Update dependency docker-cli to v29.9.3 (b50a319)
- **deps:** Update node.js to v26 (235d3f8)
- **renovate:** Group updates by project area and add gitmoji (3e4b749)
- **mise:** Update dependency docker-compose to v5.7.1 (f29a1bc)
- **deps:** Update go modules (77538b1)

## [0.3.0] - 2026-11-15

### Features

- **server:** [**breaking**] Split webhook, ops and web listeners (ec864f9)
- **ui:** Add a read-only web UI (b9d301f)

### Documentation

- **spec:** Design the web UI and the port split (7438e44)

### Tests

- Create fake git scripts without an open write descriptor (3ffeb26)

### CI

- **e2e:** Point the e2e job at the runner pod's DinD daemon (832cb3c)

## [0.2.0] - 2026-11-15

### Features

- **source:** Restrict clone URLs to source.base_url (ba557a3)
- **forge:** Add forge-neutral repository model and settings resolution (255b7e5)
- **forge:** Add hardened HTTP client for forge APIs (99e0aa5)
- **forge:** Add Forgejo adapter (40b87b7)
- **forge:** Add GitHub adapter (c74367a)
- **forge:** Add GitLab adapter (1c29598)
- **config:** Add sync settings, typed targets and overrides (d8c48ea)
- **mirror:** List remote branches and tags (c478e01)
- **dispatch:** Create repositories and sync settings and labels (e51b11a)
- **webhook:** Handle repository deletions and allow disabling push events (468635f)
- **schedule:** Sync repositories on a schedule (3a2f8f6)

### Bug fixes

- **release:** Publish the CHANGELOG section as release notes (24042cb)
- **forge:** Validate forge type unconditionally and keep URLs out of parse errors (9c12afd)
- **forge:** Exclude ancestor group labels from GitLab label listing (ac15079)
- **dispatch:** Re-read created repositories and connect to Forgejo lazily (ac47390)
- **forge:** Make the lazy Forgejo connection context-aware with failure backoff (6f2a657)
- **schedule:** Let scheduled mirrors drain on shutdown instead of cancelling them (e7da7a0)
- **dispatch:** Bound forge API calls by mirror.max_concurrent (a4f3013)
- **dispatch:** Make a public target private before pushing to it (319e9c9)
- **filter:** Make private repositories opt-in for typed targets and schedules (a50f0cd)
- **forgejo:** Refuse to create repositories in a foreign user namespace (1df7f21)
- **schema:** Accept an empty api_url and cover typed configs positively (df15699)
- **httpclient:** Refuse redirects that change the scheme (0235fd3)
- **gitlab:** Treat internal projects as not private (6a7eb71)
- **webhook:** Require consistent repository fields on repository events (d719eb4)
- **github:** Skip topics GitHub cannot store (4e04b7d)
- **forgejo:** Page through listings until X-Total-Count is reached (4495f6b)
- **app:** Log at startup that GitHub ignores has_pull_requests (7869bed)

### Documentation

- **ci:** Drop the release/* branch protection advice (c04afb6)
- **readme:** Turn the README into a proper landing page (bda99bf)
- **spec:** Add forge sync design and implementation plan (2c98baa)
- **spec:** Switch forge sync to client libraries, add prune_labels and e2e test (6ef2cce)
- Document repository creation, settings sync, schedule and deletion (5759e77)
- **config:** Suggest type forgejo for the Codeberg example target (5a39359)

### Tests

- **e2e:** Add end-to-end test against a real Forgejo (74998e4)
- **e2e:** Isolate e2e runs on a shared docker daemon (82d30d7)
- **e2e:** Avoid project name collisions and keep logs of a failed start (fad0082)

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
- **release:** Push protected release tags with RELEASE_TOKEN (d4c2ed1)

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


