# Release and package sync — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mirror non-draft releases (with attachments) and repository-linked
container packages from Forgejo to typed targets as part of every mirror run.

**Architecture:** Optional forge interfaces (`forge.ReleaseSource`,
`forge.ReleaseTarget`, `forge.PackageSource`) keep existing fakes valid; the
dispatcher type-asserts them. A new `internal/registry` package copies OCI
tags with go-containerregistry. Plan, apply, change log, UI and webhook follow
the label-sync pattern.

**Tech Stack:** Go 1.27, go-github v93, forgejo-sdk v4, GitLab client-go v3,
github.com/google/go-containerregistry.

**Spec:** `spec/2026-11-16-release-package-sync-design.md`

## Global Constraints

- `settings.releases` / `settings.packages` default **off**; labels stay on.
- Draft releases are never synced; packages only when the desired target repository is public.
- Packages and image tags are never deleted on targets; releases only with `prune_releases: true`.
- Tokens never in logs, errors or URLs; the source token only goes to the host of `source.base_url`.
- No whole-file buffering of attachments or layers.
- Transfers (attachments, image copies) are bounded by `mirror.timeout`, not the 30 s API timeout.
- `mise run check` green before every commit; conventional commits with gitmoji.

## Review Focus

- Source release/package listing fails → no release deleted on any target (prune must not act on a failed listing). Test in Task 6.
- Release asset with the same name but a different size → replaced, not duplicated. Test in Task 6.
- Attachment download redirected to another host → followed without the token. Test in Task 2.
- Private repository with packages enabled → warning, nothing copied. Test in Task 6.
- Package webhook without a linked repository → 200 ignored, nothing dispatched. Test in Task 7.

---

### Task 1: Config and forge model

**Files:** `internal/forge/forge.go`, `internal/forge/release.go` (new),
`internal/forge/resolve.go`, `internal/config/target.go`,
`internal/config/validate.go`, schema JSON, `forgemirror.example.yaml`, tests.

**Produces:**
- `forge.Settings.Releases, Packages *bool` (`koanf:"releases"`, `"packages"`).
- `forge.Features{Labels, Releases, Packages bool}`; `forge.ResolveFeatures(layers ...Settings) Features` (labels default true, others false).
- `forge.Release{ID int64; Tag, Name, Body string; Prerelease, Draft bool; CreatedAt time.Time; Assets []Asset; UploadURL string}`, `forge.Asset{ID int64; Name string; Size int64; URL string}`.
- `forge.ReleaseSource{Releases(ctx, owner, name) ([]Release, error); OpenAsset(ctx, Asset) (io.ReadCloser, error)}`.
- `forge.ReleaseTarget{Releases(ctx, Address) ([]Release, error); CreateRelease(ctx, Address, Release) (Release, error); UpdateRelease(ctx, Address, current, desired Release) error; DeleteRelease(ctx, Address, Release) error; UploadAsset(ctx, Address, Release, name string, size int64, body io.Reader) error; DeleteAsset(ctx, Address, Release, Asset) error; ReleaseCapabilities() ReleaseCapabilities}`; `ReleaseCapabilities{Prerelease bool}`.
- `forge.Package{Name, Type string; Tags []string}`; `forge.PackageSource{Packages(ctx, owner, name) ([]Package, error)}`.
- `config.Target.Registry string`, `PruneReleases bool`; `(*Target).RegistryHost() (host string, insecure bool, err error)`; `config.Source.RegistryHost()` likewise.

Tests: ResolveFeatures defaults and override precedence; RegistryHost table (github.com→ghcr.io, gitlab.com→registry.gitlab.com, forgejo http URL→host+insecure, GHE without registry→error, explicit `http://h:1`); validation: untyped with `prune_releases`/`registry` → "requires type"; packages enabled on GHE without registry → error; schema test stays green.

### Task 2: Transfer HTTP client and safe download

**Files:** `internal/forge/httpclient/httpclient.go`, `download.go` (new), tests.

**Produces:** `httpclient.NewTransfer() *http.Client` (same redirect rule, no timeout); `httpclient.Download(ctx, c *http.Client, rawURL, allowedBase, authHeader string) (io.ReadCloser, error)` — refuses URLs outside `allowedBase` (same rule as `CheckCloneURL`), sends the auth header only to that host, follows one cross-host redirect without credentials, refuses https→http.

Tests: auth header present on same host; absent after cross-host redirect; URL outside base refused; non-2xx → error without URL credentials.

### Task 3: Forgejo releases and packages

**Files:** `internal/forge/forgejo/release.go` (new), `lazy.go`, tests with `httptest` server (existing pattern).

Source view implements `ReleaseSource` and `PackageSource`; `Client` and `Lazy` implement `ReleaseTarget`. Packages: `ListPackages(owner)` paginated, keep entries whose repository full name equals the source (case-insensitive), group by name, drop `sha256:` versions. Upload builds its own multipart request streamed through `io.Pipe` on the transfer client.

Tests: drafts listed with Draft=true; packages grouped and filtered; upload streams body and filename; delete by ID.

### Task 4: GitHub and GitLab release targets

**Files:** `internal/forge/github/release.go`, `internal/forge/gitlab/release.go` (new), tests.

GitHub: list (paginated, assets included), create with `make_latest: "legacy"`, edit, delete, `UploadReleaseAssetFromRelease` on a transfer-client `gh.Client`, delete asset. GitLab: release by tag; assets = links of type `package` to generic package `release-assets`, version = tag; sizes from the package files; upload `PublishPackageFile` then `CreateReleaseLink`; delete removes link and package file. `ReleaseCapabilities{Prerelease: false}` for GitLab.

### Task 5: Registry copier

**Files:** `internal/registry/registry.go` (new), tests with ggcr's in-memory registry (`pkg/registry`).

**Produces:** `registry.Repository{Host, Path string; Insecure bool; Username string; Password config.Secret}`, `(Repository).String()`; `registry.Client` with `Digests(ctx, Repository, tags []string) (map[string]string, error)` (missing repo/tag → absent) and `Copy(ctx, src, dst Repository, tag string) error` (index or manifest, digest-preserving).

Tests: copy image and index keep digests; Digests on missing repo is empty.

### Task 6: Plan, apply, change log, UI

**Files:** `internal/plan/plan.go`, `internal/dispatch/{plan.go,sync.go,release.go,package.go,changelog.go,dispatch.go}`, `internal/ui/templates/ui.html`, telemetry description, tests.

**Produces:** `plan.ReleaseChange{Tag string; Old, New *forge.Release; Fields []FieldChange; AssetsAdded, AssetsReplaced, AssetsRemoved []string}` with `Kind()`; `plan.PackageChange{Package, Image string; Tags []TagChange}`, `plan.TagChange{Tag, Old, New string}`; `Target.Releases`, `Target.Packages`; `Changes()` counts them. `dispatch.Options.Registry` (interface with Digests/Copy), `Options.TransferTimeout`. Operations `releases`, `packages`.

Tests (fakes): create oldest first; update fields; asset replace by size; prune only with `prune_releases`; source listing failure → warning, no deletes; tag missing in source refs → warning; packages copied only for public repos; non-container package → warning; review mode plans only.

### Task 7: Webhook events

**Files:** `internal/webhook/{webhook.go,payload.go}`, tests.

`release` (any action) and `package` events → `Event{Kind: EventPush}` after validation, clone URL check and filter; ignored when `IgnorePush`; package without repository → 200 ignored.

### Task 8: Wiring, e2e, docs

**Files:** `internal/app/app.go`, `e2e/e2e_test.go`, `mise-tasks/test/e2e` (token scopes), docs (`configuration.md`, `forgejo-webhook.md`, `security.md`, `web-ui.md`), Helm chart README if it lists keys.

e2e: release with attachment and an image linked to the repository are mirrored to a Forgejo target in another org on the same instance.

### Task 9: Gate, push, PR

`mise run check`, `mise run test:e2e`, push the branch, open the PR on Forgejo.
