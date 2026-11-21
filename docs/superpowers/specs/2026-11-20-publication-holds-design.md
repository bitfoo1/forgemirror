# Publication Holds: H+C

## Approved UI

A separate collapsible Publication controls card sits between overview and repositories. New hold contains a four-step Targets → Repositories → Duration → Review flow. Current holds (N) lists independent holds with Resume & sync actions. Switching tabs preserves drafts; successful creation selects Current holds and resets the wizard to step one. The collapsed header retains hold count and explicitly identifies an all-publication hold.

Targets and repositories are removable badge pickers. Selected items disappear from suggestions; removing badges restores suggestions. Repository names and `*`/`?` globs may be combined. Target aliases stay unchanged in submissions; forge branding is presentation only. Duration uses a matching single-select dropdown with Until resumed as its default, preset durations, and a custom Go duration. All targets and All repositories are explicit choices; empty selections never mean all.

## Durable semantics

A hold has its own stable ID, target scope, repository scope, reason, and optional expiry. All targets covers current and subsequently configured targets. Repository globs continue to cover future admitted matches. Exact names are admitted/known repositories; globs must initially match at least one admitted repository. All-repository holds may be created before discovery to protect future admissions. `*` and `?` match within owner/name components; character classes, escapes, exclusions, empty scopes, and malformed names are rejected. Scope size and text lengths are bounded: 64 selections per dimension, 256 bytes per reason, and 256 current holds. No new dependencies.

Hold creation is atomic and waits behind active remote mutations using the existing publication barrier. Persist before acknowledging; failed persistence retains previous state. Resume removes only the submitted hold ID, then requests fresh syncs only for matching currently admitted repository/target pairs, respecting remaining holds, approvals, and clone-URL checks. Expiry allows future enabled work; it does not schedule a sync.

Existing exact-repository/whole-target pause APIs remain compatible. Older persisted pauses migrate to stable legacy hold IDs. New independent holds never overwrite one another or get removed by the legacy scope-based resume action. All persisted scopes/IDs are validated before admission. Free-text reasons retain existing secret redaction.

## Rendering and safety

The server is authoritative for scope validation, active holds, and matching. The UI previews only admitted repository names; rendering and suggestion updates must not insert user text as HTML. No inline scripts/styles or new external assets; CSP stays strict. Native forms remain a usable no-JavaScript fallback. Invalid holds return a safe validation error, never a success toast. Same-origin protection and request size limits apply to new actions.

The controls pane has its own polled fragment. Its draft subtree is client-owned and preserved during reconciliation, while the current-holds subtree/count remains live. Picker input, badge state, tabs, open state, custom duration, and wizard progress survive polling. Empty/expired holds, restart migration, overlapping scopes, and unknown IDs must be covered by tests. The browser fixture uses real store/control logic with fake dispatch, not forge remotes.

## Approach

Keep the current publication barrier and pause storage, extending pauses with a scope and ID rather than introducing a second competing control store. Share bounded scope matching between store, controller, and UI. Separate current hold state from client draft state. This preserves current safety and avoids trying to implement multi-selection as a series of non-atomic legacy requests.

## Non-goals

No merge, version bump, tag, or release. Commit and PR creation follow explicit UI review and approval. No automatic publication on expiry, no scope exclusions or arbitrary target globs, and no removal of other holds when resuming one.
