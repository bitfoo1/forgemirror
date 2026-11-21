// Action acknowledgements: one transient toast at a time, independent of the
// polled fragments. Errors stay until dismissed; successes expire, pausing
// while hovered or focused.

const LIFETIME_MS = 5000;
let current = null;

function pause(entry) {
  if (current !== entry || entry.timer === null) return;
  window.clearTimeout(entry.timer);
  entry.remaining = Math.max(0, entry.deadline - performance.now());
  entry.timer = null;
}

function dismiss(entry) {
  if (current !== entry) return;
  const restoreFocus = entry.node.contains(document.activeElement);
  pause(entry);
  entry.node.remove();
  current = null;
  if (restoreFocus && entry.returnFocus?.isConnected) entry.returnFocus.focus({ preventScroll: true });
}

function resume(entry) {
  if (current !== entry || entry.kind === "error" || entry.timer !== null ||
      entry.node.matches(":hover") || entry.node.contains(document.activeElement)) return;
  entry.deadline = performance.now() + entry.remaining;
  entry.timer = window.setTimeout(() => dismiss(entry), entry.remaining);
}

// ariaNotify() announces without leaving text in the DOM; the pre-rendered
// live regions are the fallback for browsers without it. An options
// dictionary the browser does not understand throws, hence the try.
let lastAnnouncement = "";
function announce(text, urgent) {
  if (typeof document.ariaNotify === "function") {
    try {
      document.ariaNotify(text, { priority: urgent ? "high" : "normal" });
      return;
    } catch { /* use the live regions */ }
  }
  const live = document.getElementById(urgent ? "announce-assertive" : "announce-polite");
  if (!live) return;
  // VoiceOver skips a message identical to the previous one even after the
  // region was emptied; a trailing no-break space is silent but differs.
  let message = text;
  if (message === lastAnnouncement.replace(/ $/, "") && !lastAnnouncement.endsWith(" ")) {
    message += " ";
  }
  lastAnnouncement = message;
  live.textContent = message;
}

function show(detail) {
  const region = document.getElementById("notifications");
  if (!region || typeof detail?.message !== "string" || !detail.message) return;
  const keepFocus = Boolean(current?.node.contains(document.activeElement));
  const returnFocus = keepFocus ? current.returnFocus : document.activeElement;
  if (current) pause(current);
  const kind = detail.kind === "error" ? "error" : "success";

  const toast = document.createElement("div");
  toast.className = "toast";
  toast.dataset.kind = kind;
  const message = document.createElement("p");
  message.className = "toast-message";
  message.textContent = detail.message;
  const close = document.createElement("button");
  close.type = "button";
  close.className = "btn btn-ghost toast-close";
  close.setAttribute("aria-label", "Dismiss notification");
  close.textContent = "×";
  toast.append(message, close);
  region.replaceChildren(toast);
  announce(detail.message, kind === "error");

  const entry = { node: toast, kind, remaining: LIFETIME_MS, deadline: 0, timer: null, returnFocus };
  current = entry;
  toast.addEventListener("mouseenter", () => pause(entry));
  toast.addEventListener("mouseleave", () => resume(entry));
  toast.addEventListener("focusin", () => pause(entry));
  toast.addEventListener("focusout", () => window.setTimeout(() => resume(entry), 0));
  close.addEventListener("click", () => dismiss(entry));
  if (keepFocus) close.focus({ preventScroll: true });
  resume(entry);
}

document.addEventListener("forgemirror:notice", (event) => show(event.detail));

function backendNotified(xhr) {
  if (!xhr) return false;
  try {
    return Boolean(JSON.parse(xhr.getResponseHeader("HX-Trigger") || "{}")["forgemirror:notice"]);
  } catch {
    return false;
  }
}

function failedRequest(event) {
  const { elt: source, xhr } = event.detail || {};
  if (!source?.matches('form[hx-post^="actions/"]') || backendNotified(xhr)) return;
  // An ambiguous network/HTTP failure does not prove that the backend did not start.
  show({ kind: "error", message: "Could not confirm the action. Refresh repository status before retrying." });
}
for (const name of ["htmx:responseError", "htmx:sendError", "htmx:timeout"]) {
  document.addEventListener(name, failedRequest);
}
