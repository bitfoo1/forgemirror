// Reconcile server-rendered fragments into the page instead of replacing them,
// so focus, selection, scroll positions and open <details> survive polling.

const LIVE_TARGETS = /^(repos|overview|logs|controls)$/;
const pending = new Map();
const accepted = new WeakMap();
let sequence = 0;

const keyOf = (node) => (node.nodeType === Node.ELEMENT_NODE ? node.id || node.getAttribute("data-live-key") || "" : "");
const compatible = (a, b) => a.nodeType === b.nodeType && a.nodeName === b.nodeName && keyOf(a) === keyOf(b);

function reconcile(parent, incoming) {
  const old = Array.from(parent.childNodes);
  const used = new Set();
  const keyed = new Map();
  for (const node of old) if (keyOf(node)) keyed.set(keyOf(node), node);
  let cursor = parent.firstChild;
  Array.from(incoming.childNodes).forEach((next, index) => {
    let node = keyOf(next) ? keyed.get(keyOf(next)) : old[index];
    if (!node || used.has(node) || !compatible(node, next)) node = null;
    if (node) {
      if (node !== cursor) parent.insertBefore(node, cursor);
      update(node, next);
    } else {
      node = next.cloneNode(true);
      parent.insertBefore(node, cursor);
    }
    used.add(node);
    cursor = node.nextSibling;
  });
  for (const node of old) if (!used.has(node) && node.parentNode === parent) node.remove();
}

function update(node, next) {
  if (node.isEqualNode(next)) return;
  if (node.nodeType !== Node.ELEMENT_NODE) {
    if (node.nodeValue !== next.nodeValue) node.nodeValue = next.nodeValue;
    return;
  }
  // The hold wizard owns its draft subtree; current hold rows remain live.
  if (node.hasAttribute("data-live-preserve")) return;
  // A deferred poll can reconcile a form while its action request is pending.
  const requesting = node.classList.contains("htmx-request");
  // The user owns a <details> element's open state, not the server.
  const owned = (name) => name === "open" && node.tagName === "DETAILS";
  for (const { name } of Array.from(node.attributes)) {
    if (!owned(name) && !next.hasAttribute(name)) node.removeAttribute(name);
  }
  for (const { name, value } of Array.from(next.attributes)) {
    if (!owned(name) && node.getAttribute(name) !== value) node.setAttribute(name, value);
  }
  if (requesting) {
    node.classList.add("htmx-request");
    if (node.matches("form.run-action")) node.setAttribute("aria-busy", "true");
  }
  reconcile(node, next);
}

// Updates wait while the user selects text in, or types into, the target.
function interacting(target) {
  const selection = window.getSelection();
  if (selection && !selection.isCollapsed) {
    for (let i = 0; i < selection.rangeCount; i++) {
      if (selection.getRangeAt(i).intersectsNode(target)) return true;
    }
  }
  const active = document.activeElement;
  if (target.id === "controls" && active?.closest("[data-live-preserve]")) return false;
  return Boolean(active && target.contains(active) &&
    active.matches("input:not([type=hidden]), textarea, select, [contenteditable=true]"));
}

function apply(target, html) {
  if (!target.isConnected || interacting(target)) return false;
  const fragment = document.createElement("template");
  fragment.innerHTML = html;
  const focus = document.activeElement;
  // The logs container is itself a scroll box, unlike the repositories.
  const scrollers = [...target.querySelectorAll(".scroll"), target]
    .map((node) => ({ node, top: node.scrollTop, left: node.scrollLeft }));
  reconcile(target, fragment.content);
  if (focus?.isConnected && document.activeElement !== focus) focus.focus({ preventScroll: true });
  for (const { node, top, left } of scrollers) {
    if (node.isConnected) {
      node.scrollTop = top;
      node.scrollLeft = left;
    }
  }
  window.htmx?.process(target);
  target.dispatchEvent(new CustomEvent("forgemirror:updated", { bubbles: true }));
  return true;
}

function flush() {
  for (const [target, html] of pending) {
    if (!target.isConnected || apply(target, html)) pending.delete(target);
  }
}

const isRunAction = (node) => node?.matches?.("form.run-action");

document.addEventListener("htmx:beforeRequest", (event) => {
  event.detail.xhr.forgemirrorSequence = ++sequence;
  if (isRunAction(event.detail.elt)) event.detail.elt.setAttribute("aria-busy", "true");
});
document.addEventListener("htmx:afterRequest", (event) => {
  if (isRunAction(event.detail.elt)) event.detail.elt.setAttribute("aria-busy", "false");
});
document.addEventListener("htmx:beforeSwap", (event) => {
  const { target } = event.detail;
  if (!target || !LIVE_TARGETS.test(target.id) || !event.detail.shouldSwap) return;
  event.detail.shouldSwap = false;
  // Responses can arrive out of order; an older one never replaces a newer one.
  const seq = event.detail.xhr.forgemirrorSequence || ++sequence;
  if (seq < (accepted.get(target) || 0)) return;
  accepted.set(target, seq);
  const html = event.detail.serverResponse;
  if (apply(target, html)) pending.delete(target);
  else pending.set(target, html);
});
document.addEventListener("selectionchange", flush);
document.addEventListener("focusout", () => window.setTimeout(flush, 0));
