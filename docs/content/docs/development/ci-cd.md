---
title: CI/CD and releases
weight: 2
---

## Pipeline

`.forgejo/workflows/ci.yaml` runs on pushes to `main`, on `v*.*.*` tags and
on pull requests. Every step is a mise task, so a local `mise run check`
(also run by the pre-push hook) predicts the pipeline result.

| Job         | Runs on          | Steps                                                                                        |
|-------------|------------------|----------------------------------------------------------------------------------------------|
| `verify`    | PR, push to main | `commits:check` → `check` (fmt, tidy, vet, lint, test + coverage, govulncheck, manifests) → `snapshot` |
| `changelog` | after `verify`   | PR: `changelog:preview --pr` · main: `release:publish`, then `release:pr` · tag: `release:publish` |

The changelog job is always last: it only runs once the code is verified.

`.forgejo/workflows/docs.yaml` publishes this documentation separately, on
pushes to `main` that touch the docs (or files the docs embed). It runs
`docs:check`, then `docs:publish`, which builds the site and syncs it to the
web host with `rsync --delete` over SSH. Keeping it out of the CI workflow
means the changelog job stays last there, and a failed upload never blocks a
release.

## Release flow

Releases are never tagged by hand.

1. Merging feature/fix PRs into `main` releases nothing.
2. After every push to `main`, `release:pr` asks git-cliff for the next
   semver version. If there are user-facing commits (`feat`, `fix`, `perf` or
   breaking changes) since the last tag, it regenerates `CHANGELOG.md` on the
   branch `release/next` and opens or updates the PR
   `chore(release): :bookmark: vX.Y.Z`. If there are none, it closes a stale
   release PR.
3. To release, merge that PR (merge, squash or rebase all work).
4. The push to `main` runs `release:publish`. It reads the newest version from
   `CHANGELOG.md`, creates the tag, and runs goreleaser in the same job:
   binaries for linux/darwin × amd64/arm64, a multi-arch image at
   `git.bitfoo.de/dvonessen/forgemirror`, SBOMs, cosign signatures, and the
   Forgejo release with git-cliff release notes. Then it packages the Helm
   chart with the same version, pushes it to
   `oci://git.bitfoo.de/dvonessen/charts/forgemirror` and signs it with cosign.

Why the tag and the release happen in one job: tags and pushes made with the
automatic Actions token start no new workflow run
([forgejo#11446](https://codeberg.org/forgejo/forgejo/issues/11446)). The same
rule keeps the changelog from triggering an extra pipeline. For the same
reason, `release/next` is pushed with `RELEASE_TOKEN`. Pushed with the
automatic token, the release PR would get no checks.

Version bumps (git-cliff, `cliff.toml`): `feat` → minor, breaking change
(`!` or `BREAKING CHANGE:`) → major, everything else → patch. The first
release is `v0.1.0`.

## One-time setup

### Secrets (repository → Settings → Actions → Secrets)

| Secret               | Purpose                                                                         |
|----------------------|---------------------------------------------------------------------------------|
| `RELEASE_TOKEN`      | PAT with `write:repository`; pushes `release/next` and opens the release PR     |
| `PACKAGE_RW_TOKEN`   | PAT with `write:package`; pushes image and chart                               |
| `COSIGN_PRIVATE_KEY` | contents of `cosign.key`                                                        |
| `COSIGN_PASSWORD`    | password of `cosign.key`                                                        |
| `GH_TOKEN`           | optional; GitHub token so mise's tool downloads are not rate-limited            |
| `DOCS_SSH_KEY`       | base64-encoded private SSH key allowed to write the docs document root          |

### Variables (repository → Settings → Actions → Variables)

| Variable               | Purpose                                                             |
|------------------------|---------------------------------------------------------------------|
| `DOCS_DEPLOY_TARGET`   | rsync target, `user@host:/document/root/`; just `user@host:` when the deploy key is restricted with rrsync, which resolves paths below its root. The docs job is skipped while unset |
| `DOCS_BASE_URL`        | public URL of the site, e.g. `https://forgemirror.example.org/`       |
| `DOCS_SSH_KNOWN_HOSTS` | pinned host key line(s) of the web host (`ssh-keyscan host`, verified once by hand) |

Create the signing key pair locally and commit only the public key:

```sh
mise exec -- cosign generate-key-pair   # writes cosign.key and cosign.pub
```

Store `cosign.key` and its password as secrets, commit `cosign.pub`, and
delete `cosign.key` from disk.

Keyless signing is not an option: Sigstore's Fulcio would have to reach the
Forgejo OIDC issuer, and the instance is not internet-exposed. Signatures are
still recorded in the public Rekor transparency log, which publishes only
hashes and the public key.

### Branch protection (repository → Settings → Branches)

Rule for `main`:

- Disable direct pushes; changes land via pull requests only.
- Required status checks: `CI / verify (pull_request)` and
  `CI / changelog (pull_request)`.
- Enable "Block merge on outdated branch", so checks always ran against the
  current `main`.
- Disable force pushes and branch deletion.
- Optional: require one approval. The release bot's PR then needs your review,
  which is the point of having a release PR.

Rule for `release/*`:

- Allow force pushes only for the account that owns `RELEASE_TOKEN`; the bot
  regenerates the branch on every run.

Tags: protect `v*` so that only the Actions token (release job) and
administrators can create release tags.

## Verifying a release

```sh
cosign verify-blob --key cosign.pub \
  --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --check --ignore-missing checksums.txt

cosign verify --key cosign.pub git.bitfoo.de/dvonessen/forgemirror:<version>
```
