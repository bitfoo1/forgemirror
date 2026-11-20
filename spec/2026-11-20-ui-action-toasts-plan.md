# Transient UI Action Notifications — Plan

The user requested replacing persistent inline Refresh/Sync acknowledgements
with a top notification that drops down for a few seconds. Standing instruction:
implement without further approval questions; deliver a follow-up PR.

## Design

- One top-center toast, latest acknowledgement wins. Successful acknowledgements
  dismiss after five seconds, with a close button and pause while hovered or
  keyboard-focused. Errors remain until dismissed. Reduced motion disables the
  entrance movement. Do not steal focus or conflate accepted work with completion.
- Send action feedback using a JSON `HX-Trigger` event (`forgemirror:notice`),
  outside the polled/deferred repository fragment. This avoids replaying notices
  on polling and shows acknowledgements even while text selection defers DOM
  updates. Remove old inline action-notice markup/preservation logic only;
  persistent database/policy/operation warnings remain.
- Repository text names the repository. Bulk text names all repositories (all
  pending repositories for Sync all) and queued count; zero work is not called
  started. Existing spinners continue independently.
- Render notification text via DOM textContent, never HTML. Keep CSP unchanged,
  embed one small static script and use existing theme CSS. No dependency/build
  step or changes to remote webhook notifications.
- Native non-JS form redirects keep their existing behavior; toast feedback
  requires JavaScript. Generic HTTP/network errors must not falsely announce
  that work started.

## Tasks

- [x] RED/GREEN Go action response tests: structured event instead of inline
  notice, single/bulk/zero-work messages, success/error kind, safe encoding and
  unchanged native redirects.
- [x] RED/GREEN browser coverage: visible top-center drop-down, no inline
  acknowledgement, timeout/dismiss/hover/focus behavior, rapid replacement,
  deferred selection and polling independence, reduced motion and failure path.
- [x] Update Web UI docs, self-review and full `mise run check`: passed,
  89.6% internal coverage and all 15 browser tests, plus all other gates.
- [ ] Commit and open follow-up PR based on #56 (or main if merged).

## Ledger

- Worktree `.superpowers/worktrees/toasts`, branch `feat/ui-action-toasts`.
- Baseline `mise run test` passed before changes.
- RED Go tests found missing structured events and persistent inline notices.
  GREEN verifies single/bulk/zero-work messages, error kind, safe data encoding
  and unchanged native redirects.
- RED browser test found no toast. GREEN verifies all four actions, expiry,
  polling/deferred-update independence, replacement/timer reset, hover/focus,
  focus restoration, safe text, error persistence, mobile fit and reduced motion.
- Full `mise run check` passed: race tests, 89.6% internal coverage, 15 browser
  tests, lint/vet/format/tidy, vulnerability scan, deployment/chart validation
  and strict documentation/link checks.
- Self-review confirmed no raw HTML rendering, no CSP relaxation, no new
  dependencies, and no removal of persistent database/policy warnings.
  No independent reviewer tool is available.
