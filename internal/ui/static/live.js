// Reconcile server-rendered fragments without replacing the DOM people use.
(function () {
  "use strict";
  var pending = new Map();
  var accepted = new Map();
  var sequence = 0;

  function key(node) {
    return node.nodeType === 1 ? (node.id || node.getAttribute("data-live-key") || "") : "";
  }
  function compatible(a, b) {
    return a.nodeType === b.nodeType && a.nodeName === b.nodeName && key(a) === key(b);
  }
  function reconcile(parent, incoming) {
    var old = Array.from(parent.childNodes);
    var used = new Set();
    var keyed = new Map();
    old.forEach(function (node) { if (key(node)) { keyed.set(key(node), node); } });
    var cursor = parent.firstChild;
    Array.from(incoming.childNodes).forEach(function (next, index) {
      var node = key(next) ? keyed.get(key(next)) : old[index];
      if (!node || used.has(node) || !compatible(node, next)) { node = null; }
      if (!node) {
        node = next.cloneNode(true);
        parent.insertBefore(node, cursor);
      } else {
        if (node !== cursor) { parent.insertBefore(node, cursor); }
        update(node, next);
      }
      used.add(node);
      cursor = node.nextSibling;
    });
    old.forEach(function (node) { if (!used.has(node) && node.parentNode === parent) { parent.removeChild(node); } });
  }
  function update(node, next) {
    if (node.isEqualNode(next)) { return; }
    if (node.nodeType !== 1) {
      if (node.nodeValue !== next.nodeValue) { node.nodeValue = next.nodeValue; }
      return;
    }
    // A deferred poll can reconcile a form while its action request is pending.
    var requesting = node.classList.contains('htmx-request');
    Array.from(node.attributes).forEach(function (attr) {
      if (attr.name === "open" && node.tagName === "DETAILS") { return; }
      if (!next.hasAttribute(attr.name)) { node.removeAttribute(attr.name); }
    });
    Array.from(next.attributes).forEach(function (attr) {
      if (attr.name === "open" && node.tagName === "DETAILS") { return; }
      if (node.getAttribute(attr.name) !== attr.value) { node.setAttribute(attr.name, attr.value); }
    });
    if (requesting) {
      node.classList.add('htmx-request');
      if (node.matches('form.run-action')) { node.setAttribute('aria-busy', 'true'); }
    }
    reconcile(node, next);
  }
  function interacting(target) {
    var selection = window.getSelection();
    if (selection && !selection.isCollapsed && selection.rangeCount) {
      for (var i = 0; i < selection.rangeCount; i++) {
        if (selection.getRangeAt(i).intersectsNode(target)) { return true; }
      }
    }
    var active = document.activeElement;
    return active && target.contains(active) && active.matches("input:not([type=hidden]), textarea, select, [contenteditable=true]");
  }
  function apply(target, item) {
    if (!target.isConnected || interacting(target)) { return false; }
    var fragment = document.createElement("template");
    fragment.innerHTML = item.html;
    var focus = document.activeElement;
    var scroll = Array.from(target.querySelectorAll(".scroll")).map(function (node) {
      return {node:node, top:node.scrollTop, left:node.scrollLeft};
    });
    // The logs container is itself a scroll box, unlike the repositories.
    scroll.push({node:target, top:target.scrollTop, left:target.scrollLeft});
    reconcile(target, fragment.content);
    if (focus && focus.isConnected && document.activeElement !== focus) { focus.focus({preventScroll:true}); }
    scroll.forEach(function (pos) { if (pos.node.isConnected) { pos.node.scrollTop = pos.top; pos.node.scrollLeft = pos.left; } });
    if (window.htmx) { window.htmx.process(target); }
    target.dispatchEvent(new CustomEvent("forgemirror:updated", {bubbles:true}));
    return true;
  }
  function flush() {
    pending.forEach(function (item, target) {
      if (!target.isConnected || apply(target, item)) { pending.delete(target); }
    });
  }

  document.addEventListener("htmx:beforeRequest", function (ev) {
    ev.detail.xhr.forgemirrorSequence = ++sequence;
    var source = ev.detail.elt;
    if (source && source.matches('form.run-action')) { source.setAttribute('aria-busy', 'true'); }
  });
  document.addEventListener('htmx:afterRequest', function (ev) {
    var source = ev.detail.elt;
    if (source && source.matches('form.run-action')) { source.setAttribute('aria-busy', 'false'); }
  });
  document.addEventListener("htmx:beforeSwap", function (ev) {
    var target = ev.detail.target;
    if (!target || !/^(repos|overview|logs)$/.test(target.id) || !ev.detail.shouldSwap) { return; }
    ev.detail.shouldSwap = false;
    var seq = ev.detail.xhr.forgemirrorSequence || ++sequence;
    if (seq < (accepted.get(target) || 0)) { return; }
    accepted.set(target, seq);
    var item = {html:ev.detail.serverResponse};
    var previous = pending.get(target);
    // Do not discard an action's acknowledgement when polls are deferred.
    if (previous) {
      var old = document.createElement("template"); old.innerHTML = previous.html;
      var notice = old.content.querySelector(".notice[role=status]");
      if (notice && item.html.indexOf('role="status"') < 0) { item.html += notice.outerHTML; }
    }
    if (apply(target, item)) { pending.delete(target); } else { pending.set(target, item); }
  });
  document.addEventListener("selectionchange", flush);
  document.addEventListener("focusout", function () { window.setTimeout(flush, 0); });
})();
