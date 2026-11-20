# Forge Branding Implementation Plan

> Execute inline with test-driven development. The approved badge UI was committed separately as `0b811c0`. Branding was then reviewed and approved for a separate logical commit. Do not push or release.

**Goal / approved design:** Use the correct GitHub, GitLab, and Codeberg names and official icons throughout the UI, including repository/change badges, overview tiles, target details, and history. Preserve configured target identifiers in actions, history keys, configuration, and logs, and preserve custom display names.

**Approaches:** Use centrally resolved display identities and locally vendored upstream icons. Do not fetch icons from a CDN at runtime, rewrite configuration IDs, or equate every Forgejo instance with Codeberg. Native select options show the proper names but cannot contain image icons.

**Architecture:** Resolve brands from explicit API type and exact public hostname (Codeberg is a Forgejo instance), falling back safely for custom/removed targets. Templates share one icon/name renderer. Official icons retain their monochrome/brand appearance rather than taking the badge's status color; Codeberg uses its official blue/light and white/dark variants. Status stays on the badge background/text. No new dependencies.

**Review focus:** custom aliases; self-hosted GitLab/GitHub; lookalike domains; unknown and removed targets; icons offline under CSP in both themes; shared diff attribution and action IDs unchanged.

## Tasks

- [x] Add failing renderer tests and browser coverage for proper names, icons, source identity, and preserved action IDs.
- [x] Vendor official upstream SVGs and licenses, documenting provenance and checksums.
- [x] Centralize display identity and use it in badges, overview, controls, details, and history.
- [x] Verify desktop/mobile, both themes, and offline icon loading; run the full check gate.
- [x] Restart the safe preview and leave branding changes uncommitted for review.

## Ledger

- User approved committing E, then requested proper forge names and official icons everywhere; continuing on `fix/compact-sync-notice`.
- Badge UI commit: `0b811c0`, preceded by a passing full `mise run check`.
- Official sources inspected: GitHub's primer/octicons, GitLab's gitlab-svgs, and Codeberg/Design. Codeberg guidelines require blue on light and white on dark; no custom tinting.
- No separate reviewer tool is installed; reviewed brand resolution, target ID preservation, asset provenance, CSP, theme variants, and template consumers inline.
- RED: renderer tests reproduced lowercase names, missing icons, and local SVG 404 responses.
- GREEN: `mise run check` passed: all Go tests, all 17 browser tests, 89.8% internal coverage, lint, docs, vulnerability, deployment and chart gates.
- Existing app integration expected the custom target identifier in a code element; retained that semantic wrapper while branding the text inside it.
- Actual preview restarted at `http://127.0.0.1:9099/`; verified proper names, grouped changes, locally loaded icons with external HTTPS blocked, and both themes at 1440px/390px.
- User approved committing branding and organizing branch history into logical units. Keep two cohesive commits: badge/grouped-diff UI first, forge naming/icons second. Each includes its tests and documentation; no fixup or intermediate commits need squashing.
- No push, merge, version change, tag, or release performed.
