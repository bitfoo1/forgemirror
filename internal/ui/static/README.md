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

The tarball was checked against the npm registry's `dist.integrity`
(sha512). To update: download `https://registry.npmjs.org/htmx.org/-/htmx.org-<version>.tgz`,
verify it the same way, copy both files, and update this table and the
version in `internal/ui/ui_test.go`.

htmx is served from the binary, never from a CDN: the UI must work without
internet access and under a strict Content-Security-Policy.
