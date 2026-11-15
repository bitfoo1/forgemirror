# Forge Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** forgemirror creates missing target repositories, syncs settings and labels from Forgejo with per-target defaults and per-repo overrides, can run on a schedule, and can delete targets whose source was deleted.

**Architecture:** A forge-neutral model (`internal/forge`) with thin adapters over the forges' Go libraries, sharing one hardened `http.Client`. The dispatcher's run gains steps around the existing clone/push: ensure repo → push → settings → labels. A scheduler and new webhook event kinds feed the same dispatcher.

**Tech Stack:** Go 1.27 (mise), go-github v93, GitLab client-go v3, forgejo-sdk v4, httptest, koanf, OpenTelemetry, Docker (e2e only).

**Spec:** `spec/2026-11-15-forge-sync-design.md`

## Global Constraints

- `mise run check` must pass before every commit (fmt, tidy, vet, golangci-lint golden config, tests with race detector, ≥ 80 % coverage of `internal/`, govulncheck, manifests, chart, strict docs build).
- New Go module dependencies exactly: `github.com/google/go-github/v93@v93.0.0`, `gitlab.com/gitlab-org/api/client-go/v3@v3.40.0`, `codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v4@v4.0.0`. No others (test helpers included). Each library is constructed with forgemirror's `http.Client` from Task 2.
- Secrets (`config.Secret`) never reach logs, error messages, process arguments or disk; error strings from API calls carry method, path, status and the API's message only.
- Tests are table-driven, call `t.Parallel()`, live in `<pkg>_test` packages, and use `httptest.Server` fakes; no network.
- Comments explain why, not what. `//nolint` only with a linter name and a reason.
- No package-level mutable state; `context.Context` first parameter, never stored in structs.
- Commits: `type(scope): :gitmoji: description`, body optional, last line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Allowed types: feat, fix, perf, refactor, docs, test, build, ci, chore, style, revert.
- Topics are lower-cased for every target. Label colors are compared case-insensitively without a leading `#`.
- A private source repository is private on every target, whatever the config says. `settings.private` accepts only `true`.
- Deletion only on a Forgejo `repository` event with `action: "deleted"` and only for targets with `delete: true`.
- Targets without `type` behave exactly as before this plan (git push only, no API calls).
- Before relying on any library call or endpoint shape, read the library source in the module cache (`go doc`, `$(go env GOMODCACHE)`) and the forge's API docs. Never guess field or method names.
- The JSON schema stays at `docs/static/schema/v1/` (additive changes only) and must describe every new key.

## Review Focus

1. A private source with `private` absent everywhere, or a public source with `private: true` in an override → resulting repo is private; nothing in config can make a private source public (Task 1 test `TestResolveVisibility`, Task 8 test `TestRunCreatesPrivateRepoPrivate`).
2. Target repository missing and `create: false` → that target fails with an error naming the target, other targets still sync and push (Task 8 test `TestRunMissingRepoWithoutCreate`).
3. An API error from any of the three libraries → the error string forgemirror logs never contains the token (tests `TestErrorDoesNotLeakToken` in Tasks 3, 4 and 5).
4. An API answering with a redirect to another host → request is refused, token never sent there (Task 2 test `TestRefusesCrossHostRedirect`).
5. A brand-new empty target repo → create is called without a default branch, default branch is set after the push; push failure skips settings/labels for that target (Task 8 test `TestRunOrderCreatePushSettingsLabels`).

---

### Task 1: Forge-neutral model, address parsing, settings resolution and diffs

**Files:**
- Create: `internal/forge/forge.go`, `internal/forge/resolve.go`, `internal/forge/address.go`
- Test: `internal/forge/resolve_test.go`, `internal/forge/address_test.go`

**Interfaces:**
- Produces (package `forge`):
  - `var ErrNotFound = errors.New("not found")`
  - `type Label struct { Name, Color, Description string }` — `Color` is canonical: lower-case hex, no `#`.
  - `func NormalizeColor(c string) string` — strips `#`, lower-cases.
  - `type Repo struct { Description, Website string; Topics []string; DefaultBranch string; Private, HasIssues, HasWiki, HasPullRequests bool }`
  - `type Settings struct` with koanf tags: `Description *string "description"`, `Website *string "website"`, `Topics *[]string "topics"`, `DefaultBranch *string "default_branch"`, `Private *bool "private"`, `HasIssues *bool "has_issues"`, `HasWiki *bool "has_wiki"`, `HasPullRequests *bool "has_pull_requests"`, `Labels *bool "labels"`.
  - `func (s Settings) Validate() error` — error if `Private != nil && !*Private` (message contains `"private: only true is allowed"`).
  - `func (s Settings) IsZero() bool`
  - `type Capabilities struct { Website, PullRequests bool }`
  - `func Resolve(src Repo, layers ...Settings) (repo Repo, syncLabels bool)` — applies layers in order; `syncLabels` defaults to true; topics lower-cased and de-duplicated, sorted; `Private` = `src.Private || any layer Private==true`.
  - `func Diff(current, desired Repo, caps Capabilities) Settings` — pointers set only for differing fields; Website ignored unless `caps.Website`, HasPullRequests ignored unless `caps.PullRequests`; topics compared as sorted sets; `Labels` always nil.
  - `func LabelChanges(current, desired []Label, prune bool) (create, update, remove []Label)` — matched by case-insensitive name; update when color (normalized) or description differ; `remove` holds current labels missing from desired, only when `prune`.
  - `type Address struct { APIURL, Namespace, Name string }`
  - `func ParseAddress(forgeType, apiURL, renderedURL string) (Address, error)` — namespace = path without last segment, name = last segment without `.git`; default API URLs: type `github` + host `github.com` → `https://api.github.com`; `github` other host → `https://<host>/api/v3`; `gitlab` → `https://<host>/api/v4`; `forgejo` → `https://<host>/api/v1`; non-empty `apiURL` wins (trailing `/` trimmed). Error when path has fewer than two segments or type unknown.
  - `type Source interface { Repo(ctx context.Context, owner, name string) (Repo, error); Labels(ctx context.Context, owner, name string) ([]Label, error); Repos(ctx context.Context) ([]repo.Repository, error) }`
  - `type Target interface { Get(ctx context.Context, a Address) (Repo, error); Create(ctx context.Context, a Address, r Repo) error; Update(ctx context.Context, a Address, s Settings) error; Labels(ctx context.Context, a Address) ([]Label, error); CreateLabel(ctx context.Context, a Address, l Label) error; UpdateLabel(ctx context.Context, a Address, l Label) error; DeleteLabel(ctx context.Context, a Address, name string) error; Delete(ctx context.Context, a Address) error; Capabilities() Capabilities }` — `Get` returns an error wrapping `ErrNotFound` for a missing repo; `Create` must not set a default branch.
  - Type constants: `const (TypeGitHub = "github"; TypeGitLab = "gitlab"; TypeForgejo = "forgejo")`.

- [ ] **Step 1: Write failing tests**
  - `TestResolveVisibility`: cases — private src, no layers → Private true; private src, layer `Private:&false` is impossible via Validate but Resolve still returns true; public src, layer `Private:&true` → true; public src, no layers → false.
  - `TestResolveLayersLastWins`: src `HasIssues:true`, layers `{HasIssues:&false}`, `{HasIssues:&true}` → true; src description "a", layer description "b" → "b"; untouched fields keep src values.
  - `TestResolveTopicsNormalized`: src topics `["Go","go","Mirror"]` → `["go","mirror"]`.
  - `TestResolveSyncLabels`: default true; layer `Labels:&false` → false.
  - `TestDiff`: equal repos → `IsZero()`; differing website with `caps.Website=false` → Website nil; topics `["b","a"]` vs `["a","b"]` → Topics nil; differing default branch → `*DefaultBranch == "main"`.
  - `TestLabelChanges`: current `{bug, "#D73A4A", "x"}`, desired `{Bug, "d73a4a", "x"}` → no changes; desired new label → in create; color change → in update; extra current label → not in remove with `prune=false`, in remove with `prune=true`.
  - `TestSettingsValidate`: `Private:&false` → error containing `private: only true is allowed`; `Private:&true` → nil.
  - `TestParseAddress`: `("github","","https://github.com/acme/app.git")` → `{https://api.github.com, acme, app}`; `("github","","https://ghe.example/acme/app.git")` → APIURL `https://ghe.example/api/v3`; `("gitlab","","https://gitlab.com/group/sub/app.git")` → `{https://gitlab.com/api/v4, group/sub, app}`; `("forgejo","https://f.example/api/v1/","https://f.example/a/b.git")` → APIURL `https://f.example/api/v1`; `("github","","https://github.com/app.git")` → error; `("bitbucket",…)` → error.

- [ ] **Step 2: Run `mise run test -- ./internal/forge/...`** — expected FAIL (package missing).
- [ ] **Step 3: Implement** the types and functions above.
- [ ] **Step 4: Run `mise run test -- ./internal/forge/...`** — expected PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(forge): :sparkles: add forge-neutral repository model and settings resolution`.

### Task 2: Hardened shared HTTP client

**Files:**
- Create: `internal/forge/httpclient/httpclient.go`
- Test: `internal/forge/httpclient/httpclient_test.go`

**Interfaces:**
- Produces: `func New(timeout time.Duration) *http.Client` — `CheckRedirect` returns an error when `req.URL.Host` differs from `via[0].URL.Host` (why: libraries may set auth headers Go does not strip on cross-host redirects, e.g. GitLab's `PRIVATE-TOKEN`), and after 10 redirects. Used by every library client.

- [ ] **Step 1: Write failing tests:** `TestRefusesCrossHostRedirect` (server A 302 → server B; request errors, B never hit, a header `PRIVATE-TOKEN: s3cret` set on the original request never reaches B), `TestFollowsSameHostRedirect` (302 to another path on A → 200), `TestTimeout` (handler sleeps longer than a 50 ms timeout → error).
- [ ] **Step 2: Run `mise run test -- ./internal/forge/httpclient/...`** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(forge): :sparkles: add hardened HTTP client for forge APIs`.

### Task 3: Forgejo adapter (source and target) over forgejo-sdk

**Files:**
- Create: `internal/forge/forgejo/forgejo.go`
- Test: `internal/forge/forgejo/forgejo_test.go`
- Modify: `go.mod`, `go.sum` (`codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v4@v4.0.0`)

**Interfaces:**
- Consumes: Task 1 model/interfaces, Task 2 `httpclient.New`.
- Produces: `func New(ctx context.Context, httpClient *http.Client, baseURL string, token config.Secret) (*Client, error)` — `baseURL` is the Forgejo root URL (`source.base_url` or the target's host), not the `/api/v1` URL; the SDK adds the API path. `*Client` implements `forge.Source` and `forge.Target`. `Capabilities()` → `{Website: true, PullRequests: true}`.
- Behaviour to map (verify each SDK method in the module cache): get repo (description, website, private, default branch, has issues/wiki/pull requests) and its topics; labels list paged with page size 50 until a short page; repository search paged (map full name, name, owner login, clone URL, private, fork, topics into `repo.Repository`); create in an organisation when the owner is an organisation, else for the authenticated user, never passing a default branch; edit repo with only the set fields; replace topics; create/edit/delete label (edit and delete look up the label id by case-insensitive name); delete repo. 404 from the SDK → error wrapping `forge.ErrNotFound`.
- If the SDK queries the server version on construction, the test fake serves `GET /api/v1/version` with `{"version":"15.0.0+gitea-1.22.0"}`.

- [ ] **Step 1: Write failing tests** against an `httptest` fake of the Forgejo API: `TestRepo` (all fields + topics), `TestLabelsPaged` (50 + 3 → 53, colors normalized), `TestRepos` (two search pages), `TestGetNotFound`, `TestCreateOrgVsUser` (org → `POST /api/v1/orgs/acme/repos`; `GET /api/v1/orgs/bob` 404 → `POST /api/v1/user/repos`; body has `private`, no `default_branch`), `TestUpdateOnlySetFields` (`Settings{HasIssues:&false}` → PATCH body has `has_issues:false` and no other repo field; topics-only → only the topics PUT), `TestLabelCreateUpdateDelete` (id looked up by name), `TestDelete`, `TestErrorDoesNotLeakToken` (fake returns 500 with body `{"message":"boom"}` → `err.Error()` and `fmt.Sprintf("%+v", err)` contain neither the token nor `Authorization`; wrap library errors with method + path context, never the request).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(forge): :sparkles: add Forgejo adapter`.

### Task 4: GitHub adapter over go-github

**Files:**
- Create: `internal/forge/github/github.go`
- Test: `internal/forge/github/github_test.go`
- Modify: `go.mod`, `go.sum` (`github.com/google/go-github/v93@v93.0.0`)

**Interfaces:**
- Produces: `func New(httpClient *http.Client, apiURL string, token config.Secret) (*Client, error)` implementing `forge.Target`; `apiURL` `https://api.github.com` uses the library default, any other value goes through the library's enterprise/base-URL option. `Capabilities()` → `{Website: true, PullRequests: false}`.
- Behaviour: get repo (homepage → Website); create under an organisation when the owner's user type is `Organization`, else for the authenticated user; edit with only set fields (homepage, private, has issues, has wiki, default branch, description); replace all topics; labels listed with 100 per page following the library's next page; create/edit/delete label by name; delete repo. 404 → `forge.ErrNotFound`.

- [ ] **Step 1: Write failing tests:** `TestGet`, `TestGetNotFound`, `TestCreateOrgVsUser`, `TestUpdateOnlySetFields` (HasPullRequests set → not sent), `TestTopicsReplace`, `TestLabelsPaged` (two pages via `Link` header; colors without `#`), `TestLabelNameEscaped` (`good first issue` → path `/repos/o/r/labels/good%20first%20issue`), `TestDelete`, `TestErrorDoesNotLeakToken` (fake returns 500 with body `{"message":"boom"}` → `err.Error()` and `fmt.Sprintf("%+v", err)` contain neither the token nor `Authorization`; wrap library errors with method + path context, never the request).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(forge): :sparkles: add GitHub adapter`.

### Task 5: GitLab adapter over client-go

**Files:**
- Create: `internal/forge/gitlab/gitlab.go`
- Test: `internal/forge/gitlab/gitlab_test.go`
- Modify: `go.mod`, `go.sum` (`gitlab.com/gitlab-org/api/client-go/v3@v3.40.0`)

**Interfaces:**
- Produces: `func New(httpClient *http.Client, apiURL string, token config.Secret) (*Client, error)` implementing `forge.Target` (library base URL option set to `apiURL`; disable or bound the library's retries so a run cannot hang past `mirror.timeout`). `Capabilities()` → `{Website: false, PullRequests: true}`.
- Behaviour: the project id is `namespace/name`; get project (visibility ≠ public → Private; issues/wiki/merge-request access levels `enabled`/`private` → true, `disabled` → false; topics; default branch; description); create resolves the namespace id from the namespace path, sets name, path, visibility, access levels, description, topics; edit with only set fields; labels listed 100 per page following the library's next page; create label with color prefixed `#`; edit/delete label by name; delete project. 404 → `forge.ErrNotFound`.

- [ ] **Step 1: Write failing tests:** `TestGetMapsAccessLevels`, `TestGetNotFound`, `TestCreateResolvesNamespace` (`group/sub` → namespace lookup → `namespace_id: 42` in create body), `TestUpdateOnlySetFields` (Website set → nothing sent for it), `TestLabelsPaged` (`X-Next-Page`), `TestLabelColorHasHash`, `TestDelete`, `TestErrorDoesNotLeakToken` (fake returns 500 with body `{"message":"boom"}` → `err.Error()` and `fmt.Sprintf("%+v", err)` contain neither the token nor `Authorization`; wrap library errors with method + path context, never the request).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(forge): :sparkles: add GitLab adapter`.

### Task 6: Configuration: sync section, typed targets, overrides, allow_private

**Files:**
- Modify: `internal/config/config.go` (add `Sync`, defaults `sync.on_push: true`), `internal/config/target.go` (new fields, `Address`, `Layers`), `internal/config/validate.go`, `internal/filter/filter.go` (`AllowPrivate`)
- Modify: `docs/static/schema/v1/forgemirror.schema.json`, `forgemirror.example.yaml`
- Test: `internal/config/config_test.go`, `internal/config/target_test.go`, `internal/filter/filter_test.go`, `internal/config/schema_test.go` (existing drift tests must pass)

**Interfaces:**
- Consumes: `forge.Settings`, `forge.ParseAddress`, type constants.
- Produces:
  - `type Sync struct { OnPush bool "on_push"; Schedule time.Duration "schedule" }`, `Config.Sync Sync "sync"`.
  - `Target` new fields: `Type string "type"`, `APIURL string "api_url"`, `Create bool "create"`, `Delete bool "delete"`, `PruneLabels bool "prune_labels"`, `Settings forge.Settings "settings"`, `Overrides []Override "overrides"`.
  - `type Override struct { Repos string "repos"; Settings forge.Settings "settings" }` (compiled anchored regex kept unexported).
  - `func (t *Target) Address(r repo.Repository) (forge.Address, error)` — renders URL then `forge.ParseAddress(t.Type, t.APIURL, url)`.
  - `func (t *Target) Layers(fullName string) []forge.Settings` — `t.Settings` then each matching override's settings, in order.
  - `func (c *Config) Typed() bool` — any target with a type.
  - `filter.Rules.AllowPrivate string "allow_private"`: a private repo matching it passes `exclude_private`.
- Validation messages (keys exactly): `targets[i].type: must be one of github, gitlab, forgejo`; `targets[i].create: requires type` (same for `delete`, `prune_labels`, `settings`, `overrides`, `api_url`); `targets[i].settings: private: only true is allowed` (and `targets[i].overrides[j].settings: …`); `targets[i].overrides[j].repos: <regex error>` / `required`; `source.base_url: required when a target has a type or sync.schedule is set`; `source.token: required when a target has a type or sync.schedule is set`; `sync: on_push is false and no schedule is set`; `sync.schedule: must not be negative`; `webhook.secret: required` only when `sync.on_push` or any target `delete`; `filter.allow_private: <regex error>`.

- [ ] **Step 1: Write failing tests:**
  - config validation table cases for each message above (YAML snippets appended to `minimalYAML`).
  - `TestSyncDefaults`: no `sync` section → `OnPush == true`, `Schedule == 0`.
  - `TestWebhookSecretOptionalWhenScheduleOnly`: `sync: {on_push: false, schedule: 1h}`, source base_url + token, no webhook secret → valid.
  - `TestTargetAddressAndLayers`: typed github target with settings `has_issues: false` and overrides `acme/app` → `has_issues: true`, `acme/.*` → `private: true`; `Layers("acme/app")` returns three layers in order; `Layers("bob/x")` returns one; `Address` for `acme/app` → `{https://api.github.com, acme, app}`.
  - filter: `TestAllowPrivate`: `exclude_private: true, allow_private: "acme/notes"` → `acme/notes` private passes, `acme/other` private rejected with existing reason.
- [ ] **Step 2: Run `mise run test -- ./internal/config/... ./internal/filter/...`** — FAIL.
- [ ] **Step 3: Implement**; extend the JSON schema (`sync` object; target `type` enum `["github","gitlab","forgejo"]`, `api_url`, `create`, `delete`, `prune_labels`, `settings` (`$defs/settings`, `private` `const: true`), `overrides` array of `{repos, settings}`; `filter.allow_private`) and `forgemirror.example.yaml` (commented examples for every new key, defaults shown). The existing schema drift tests (`TestSchemaMatchesConfigTypes`, `TestSchemaAcceptsShippedConfigs`) must cover the new keys.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(config): :sparkles: add sync settings, typed targets and overrides`.

### Task 7: Remote ref listing for the scheduled skip check

**Files:**
- Modify: `internal/mirror/mirror.go` (interface), `internal/mirror/git.go`
- Test: `internal/mirror/git_test.go`, `internal/mirror/integration_test.go`

**Interfaces:**
- Produces: `Mirrorer.Refs(ctx context.Context, r Remote) (map[string]string, error)` — map ref name → object id for `refs/heads/*` and `refs/tags/*` (peeled `^{}` entries excluded), via `git ls-remote --heads --tags -- <url>` with the same credential environment, isolation and token scrubbing as clone. An empty remote returns an empty map, nil error. Missing remote returns an error.
- [ ] **Step 1: Write failing tests:** `TestRefs` (integration with local bare repo over file path as the existing integration tests do: one branch, one annotated tag → exactly `refs/heads/main` and `refs/tags/v1`), `TestRefsCredentialNotInArgs` (the fake git binary pattern used by existing tests records argv; token absent).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS. Update the dispatch test fake (`fakeMirrorer`) with a `Refs` method so the build stays green.
- [ ] **Step 5: `mise run check`, commit** `feat(mirror): :sparkles: list remote branches and tags`.

### Task 8: Dispatcher sync flow, delete events, metrics, wiring

**Files:**
- Modify: `internal/webhook/webhook.go` (Event kind only), `internal/dispatch/dispatch.go`, create `internal/dispatch/sync.go`, `internal/telemetry/telemetry.go`, `internal/app/app.go` (build clients)
- Test: `internal/dispatch/sync_test.go`, `internal/dispatch/dispatch_test.go`, `internal/telemetry/telemetry_test.go`, `internal/app/app_test.go`

**Interfaces:**
- Consumes: Tasks 1–7.
- Produces:
  - `webhook.Event` gains `Kind EventKind`; `type EventKind int; const (EventPush EventKind = iota; EventScheduled; EventDelete)`. Coalescing key stays `FullName`; a queued `EventDelete` replaces any queued event, and a queued push never replaces a queued delete.
  - `dispatch.Options` gains `Source forge.Source` (nil when no typed target) and `Forges map[string]forge.Target` (keyed by target name; only typed targets).
  - `dispatch.Observer` gains `SyncFinished(ctx context.Context, target, operation string, err error)`; operations `"create"`, `"settings"`, `"labels"`, `"delete"`.
  - telemetry counter `forgemirror_sync_operations_total` with attributes `target`, `operation`, `status` (`success`/`failure`).
  - `app` builds one shared `httpclient.New(30*time.Second)`, a Forgejo source client from `source.base_url` when `cfg.Typed()` or a schedule is set, and per typed target the client for its type with `APIURL` from `forge.ParseAddress` defaults (API URL is per repo-address but constant per target: compute from the target's rendered sample URL, or construct the client lazily per address — implementer's choice, documented in a comment).
- Run algorithm per event (push/scheduled):
  1. If any typed target: `src.Repo` + `src.Labels` (failure → all typed targets fail with that error; untyped targets still push).
  2. Per typed target, in parallel with others: `Address`; `Get`; on `ErrNotFound` and `Create` → `Create(addr, Resolve(...)-repo)` (SyncFinished create); on `ErrNotFound` and not `Create` → target fails `target repository does not exist and create is false`.
  3. Scheduled events only: `Refs` of source and every target; if every target equals the source, skip clone/push (log `already in sync`).
  4. Clone once, push to every target (existing behaviour).
  5. Per typed target whose push succeeded: `Diff(Get-result-or-created, resolved, caps)`; if not zero `Update` (SyncFinished settings); if `syncLabels`: `Labels`, `LabelChanges(current, desired, target.PruneLabels)`, create/update/delete each (SyncFinished labels, one per run with joined errors).
  - Delete events: for each typed target with `Delete: true`: `Delete(addr)`; `ErrNotFound` counts as success; no clone, no source API calls. Targets without `delete` untouched (log debug).
- [ ] **Step 1: Write failing tests** with a fake `forge.Source` and fake `forge.Target` recording calls in order:
  - `TestRunOrderCreatePushSettingsLabels`: missing repo, `create: true` → call order `Get, Create, (push), Update, Labels, CreateLabel`; the recorded Create argument has `DefaultBranch == ""` (the dispatcher clears it) and the Update carries `*DefaultBranch == "main"`.
  - `TestRunCreatesPrivateRepoPrivate`: source private, target settings empty → Create arg `Private == true`.
  - `TestRunMissingRepoWithoutCreate`: two typed targets, one missing with `create: false` → that target's error contains target name and `create is false`; the other target still pushed and synced; observer recorded failure for the first.
  - `TestRunPushFailureSkipsSettings`: push hook fails for target → no Update/Labels for it.
  - `TestRunUntypedTargetUnchanged`: only untyped targets → no source API calls, push as before.
  - `TestScheduledRunSkipsWhenInSync`: Refs equal → no clone; settings still synced.
  - `TestPruneLabels`: target `prune_labels: true`, extra label on target → `DeleteLabel`; `false` → no delete.
  - `TestDeleteEvent`: targets with `delete: true` get `Delete`; `delete: false` not; NotFound → success; no clone.
  - `TestQueuedDeleteWinsOverPush`: push running, queue push then delete → follow-up is delete; queue delete then push → still delete.
  - telemetry: `TestSyncFinishedCounts` asserts the Prometheus output contains `forgemirror_sync_operations_total{operation="create",status="success",target="github"} 1`.
- [ ] **Step 2: Run `mise run test -- ./internal/dispatch/... ./internal/telemetry/...`** — FAIL.
- [ ] **Step 3: Implement** (`sync.go` holds the per-target sync steps; `dispatch.go` only orchestrates).
- [ ] **Step 4: Run** — PASS; `mise run test -- ./internal/app/...` PASS (app test with a typed target pointing at an httptest Forgejo/GitHub fake: one push leads to create + push).
- [ ] **Step 5: `mise run check`, commit** `feat(dispatch): :sparkles: create repositories and sync settings and labels`.

### Task 9: Webhook: on_push switch and repository-deleted events

**Files:**
- Modify: `internal/webhook/webhook.go`, `internal/webhook/payload.go`, `internal/app/app.go`, `internal/server/server.go` (404 for `/webhook` when no secret)
- Test: `internal/webhook/webhook_test.go`, `internal/server/server_test.go`

**Interfaces:**
- Produces: `webhook.Options.IgnorePush bool` (true when `sync.on_push` is false); event header value `repository`; payload `{"action": "...", "repository": {full_name, name, owner.login, ...}}`.
- Behaviour: push with `IgnorePush` → `200`, outcome `ignored`, body `push events disabled (sync.on_push is false)`; repository `deleted` → validated (full_name required; clone URL check not applied), filter **not** applied (deletion of a now-excluded repo is still a source deletion), dispatched with `Kind: EventDelete`, `202 accepted` body `delete queued`; repository `created` or other actions → `200 ignored`. Server: when webhook secret empty, `/webhook` → 404.
- [ ] **Step 1: Write failing tests:** `TestPushIgnoredWhenDisabled`, `TestRepositoryDeletedDispatchesDelete` (asserts dispatched Kind and FullName), `TestRepositoryCreatedIgnored`, `TestRepositoryDeletedMissingFullName` (400), `TestWebhookDisabledWithoutSecret` (server 404).
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement**, wire `IgnorePush: !cfg.Sync.OnPush` in app. **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(webhook): :sparkles: handle repository deletions and allow disabling push events`.

### Task 10: Scheduler

**Files:**
- Create: `internal/schedule/schedule.go`
- Modify: `internal/app/app.go`, `internal/telemetry/telemetry.go`
- Test: `internal/schedule/schedule_test.go`, `internal/telemetry/telemetry_test.go`, `internal/app/app_test.go`

**Interfaces:**
- Consumes: `forge.Source.Repos`, `filter.Filter.Matches`, `webhook.Dispatcher`.
- Produces:
  - `type Options struct { Interval time.Duration; Source forge.Source; Filter interface{ Matches(repo.Repository) bool }; Dispatcher webhook.Dispatcher; Logger *slog.Logger; Observer Observer; Tick <-chan time.Time /* tests; nil = time.NewTicker(Interval) */ }`
  - `type Observer interface { ScheduledRunFinished(ctx context.Context, repos int, err error) }`
  - `func Run(ctx context.Context, opts Options)` — runs once immediately, then on every tick until ctx is done; per run: `Repos`, filter with `Matches`, dispatch `webhook.Event{Repository: r, Kind: webhook.EventScheduled, Delivery: "schedule-<RFC3339 time>"}` for each; `Repos` error → logged, observer gets error, loop continues.
  - telemetry counter `forgemirror_scheduled_runs_total{status}`.
  - app starts `schedule.Run` in a goroutine when `cfg.Sync.Schedule > 0`, stopped by the serve context before the dispatcher drains.
- [ ] **Step 1: Write failing tests:** `TestRunDispatchesFilteredRepos` (3 repos, filter rejects one → 2 dispatched with Kind Scheduled), `TestRunImmediatelyAndOnTick` (manual tick channel: 1 run at start, 2nd after tick), `TestRunListErrorContinues` (first `Repos` error, second ok → observer saw error then success), `TestRunStopsOnCancel`.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: `mise run check`, commit** `feat(schedule): :sparkles: sync repositories on a schedule`.

### Task 11: Documentation

**Files:**
- Modify: `docs/content/docs/configuration.md` (sections Sync, typed targets: type/api_url/create/delete/settings/overrides, precedence and visibility rule, allow_private, per-forge differences table), `docs/content/docs/security.md` (token rights per forge exactly as in the spec's "Token scopes" section, including the Forgejo source table: `read:repository`, `read:issue`, repository access "all", bot user recommendation; private stays private; delete and prune_labels opt-in), configuration "Editor support" section (schema stays v1, lists the new keys: sync, filter.allow_private, target type/api_url/create/delete/prune_labels/settings/overrides), `docs/content/docs/forgejo-webhook.md` (Repository events for deletion; schedule-only needs no webhook; new responses), `docs/content/docs/operations.md` (new metrics), `deploy/configmap.yaml` and `charts/forgemirror/README.md` (typed GitHub target example with `create: true`), `README.md` (features list: creates repos, syncs settings, schedule), `spec/advanced-use-cases.md` (add "Scheduled deletion detection" with why deferred).
- [ ] **Step 1:** Write the docs. Every config key in the docs must exist in the schema.
- [ ] **Step 2:** `mise run docs:check` — PASS; `mise run check` — PASS.
- [ ] **Step 3: Commit** `docs: :memo: document repository creation, settings sync, schedule and deletion`.

### Task 12: End-to-end test against a real Forgejo

**Files:**
- Create: `e2e/e2e_test.go` (build tag `e2e`, package `e2e_test`), `e2e/compose.yaml` (single Forgejo service, image `codeberg.org/forgejo/forgejo:15` pinned by digest, SQLite, `INSTALL_LOCK=true`, webhook `ALLOWED_HOST_LIST=*`), `mise-tasks/test/e2e`
- Modify: `.forgejo/workflows/ci.yaml` (job `e2e` on pull requests and pushes to main, after `verify`, calling `mise run test:e2e`), `docs/content/docs/development/` page that lists tasks (add `test:e2e`)

**Interfaces:**
- Consumes: the built binary (`mise run build` → `bin/forgemirror`), Docker.
- `mise run test:e2e`: starts compose, waits for `GET /api/v1/version`, creates an admin user with `forgejo admin user create` via `docker compose exec`, creates an access token with scopes `read:repository, read:issue, write:repository, write:issue, write:organization`, runs `go test -tags e2e ./e2e/...` with the URL and token in env, always tears compose down (`trap`).
- Test flow (`TestEndToEnd`, sequential subtests):
  1. Create orgs `src` and `dst`; create `src/app` (private), with labels `bug` (`#d73a4a`) and `docs`, topics `go`, description, has_wiki false.
  2. Write a config: source = Forgejo, `filter.exclude_private: true`, `allow_private: "src/app"`, one target `type: forgejo`, url `<forgejo>/dst/{{ .Name }}.git`, `create: true`, `delete: true`, `prune_labels: true`, settings `has_issues: false`; `sync.schedule: 2s`; start `bin/forgemirror` as a subprocess on a free port.
  3. `push`: add a repository webhook on `src/app` pointing at forgemirror (Forgejo reaches it via `host.docker.internal` or the compose network gateway — implementer verifies which works on the CI runner), push a commit and a tag to `src/app` → within 30 s `dst/app` exists, is private, has the branch and tag at the same commit ids, `has_issues` false, `has_wiki` false, description and topic equal, labels `bug` and `docs` with equal colors.
  4. `schedule`: on the source add label `ci` and delete label `docs` (no push) → within 10 s the target has `ci` and no `docs` (scheduled run + prune).
  5. `delete`: delete `src/app` with the repository webhook configured for repository events at org level (`src` org webhook with Repository events) → within 30 s `dst/app` returns 404.
- [ ] **Step 1:** Write compose, task and test. **Step 2:** `mise run test:e2e` locally — PASS. **Step 3:** `mise run check` — PASS (the `e2e` tag keeps the test out of the normal run). **Step 4: Commit** `test(e2e): :white_check_mark: add end-to-end test against a real Forgejo`.
