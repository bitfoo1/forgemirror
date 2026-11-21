// forgemirror UI behaviour that must run before first paint: the stored theme
// and the collapsed cards. It is a classic script loaded without defer in
// <head>; the block scope keeps its bindings off the global object. Without
// JavaScript the page follows the system theme and every card stays open.
{
  const THEME_KEY = "forgemirror-theme";
  const THEMES = ["system", "light", "dark"];
  const CARDS_KEY = "forgemirror-cards";
  const DEFAULT_COLLAPSED = { config: true };
  const CARD_ID = /^[a-z][a-z-]*$/;
  const root = document.documentElement;

  // Storage can be unavailable (private windows, blocked site data); a choice
  // then lasts until the page is reloaded.
  const memory = new Map();
  const load = (key) => {
    try {
      const value = window.localStorage.getItem(key);
      if (value !== null) return value;
    } catch { /* fall back to memory */ }
    return memory.get(key) ?? null;
  };
  const store = (key, value) => {
    memory.set(key, value);
    try { window.localStorage.setItem(key, value); } catch { /* memory only */ }
  };

  const currentTheme = () => {
    const theme = load(THEME_KEY);
    return THEMES.includes(theme) ? theme : "system";
  };
  function applyTheme(theme) {
    if (theme === "system") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", theme);
    const button = document.getElementById("theme-toggle");
    if (button) {
      button.textContent = `Theme: ${theme}`;
      button.title = "Switch theme (system → light → dark)";
    }
  }
  applyTheme(currentTheme());

  // Collapsed cards, remembered per browser. The state is written to <html>
  // here, before first paint, so a collapsed card never flashes open; CSS
  // hides the card bodies.
  function cardPrefs() {
    try {
      const prefs = JSON.parse(load(CARDS_KEY) || "{}");
      return prefs && typeof prefs === "object" ? prefs : {};
    } catch {
      return {};
    }
  }
  const isCollapsed = (prefs, card) =>
    Object.hasOwn(prefs, card) ? prefs[card] === true : DEFAULT_COLLAPSED[card] === true;

  // Polling re-renders the repository header with the server's
  // aria-expanded="true", so the buttons are re-synced after every update.
  function syncCardToggles() {
    const prefs = cardPrefs();
    for (const button of document.querySelectorAll("[data-card-toggle]")) {
      const expanded = String(!isCollapsed(prefs, button.dataset.cardToggle));
      if (button.getAttribute("aria-expanded") !== expanded) button.setAttribute("aria-expanded", expanded);
    }
  }
  function applyCards() {
    const prefs = cardPrefs();
    const cards = new Set([...Object.keys(DEFAULT_COLLAPSED), ...Object.keys(prefs)]);
    const collapsed = [...cards].filter((card) => CARD_ID.test(card) && isCollapsed(prefs, card));
    root.setAttribute("data-collapsed", collapsed.join(" "));
    syncCardToggles();
  }
  applyCards();

  document.addEventListener("click", (event) => {
    const button = event.target.closest?.("[data-card-toggle]");
    if (!button) return;
    const card = button.dataset.cardToggle;
    const prefs = cardPrefs();
    prefs[card] = !isCollapsed(prefs, card);
    store(CARDS_KEY, JSON.stringify(prefs));
    applyCards();
  });

  // Keep other open tabs of the UI in step.
  window.addEventListener("storage", (event) => {
    if (event.key === THEME_KEY) applyTheme(currentTheme());
    if (event.key === CARDS_KEY) applyCards();
  });

  // Open state of the diffs, by repository. Polling replaces the repository
  // list every few seconds, which would otherwise close every open diff.
  const openState = new Map();
  function restoreDetails(scope) {
    for (const details of (scope || document).querySelectorAll("details[data-key]")) {
      const key = details.dataset.key;
      if (openState.has(key)) details.open = openState.get(key);
      else if (details.hasAttribute("data-open-default")) details.open = true;
    }
  }

  // Inline styles are forbidden by the Content-Security-Policy; setting a
  // property through the CSSOM is not.
  function paintSwatches(scope) {
    for (const swatch of (scope || document).querySelectorAll(".swatch[data-color]")) {
      const color = swatch.dataset.color;
      if (/^[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$/.test(color)) swatch.style.setProperty("--swatch", `#${color}`);
    }
  }

  document.addEventListener("toggle", (event) => {
    const details = event.target;
    if (details?.isConnected && details.matches?.("details[data-key]")) {
      openState.set(details.dataset.key, details.open);
    }
  }, true);

  function updated(event) {
    restoreDetails(event.target);
    paintSwatches(event.target);
    syncCardToggles();
  }
  document.addEventListener("htmx:afterSwap", updated);
  document.addEventListener("forgemirror:updated", updated);

  // Mirrors compactDuration in ui.go: the two largest units.
  function compactDuration(ms) {
    const minutes = Math.floor(ms / 60000);
    if (ms < 60000) return `${Math.floor(ms / 1000)}s`;
    if (minutes < 60) return `${minutes}m`;
    if (minutes < 1440) return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
    return `${Math.floor(minutes / 1440)}d ${Math.floor(minutes / 60) % 24}h`;
  }
  // The header is not polled. Counting from the server's elapsed time plus
  // the page's own monotonic clock avoids any client/server clock skew.
  function startUptime() {
    const node = document.querySelector("[data-uptime-ms]");
    if (!node) return;
    const elapsed = Number(node.dataset.uptimeMs);
    const loaded = performance.now();
    if (!Number.isFinite(elapsed)) return;
    const tick = () => { node.textContent = compactDuration(elapsed + performance.now() - loaded); };
    tick();
    window.setInterval(tick, 1000);
  }

  document.addEventListener("DOMContentLoaded", () => {
    startUptime();
    const button = document.getElementById("theme-toggle");
    if (button) {
      button.hidden = false;
      button.addEventListener("click", () => {
        const next = THEMES[(THEMES.indexOf(currentTheme()) + 1) % THEMES.length];
        store(THEME_KEY, next);
        applyTheme(next);
      });
    }
    applyTheme(currentTheme());
    applyCards();
    restoreDetails(document);
    paintSwatches(document);
  });
}
