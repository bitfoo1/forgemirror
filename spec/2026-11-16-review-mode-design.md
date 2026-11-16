# Review mode, change log and UI redesign — design

Status: agreed in conversation 2026-11-16; this document records it.

## Intent

The operator wants to review what forgemirror will publish before it reaches
a public forge. This matters most on the first run, when a new deployment
would otherwise push a large number of repositories at once. Today a run
decides and acts in one step, and the logs only say that a step succeeded
("forge operation succeeded"), not what it changed.

Three outcomes:

1. Review mode: automatic syncing can be switched off globally. Pushes and
   scheduled sweeps then only compute what a sync would change; the operator
   reviews the diff per repository in the UI and syncs per repository or all
   at once.
2. Change log: every applied sync logs exactly what it changed on each target
   (refs, repository creation, settings old → new, labels).
3. A modern status page in the Tokyo Night colour scheme, following the
   system light/dark preference with a manual override.

Diff and change log come from one change list ("plan"), computed by the same
code that applies it, so what the UI shows is what a sync does.

## Goals

- `sync.auto: false` puts the whole service into review mode.
- Per repository: show diff, refresh diff, sync. Globally: refresh all, sync
  all.
- The diff covers every effect of a sync on each target: repository creation,
  privatisation, refs created / updated / deleted, settings old → new, labels
  created / updated / deleted, repository deletion.
- Applied plans are logged in the same detail, in both modes.
- Light and dark theme from Tokyo Night Day and Night; system preference by
  default, override remembered per browser.

## Non-goals

- Applying exactly the reviewed diff. Sync applies the current state at click
  time (decided: option A). If the source changed after the review, the new
  changes are synced too. The UI shows when a diff was computed.
- Per-target or per-repository auto/review switches; the switch is global.
- Persisting plans across restarts.
- Authentication in forgemirror (unchanged, see the web UI design).
- Approving parts of a plan (e.g. settings but not refs).

## Anti-goals

- Anything reaching a target in review mode without a click in the UI.
  Webhooks, the schedule and startup must never apply a plan then.
- A diff that disagrees with what Sync does.
- Secrets in the diff, the logs or any UI response (unchanged rule).
- A JavaScript build step, npm, or runtime CDN assets (unchanged rule).
- A cross-site page being able to trigger Sync through the operator's browser.

## Approaches considered

1. **Plan/apply split in the dispatcher (chosen).** Each run first builds a
   plan with read-only calls: `git ls-remote` on source and target for refs
   (as `inSync` already does), the forge API for repository, settings
   (`forge.Diff`) and labels (`forge.LabelChanges`). Apply executes the plan.
   Refresh needs no clone, so refreshing hundreds of repositories on a first
   run is cheap. Cost: refactoring `prepare` / `syncTarget`, which decide and
   act in one step today.
2. **`git push --dry-run`.** Exact for refs, but every refresh needs a full
   clone, and settings and labels still need approach 1. Rejected: a first-run
   "refresh all" would clone everything.
3. **A separate planner package beside the dispatcher.** Leaves the
   dispatcher untouched but duplicates the decision logic, so diff and sync
   could drift apart. Rejected: violates the main anti-goal.

UI actions: the web UI design ruled out state-changing requests because of
CSRF. This design reverses that, using Go's `http.CrossOriginProtection`
(Fetch Metadata `Sec-Fetch-Site`, falling back to `Origin` vs `Host`)
instead of tokens: no session state, no templates carrying tokens, and
it rejects cross-site browser requests, which is the threat.

## Decisions

### Configuration

```yaml
sync:
  auto: true        # new; false = review mode
  on_push: true     # in review mode: a push refreshes the diff
  schedule: 0s      # in review mode: each sweep refreshes all diffs
```

- `auto` defaults to `true`, so existing deployments do not change. Not a
  breaking change; ships as a minor version.
- Validation when `auto: false`:
  - `ui.enabled` must be true (otherwise nothing could ever be synced).
  - `source.base_url` and `source.token` are required: review mode lists all
    repositories through the Forgejo API, like the schedule.
  - The rule "on_push is false and no schedule is set" does not apply; manual
    refresh alone is a valid setup.
- Env var `FORGEMIRROR_SYNC__AUTO`; JSON schema v1, configuration docs,
  `forgemirror.example.yaml`, Helm values/README follow.

### Plan model

Per repository a plan holds, per target:

- `create` (repository missing and `create: true`), or an error when missing
  and `create: false`;
- `privatise` (existing public repository, desired private) — applied before
  the push, as today;
- ref changes: create (ref, new id), update (ref, old id → new id), delete
  (ref, old id), from comparing `ls-remote` maps of source and target; a
  missing target repository means every source ref is a create;
- settings: each changed field with old and new value (from `forge.Diff`
  plus the current values), computed against the current repository; for a
  repository that would be created, against the values `Create` sets;
- labels: create / update (old → new colour and description) / delete;
- `delete` for a deleted source repository on targets with `delete: true`;
- per-target error when the plan could not be computed (unreachable API,
  missing client), shown instead of the diff.

Untyped targets only have ref changes. When `ls-remote` on an untyped target
fails (e.g. a forge that creates repositories on first push), the plan marks
its refs as unknown with a warning and apply pushes everything, as today;
it does not block the sync. A plan with no changes is "in sync".

Every push event now runs `ls-remote` on source and targets before cloning
(today only scheduled runs do). That costs one cheap round trip per remote
and lets unchanged targets skip the push.
Topics the target rejects are dropped from the plan as today (warning logged).

Plan data is a forge-neutral type (in `internal/forge` or a new
`internal/plan` package — builder's call) so dispatch, status and UI share it.

### Dispatcher

- Event kinds gain the intent: plan only, or plan and apply. In auto mode
  every event is plan-and-apply (today's behaviour plus the change log). In
  review mode push, scheduled and delete events become plan-only; only UI
  actions submit plan-and-apply.
- Apply re-plans first (option A) and then executes that fresh plan: create,
  privatise, clone + push (skipped when the plan has no ref changes, which
  replaces today's `inSync` check), settings, labels; or delete.
- Coalescing: a queued plan-and-apply must not be replaced by a later
  plan-only event; the merged queued event is plan-and-apply if either was.
  A queued delete keeps its current precedence.
- The per-repository single-run rule, both semaphores and the shutdown
  behaviour stay as they are. Plan steps take API / git slots like the
  steps they replace.
- Observer gains a plan event (repository, plan, computed-at time, error);
  the status store keeps the latest plan per repository. Telemetry ignores
  it except, at most, a counter of plan runs (builder's call; no repository
  label).
- `git push` gains `--porcelain` so the log can confirm which refs were
  actually updated; its output is parsed, not shown raw.

### Review mode listing and startup

- The UI lists every repository the filter admits, from the source listing
  the scheduler uses, plus repositories known from events and deletions.
- At startup in review mode, one plan-only sweep over all repositories runs
  (the first scheduled sweep does this already when a schedule is set), so
  the page shows diffs after a restart without a click.

### Change log

- After apply, one INFO record per target and change kind, with structured
  attributes, e.g. `msg="settings changed" target=github field=description
  old="…" new="…"`, `msg="labels changed" target=github created=[bug]
  updated=[…] deleted=[…]`, `msg="repository created"`, `msg="refs pushed"
  created=2 updated=1 deleted=0`.
- Individual refs at INFO up to a limit (builder's call, around 20 per
  target), the rest summarised; every ref at DEBUG. A first push of a
  repository with hundreds of tags must not flood the log.
- Plan-only runs log one INFO summary per repository ("out of sync: 3
  changes on 2 targets" / "in sync") and the details at DEBUG.
- Values are logged as they come from the source repository (descriptions,
  topics, label names); no secret can appear in a plan. Errors stay redacted
  at their source as today.

### Web UI actions

- Routes (relative URLs, POST only):
  - `POST actions/refresh` — plan all repositories, or the one named in the
    form field `repo`
  - `POST actions/sync` — plan-and-apply every repository whose latest plan
    has changes, or the one named in `repo`
  - One path depth for all actions keeps the no-JS redirect (`303 ../`)
    correct under any proxy path prefix.
- Unknown repositories (not admitted by the filter) get 404, so the UI cannot
  be used to mirror arbitrary repositories.
- `http.CrossOriginProtection` wraps the action routes; GET/HEAD stay as
  today, other methods 405. Actions answer with the updated fragment (htmx
  swap) and work without JavaScript through plain forms with a redirect
  (303) back to the page.
- "Sync all" asks for confirmation (`hx-confirm`; no inline script needed).
- Actions are logged at INFO (`msg="sync requested from UI" repository=…`);
  forgemirror cannot know the user, the proxy can.
- Actions are available in both modes; in auto mode Sync is "sync now".
  In auto mode without source API credentials (push-only, untyped targets)
  there is no repository listing: the page shows only repositories known
  from events, and Refresh all / Sync all cover those.
- The UI design document's non-goal "Actions from the UI" and the web UI
  docs (security section) are updated to point here.
- Docs note that the proxy must preserve `Host` and `Origin`, otherwise the
  fallback check rejects requests from old browsers without Fetch Metadata.

### Page design

- Header: name, version, mode badge (Auto / Review), theme toggle, and in
  the repository section the Refresh all / Sync all buttons.
- Repository list: one row per repository with state badge — In sync, Out of
  sync (n changes), Not checked yet, Planning, Syncing, Failed — plus "checked
  3m ago" and Diff / Refresh / Sync buttons. Diff expands inline
  (`<details>`, no JS) and shows the plan per target in a compact
  `+ / ~ / −` notation, old → new for settings, short object ids for refs.
- Existing sections (overview, last results, logs, configuration) stay, with
  the new styling. The log view shows the new structured change records
  readably.
- Theme: Tokyo Night Night (dark) and Day (light) as CSS custom properties.
  Default follows `prefers-color-scheme`; a toggle cycles system → light →
  dark, stored in `localStorage` and applied via `data-theme` on `<html>`
  by `static/theme.js`. A wrong-theme flash on load is acceptable (loading
  the script in `<head>` without `defer` keeps it minimal). Without
  JavaScript the system preference applies.
- Palette values are taken from the upstream project
  (folke/tokyonight.nvim, extras), recorded next to the CSS with the source
  version, like htmx. Status colours: green ok, red failed, yellow warn/out of
  sync, blue accent, comment colour for muted text.
- CSP stays strict: no inline script or style.

## Left to the builder

- Exact plan types and package; how settings old values are represented.
- Fragment granularity and htmx wiring; polling interval stays 5s.
- Ref display limits, sort order of repositories (suggested: out of sync
  first, then failed, then name).
- Memory bound for stored plans (cap stored refs per target, keep the count).
- Test split; fakes for `Refs`, forge API and porcelain output.
