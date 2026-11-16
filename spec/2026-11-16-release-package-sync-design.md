# Release and package sync — design

Status: agreed in conversation 2026-11-16; this document records it.

## Intent

The operator publishes releases (binaries, checksums, signatures, SBOMs) and
container packages (image, Helm chart as OCI artifact, their cosign
signatures and SBOMs) on the private Forgejo instance. People on the public
forges should get the same artifacts without a second build pipeline there:
nothing is rebuilt, Forgejo stays the single source of truth, and what a
public forge offers is byte-identical to what Forgejo offers.

Today forgemirror mirrors git refs, repository settings and labels. This
design adds two more parts to every mirror run: releases and container
packages.

## Goals

- Non-draft releases of a source repository appear on every typed target that
  enables them: tag, title, notes, prerelease flag and all attachments.
- Container packages linked to the source repository in Forgejo appear in the
  target forge's registry with every tag, so cosign signatures, SBOMs and
  referrer-tag indexes travel with the images and still verify.
- Both are part of the plan: the review diff shows them, review mode holds
  them back, the change log records what was applied, metrics count the
  operations.
- Forgejo's `release` and `package` webhooks start a run; scheduled runs
  catch up on anything missed (e.g. attachments uploaded after the release
  event).
- GitHub, Forgejo/Codeberg and GitLab targets.

## Non-goals

- Package types other than container/OCI (generic, npm, maven, …). They are
  skipped with a plan warning.
- Deleting packages or tags on targets. Tags the source deleted stay on the
  target. (A wrong source listing must never delete published images.)
- Rewriting release notes (links to git.bitfoo.de stay as written).
- Content comparison of attachments beyond name and size: Forgejo exposes no
  attachment digest, and published release files are not expected to change
  in place.
- Untyped targets: they have no API, so they get no releases or packages.
- Changing package visibility or linking packages to repositories on the
  target (GitHub has no API for either; see Consequences).

## Anti-goals

- Anything private becoming public. Draft releases are never synced. Packages
  are only synced when the target repository is public (see Visibility).
- A source or target token reaching a host other than its own: logs, errors,
  redirects to object storage, or attachment URLs from API responses.
- A diff that disagrees with what a sync does (unchanged rule).
- Releases or packages reaching a target in review mode without a click in
  the UI (unchanged rule).
- Buffering whole attachments or image layers in memory.

## Approaches considered

1. **Extend the mirror run (chosen).** Releases and packages become further
   plan sections and apply steps in the dispatcher, after the git push.
   Review mode, change log, metrics, scheduled catch-up, per-repository
   coalescing and the API semaphore apply unchanged. Webhooks only start a
   normal run. Cost: the dispatcher grows.
2. **A separate release/package syncer.** Own triggers and loop. Rejected:
   review mode would not cover it (releases would go public unreviewed), and
   it duplicates scheduling, queueing and rate limiting.
3. **Approach 1 with an external tool (`crane`, `skopeo`) for images.**
   Rejected: one more binary in the image, output parsing, and credentials on
   command lines or in files. go-containerregistry does the same in-process.

## Decisions

### Configuration

```yaml
targets:
  - name: github
    type: github
    url: https://github.com/acme/{{ .Name }}.git
    token_file: /run/secrets/github
    registry: ""          # new; registry host override, see below
    prune_releases: false # new; delete target releases absent on the source
    settings:
      labels: true
      releases: true      # new
      packages: true      # new
    overrides:
      - repos: acme/legacy-.*
        settings:
          packages: false
```

- `settings.releases` and `settings.packages` are `*bool` in
  `forge.Settings`, next to `labels`, so target defaults and per-repository
  overrides work as for labels. **Both default to off**: package sync needs
  extra token scopes, and an existing deployment must not start publishing
  binaries or fail on missing scopes after an upgrade. (Labels default to
  on; the difference is deliberate.)
- `prune_releases` mirrors `prune_labels`: off by default; when on, target
  releases whose tag has no non-draft release on the source are deleted.
- `registry` is the target's container registry host. Defaults: `github.com`
  → `ghcr.io`; `gitlab.com` → `registry.gitlab.com`; `forgejo` type → the
  host of the target URL. Any other host (GitHub Enterprise, self-hosted
  GitLab) requires `registry` when packages are enabled anywhere on that
  target; config validation reports it.
- Packages require `source.base_url` and `source.token` (the source registry
  is the host of `source.base_url`). The source token needs `read:package`.
- Env vars, JSON schema, `forgemirror.example.yaml`, Helm values/README and
  the configuration docs follow, as for every config key.

### Forge model

`internal/forge` gains forge-neutral types and interface methods, implemented
for Forgejo (source and target), GitHub and GitLab:

- `Release{Tag, Name, Body, Prerelease, Draft, CreatedAt, Assets}` and
  `Asset{Name, Size}` plus whatever a client needs to download or delete
  (opaque ID / URL, never logged with credentials).
- `Source`: list releases with their assets; open an asset for reading
  (streaming `io.ReadCloser` plus size); list the container packages linked
  to a repository with their tags.
- `Target`: list, create, update, delete releases; upload and delete assets.
- `Capabilities` gains `Prerelease` (GitLab has no prerelease flag; the field
  is then left out of the diff) and `ReleaseAssets` (see GitLab).

### Releases

- Matching is by tag. A source release is synced when it is not a draft and
  its tag exists on the target after the push. A release whose tag is
  missing on the target is skipped with a plan warning (e.g. the tag push
  failed or refs were unknown).
- Compared fields: name, body, prerelease (where supported). Assets by name;
  same name with a different size is replaced (delete, then upload); target
  assets missing on the source are deleted; the release is owned by the
  mirror.
- New releases are created oldest first (source `CreatedAt`), so a backfill
  of many releases leaves GitHub's "latest" on the newest one.
- Uploads stream from the source download to the target upload. GitHub's
  upload needs the length, which the source listing provides. Where an SDK
  buffers the request body (check the Forgejo SDK's multipart upload), the
  client builds the request itself with a streaming body.
- Source downloads carry the source token only to the host of
  `source.base_url` (checked like `CheckCloneURL`); redirects to another
  host (object storage) are followed without credentials.

### GitLab

GitLab releases cannot hold uploaded files, and links back to Forgejo would
be dead (not internet-exposed). Attachments are uploaded to the project's
generic package registry (package name `release-assets`, version = tag,
file = asset name) and linked from the release as links of type `package`.
Asset comparison uses the link names and the generic package file sizes.
GitLab has no prerelease flag; it is ignored for GitLab targets.

### Packages

- Source listing: Forgejo packages of the repository owner with type
  `container` whose linked repository is the source repository. Every
  version that is a tag is copied; `sha256:…` versions are untagged
  manifests reached through their index and are not copied on their own.
  Tags like `sha256-<digest>`, `….sig`, `….sbom` are ordinary tags and are
  copied, which carries signatures, SBOMs and the referrers tag schema.
- Target reference: `<registry>/<target namespace>/<package name>`; for
  GitLab `<registry>/<namespace>/<project>/<package name>`, or the project
  root when the package name equals the source repository name. Names are
  lower-cased. Example: `charts/forgemirror` →
  `ghcr.io/dvonessen/charts/forgemirror`.
- Compared: for each source tag, the target tag's digest (HEAD request).
  Missing or different → copy. Copies preserve digests (manifests and
  indexes are written unchanged), so signatures stay valid.
- Implementation: go-containerregistry (`remote` package), with its own
  transport so registry bearer tokens stay scoped to their registry and
  blob redirects to object storage work. Not the forge API `http.Client`,
  whose same-host redirect rule would break blob downloads.
- Credentials: source `username` + token on the source registry; target
  `username` + token on the target registry.

### Visibility

- Releases follow repository visibility on all three forges; a private
  target repository keeps its releases private.
- Packages do not reliably follow it (Forgejo/Codeberg packages are owned
  by the user or organisation, not the repository). Packages are therefore
  only synced when the desired target repository is public; otherwise the
  plan carries a warning and nothing is copied.

### Plan, apply and change log

- `plan.Target` gains release changes (create / update with changed fields /
  delete, each with asset adds, replaces and removals) and package changes
  (per image: tags to create or update, with digests). The UI diff renders
  them like labels.
- Plan computation uses read-only calls only (listing releases and assets,
  registry tag listing and HEAD).
- Apply order per target: create/privatise → push → settings → labels →
  releases → packages. A target that failed before the push gets neither.
  Releases and packages run even when the push was skipped (no ref changes).
- Observer operations `releases` and `packages`; INFO change records such as
  `msg="release created" target=github tag=v0.4.0 assets=8` and
  `msg="package tags copied" target=github package=forgemirror tags=[…]`,
  with long lists summarised as for refs.
- Large copies take one API slot each; the mirror timeout bounds the run.

### Webhook

- `X-Forgejo-Event: release` (any action) and `package` (`created`,
  `deleted`) start an ordinary run for the repository in the payload, after
  the filter admits it. Package events without a linked repository are
  acknowledged and ignored. In review mode they only plan, like pushes.
- Payload repository data is validated like push payloads (clone URL below
  `source.base_url`).
- The webhook docs list the two extra events to enable on Forgejo.

## Consequences for the operator

- GitHub creates new ghcr packages as private and links them to a repository
  only through the image label `org.opencontainers.image.source`, which
  forgemirror cannot change without changing the digest (and breaking the
  signature). After the first copy the operator makes each package public
  and links it once in the GitHub UI. Documented in the configuration docs.
- Token scopes: GitHub classic PAT with `write:packages` (fine-grained
  tokens cannot write ghcr), Forgejo/Codeberg `write:package`, GitLab
  `api` + `write_registry`. Documented in the security docs.

## Left to the builder

- Exact type and method names; whether release/package logic lives in new
  files of `internal/dispatch` or a helper package.
- Whether webhook release/package events reuse `EventPush` or get their own
  kinds, as long as coalescing and review-mode rules hold.
- Pagination sizes, and the summarising limit for long change lists.
- Verifying registry login details per forge (accepted usernames) against
  the e2e Forgejo and the forge docs; tests use fakes, as everywhere.
