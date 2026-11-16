# Review mode, change log and UI redesign — implementation plan

**Goal:** plan/apply split with a detailed change log, `sync.auto: false`
review mode driven from the UI, and a Tokyo Night redesign of the UI.

**Spec:** `spec/2026-11-16-review-mode-design.md` (binding).

**Execution:** native, task by task; `mise run check` green before every
commit; one review of the whole branch at the end.

## Global constraints

- `sync.auto` defaults to `true`; review mode requires `ui.enabled` and
  source API credentials; manual-only (no on_push, no schedule) is valid
  in review mode.
- Nothing is written to a target in review mode unless the run was
  requested with Sync from the UI.
- Sync re-plans and applies the current state (option A).
- No secret in plans, logs or UI responses; CSP stays strict (no inline
  script/style); no JS build, no CDN.
- Tokyo Night v4.14.1 (folke/tokyonight.nvim, `extras/lua`): Night for dark,
  Day for light.
- Commits: conventional + gitmoji, `feat` (no breaking change).

## Decisions taken while planning

- Event kinds gain `EventRefresh` (plan only, always) and `EventSync` (plan
  and apply, always). Push/scheduled/delete apply only when `auto`.
- The dispatcher remembers the last `repo.Repository` per name (`Known`), so
  UI actions by name have a clone URL; names it never saw are 404. Known
  repositories passed the filter on the way in.
- Deletion is planned from a delete event or from the source API answering
  404 for the repository (so a Sync after a reviewed delete event deletes).
- Settings and labels are re-diffed right before they are applied (as
  today, needed after create because forges add defaults); refs come from
  `git push --porcelain`. Logs record what was applied.
- UI action routes are `POST actions/refresh` and `POST actions/sync` with an
  optional form field `repo`; a single path depth keeps the no-JS redirect
  (`303 ../`) correct under any proxy prefix.
- `<details>` open state survives polling via `static/app.js` (htmx swap
  events); `app.js` also owns the theme toggle.

## Tasks

1. **Plan model** — `internal/plan`: `Repo{Name, At, Apply, SourceDeleted,
   Error, Targets []Target}`, `Target{Name, Create, Privatise, Delete,
   RefsUnknown, Refs []RefChange, Settings []FieldChange, Labels
   []LabelChange, Warnings []string, Error}`, `RefChange{Ref, Old, New}`,
   `FieldChange{Field, Old, New}`, `LabelChange{Name, Old, New *forge.Label}`,
   `Changes()` on Repo/Target; `RefChanges(src, dst map[string]string)`
   (sorted), `SettingsChanges(current forge.Repo, diff forge.Settings)`,
   `LabelChanges(create, update, remove, current)`. Tests: table-driven per
   helper, change counting, ordering.
2. **Config** — `Sync.Auto` (default true), validation rules, source client
   also when `!Auto`, JSON schema, docs/configuration, example config.
   Tests: defaults, each rule, env override, schema.
3. **git porcelain** — `Workspace.Push` returns `[]mirror.RefUpdate{Ref,
   Kind, Summary}`; drop `--quiet`, add `--porcelain`; parse `*`, ` `, `+`,
   `-`, `=` lines; skip `=`. Tests: parser table, integration test with a
   real bare repo (created/updated/forced/deleted).
4. **Dispatcher plan/apply** — `Options.Auto`; `Observer.RunStarted(ctx,
   repo, apply bool)`, `Observer.PlanFinished(ctx, plan.Repo)`; plan step
   (source refs/state, per target ls-remote, Get, desired, diffs; deletion
   plan); apply step (create, privatise, clone+push only for targets with
   ref changes or unknown refs, settings, labels, delete); change log
   records; coalescing keeps apply; `Known`/`KnownNames`; telemetry and
   status adapt. Tests: plan-only writes nothing, apply order, push skipped
   when refs equal, refs unknown pushes, deletion via 404, coalescing,
   change log records.
5. **Status store** — keeps the latest plan per repository and whether the
   running run applies; forgets repositories whose source is deleted and
   whose plan has no changes. Snapshot exposes both. Tests.
6. **Actions** — `internal/control.Controller` with `Refresh(ctx, name)`,
   `Sync(ctx, name)`, `RefreshAll(ctx) (int, error)`, `SyncAll(ctx) (int,
   error)`, `ErrUnknownRepository`; lists via source + filter when a source
   client exists, otherwise known names; SyncAll covers repositories whose
   latest plan has changes. App: startup refresh sweep in review mode
   without schedule. Tests with fakes.
7. **UI** — POST action routes behind `http.CrossOriginProtection`, htmx
   fragment responses, 303 without JS, 404 unknown repo; repository view
   model (state, change count, sort), diff rendering, mode badge, Tokyo
   Night CSS with tokens, `app.js` (theme toggle, details preservation),
   palette note. Tests: actions (htmx and form), cross-origin rejection,
   405s, states, diff content, no secrets.
8. **Docs** — web UI page (review mode, actions, proxy Host/Origin note),
   security page, web UI design non-goal pointer, Helm/README where config
   keys are listed.
