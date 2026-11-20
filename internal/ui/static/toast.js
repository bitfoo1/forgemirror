// Action acknowledgements are transient and independent of polled fragments.
(function () {
  "use strict";
  var current = null;
  var lifetime = 5000;

  function pause(entry) {
    if (current !== entry || entry.timer === null) { return; }
    window.clearTimeout(entry.timer);
    entry.remaining = Math.max(0, entry.deadline - performance.now());
    entry.timer = null;
  }
  function dismiss(entry) {
    if (current !== entry) { return; }
    var restoreFocus = entry.node.contains(document.activeElement);
    pause(entry);
    entry.node.remove();
    current = null;
    if (restoreFocus && entry.returnFocus && entry.returnFocus.isConnected) {
      entry.returnFocus.focus({preventScroll:true});
    }
  }
  function resume(entry) {
    if (current !== entry || entry.kind === "error" || entry.timer !== null ||
        entry.node.matches(":hover") || entry.node.contains(document.activeElement)) { return; }
    entry.deadline = performance.now() + entry.remaining;
    entry.timer = window.setTimeout(function () { dismiss(entry); }, entry.remaining);
  }
  function show(detail) {
    var region = document.getElementById("notifications");
    if (!region || !detail || typeof detail.message !== "string" || !detail.message) { return; }
    var keepFocus = current && current.node.contains(document.activeElement);
    var returnFocus = keepFocus ? current.returnFocus : document.activeElement;
    if (current) { pause(current); }
    var kind = detail.kind === "error" ? "error" : "success";
    var toast = document.createElement("div");
    toast.className = "toast";
    toast.dataset.kind = kind;
    var message = document.createElement("p");
    message.className = "toast-message";
    message.textContent = detail.message;
    var close = document.createElement("button");
    close.type = "button";
    close.className = "btn btn-ghost toast-close";
    close.setAttribute("aria-label", "Dismiss notification");
    close.textContent = "×";
    toast.append(message, close);
    region.setAttribute("role", kind === "error" ? "alert" : "status");
    region.setAttribute("aria-live", kind === "error" ? "assertive" : "polite");
    region.replaceChildren(toast);
    var entry = {node:toast, kind:kind, remaining:lifetime, deadline:0, timer:null, returnFocus:returnFocus};
    current = entry;
    toast.addEventListener("mouseenter", function () { pause(entry); });
    toast.addEventListener("mouseleave", function () { resume(entry); });
    toast.addEventListener("focusin", function () { pause(entry); });
    toast.addEventListener("focusout", function () { window.setTimeout(function () { resume(entry); }, 0); });
    close.addEventListener("click", function () { dismiss(entry); });
    if (keepFocus) { close.focus({preventScroll:true}); }
    resume(entry);
  }

  document.addEventListener("forgemirror:notice", function (event) { show(event.detail); });

  function backendNotified(xhr) {
    if (!xhr) { return false; }
    try { return !!JSON.parse(xhr.getResponseHeader("HX-Trigger") || "{}")["forgemirror:notice"]; }
    catch (_) { return false; }
  }
  function failedRequest(event) {
    var detail = event.detail || {};
    var source = detail.elt;
    if (!source || !source.matches('form[hx-post^="actions/"]') || backendNotified(detail.xhr)) { return; }
    // An ambiguous network/HTTP failure does not prove that the backend did not start.
    show({kind:"error", message:"Could not confirm the action. Refresh repository status before retrying."});
  }
  ["htmx:responseError", "htmx:sendError", "htmx:timeout"].forEach(function (name) {
    document.addEventListener(name, failedRequest);
  });
})();
