# Vendored assets

| File               | Source                                              | SHA-256                                                            |
|--------------------|-----------------------------------------------------|--------------------------------------------------------------------|
| `htmx.min.js`      | htmx.org 2.0.11, npm tarball `dist/htmx.min.js`     | `d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717` |
| `htmx.LICENSE`     | htmx.org 2.0.11, npm tarball `LICENSE` (0BSD)       |                                                                    |

## Colours

`style.css` uses the Tokyo Night palette from
[folke/tokyonight.nvim](https://github.com/folke/tokyonight.nvim) v4.14.1
(Apache-2.0), files `extras/lua/tokyonight_day.lua` (light) and
`extras/lua/tokyonight_night.lua` (dark). Each value in `style.css` names the
palette entry it comes from; only the colours are used, no files are copied.
To update, compare those entries in the new release.

Deviations for WCAG 2.2 AA: text colours that miss 4.5:1 on any background
(`bg_dark`, `bg`, `bg_highlight`) are darkened (Day) or lightened (Night
`dark5`) at constant hue until they pass; `style.css` keeps the upstream value
next to each one. `--border-strong` is an extra token derived from
`fg_gutter` that gives controls the 3:1 non-text contrast `fg_gutter` lacks.

The tarball was checked against the npm registry's `dist.integrity`
(sha512). To update: download `https://registry.npmjs.org/htmx.org/-/htmx.org-<version>.tgz`,
verify it the same way, copy both files, and update this table and the
version in `internal/ui/ui_test.go`.

htmx is served from the binary, never from a CDN: the UI must work without
internet access and under a strict Content-Security-Policy.

## Official forge icons

All SVGs below are unmodified upstream assets, embedded and served locally.
The monochrome GitHub/GitLab glyphs use black on light backgrounds and white
on dark backgrounds, independently of badge status colors. Codeberg uses its
official blue and white variants; do not tint it with the status color.
Names and marks remain trademarks of their respective owners; their presence
identifies destinations, not endorsement.

| File | Official source | SHA-256 |
|------|-----------------|---------|
| `icons/github.svg` | [GitHub primer/octicons, mark-github-16](https://raw.githubusercontent.com/primer/octicons/main/icons/mark-github-16.svg) | `7421820090b50ac79d7c2bf7c951a433d7098a8a9c474809207bdd45f62d9d46` |
| `icons/gitlab.svg` | [GitLab gitlab-svgs, tanuki](https://gitlab.com/gitlab-org/gitlab-svgs/-/raw/main/sprite_icons/tanuki.svg) | `4a35444184ec72578fdfda58c3f17449d74091855fe91d3e0c58b52947cd20ed` |
| `icons/codeberg.svg` | [Codeberg/Design, blue icon](https://codeberg.org/Codeberg/Design/raw/commit/9f8c6992542b9c8f8f59cd3da19f095d8fec43ef/logo/icon/svg/codeberg-logo_icon_blue.svg) | `62ef326a0fdfdfd4a5f54fb96d8ecf999b88adb470ddfd8b8e87a5069cb01fbf` |
| `icons/codeberg-white.svg` | [Codeberg/Design, white icon](https://codeberg.org/Codeberg/Design/raw/commit/9f8c6992542b9c8f8f59cd3da19f095d8fec43ef/logo/icon/svg/codeberg-logo_icon_white.svg) | `80d07ed966e2233089b11d3cbc17f0771b6f5b63ab3f489cdc61be00e7a5f7c5` |

Upstream licenses are retained as `icons/github.LICENSE` (MIT),
`icons/gitlab.LICENSE` (MIT), and `icons/codeberg.LICENSE` (CC0 notice).
Codeberg artwork is credited to Robert Martinez in its SVG metadata.
See the [Codeberg guidelines](https://codeberg.org/Codeberg/Design) when updating.

To update: download from the official repositories, inspect SVGs for active
content/external references, retain licenses, and update the checksums above.
Never add a runtime dependency on a remote icon service.

## Icon and mascot

`favicon-16x16.png`, `favicon-32x32.png`, `apple-touch-icon.png`, `mark.png`
and `mascot.webp` are project artwork, derived from the same AI-generated
masters as the docs site (`docs/static/`, `docs/assets/images/`). Keep both
in step when the artwork changes. The icons are PNG rather than `.ico`
because Go's MIME table has no `.ico` entry; see the comment in
`templates/ui.html`. `mascot.webp` stays small (280×320) because every file
here is embedded in the binary.
