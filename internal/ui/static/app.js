// forgemirror UI behaviour. Loaded without defer in <head>, so the stored
// theme applies before the first paint. Everything here is optional: without
// JavaScript the page follows the system theme and works with plain forms.
(function () {
  "use strict";

  var THEME_KEY = "forgemirror-theme";
  var THEMES = ["system", "light", "dark"];
  var root = document.documentElement;

  // Storage can be unavailable (private windows, blocked site data); the
  // choice then lasts until the page is reloaded.
  var memory = {};
  function load(key) {
    try {
      var v = window.localStorage.getItem(key);
      if (v !== null) { return v; }
    } catch (e) { /* fall back to memory */ }
    return Object.prototype.hasOwnProperty.call(memory, key) ? memory[key] : null;
  }
  function store(key, value) {
    memory[key] = value;
    try { window.localStorage.setItem(key, value); } catch (e) { /* memory only */ }
  }

  function currentTheme() {
    var t = load(THEME_KEY);
    return THEMES.indexOf(t) >= 0 ? t : "system";
  }

  function applyTheme(theme) {
    if (theme === "system") {
      root.removeAttribute("data-theme");
    } else {
      root.setAttribute("data-theme", theme);
    }
    var button = document.getElementById("theme-toggle");
    if (button) {
      button.textContent = "Theme: " + theme;
      button.title = "Switch theme (system → light → dark)";
    }
  }

  applyTheme(currentTheme());

  // Open state of the diffs, by repository. Polling replaces the repository
  // list every few seconds, which would otherwise close every open diff.
  var openState = {};

  function restoreDetails(scope) {
    var list = (scope || document).querySelectorAll("details[data-key]");
    for (var i = 0; i < list.length; i++) {
      var d = list[i];
      var key = d.getAttribute("data-key");
      if (Object.prototype.hasOwnProperty.call(openState, key)) {
        d.open = openState[key];
      } else if (d.hasAttribute("data-open-default")) {
        d.open = true;
      }
    }
  }

  // Inline styles are forbidden by the Content-Security-Policy; setting a
  // property through the CSSOM is not.
  function paintSwatches(scope) {
    var list = (scope || document).querySelectorAll(".swatch[data-color]");
    for (var i = 0; i < list.length; i++) {
      var color = list[i].getAttribute("data-color");
      if (/^[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$/.test(color)) {
        list[i].style.setProperty("--swatch", "#" + color);
      }
    }
  }

  document.addEventListener("toggle", function (ev) {
    var d = ev.target;
    if (d && d.isConnected && d.matches && d.matches("details[data-key]")) {
      openState[d.getAttribute("data-key")] = d.open;
    }
  }, true);

  function updated(ev) {
    restoreDetails(ev.target);
    paintSwatches(ev.target);
  }
  document.addEventListener("htmx:afterSwap", updated);
  document.addEventListener("forgemirror:updated", updated);

  document.addEventListener("DOMContentLoaded", function () {
    var button = document.getElementById("theme-toggle");
    if (button) {
      button.hidden = false;
      button.addEventListener("click", function () {
        var next = THEMES[(THEMES.indexOf(currentTheme()) + 1) % THEMES.length];
        store(THEME_KEY, next);
        applyTheme(next);
      });
    }
    applyTheme(currentTheme());
    restoreDetails(document);
    paintSwatches(document);
  });
})();
