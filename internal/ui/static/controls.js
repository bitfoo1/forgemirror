// Progressive enhancement of the publication-hold form: the draft lives in the
// browser, current holds stay server-owned. Without this module the native
// checkboxes, multi-select and textarea in the template still work.

const SVG_NS = "http://www.w3.org/2000/svg";
const KNOWN_ICONS = new Set(["github", "gitlab", "codeberg"]);
const STEP_TITLES = ["Choose targets", "Choose repositories", "Set duration and reason", "Review publication hold"];
const LAST_STEP = STEP_TITLES.length - 1;
const MAX_TERMS = 64;
const PREVIEW_LIMIT = 30;

// One controller per control root; the root element survives polling.
const controllers = new WeakMap();

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function symbol(check) {
  const svg = document.createElementNS(SVG_NS, "svg");
  const attributes = {
    viewBox: "0 0 16 16", width: "16", height: "16", fill: "none", stroke: "currentColor",
    "stroke-width": "1.5", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true",
  };
  for (const [name, value] of Object.entries(attributes)) svg.setAttribute(name, value);
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute("d", check ? "m3.5 8 3 3 6-6" : "M8 3.5v9M3.5 8h9");
  svg.append(path);
  return svg;
}

function appendIcons(parent, icon) {
  if (!KNOWN_ICONS.has(icon)) return;
  const image = (name, cls) => {
    const img = element("img", `forge-icon ${cls}`);
    img.src = `static/icons/${name}.svg`;
    img.alt = "";
    img.setAttribute("aria-hidden", "true");
    parent.append(img);
  };
  image(icon, icon === "codeberg" ? "forge-icon-light" : "forge-icon-monochrome");
  if (icon === "codeberg") image("codeberg-white", "forge-icon-dark");
}

function readOptions(select) {
  return Array.from(select.options, (o) => ({ value: o.value, label: o.textContent, icon: o.dataset.icon || "" }));
}

// Mirrors the server's owner/name glob semantics: * and ? never cross "/".
function globMatches(pattern, name) {
  const escaped = pattern
    .replace(/[.+^${}()|[\]\\]/g, "\\$&")
    .replace(/\*/g, "[^/]*")
    .replace(/\?/g, "[^/]");
  return new RegExp(`^${escaped}$`, "u").test(name);
}

function validPattern(pattern) {
  const parts = pattern.split("/");
  return (
    pattern.length <= 256 &&
    parts.length === 2 &&
    parts.every((part) => part && part !== "." && part !== "..") &&
    !/[\s\x00-\x1f\x7f[\]{}!\\]/u.test(pattern)
  );
}

const isGlob = (value) => /[?*]/.test(value);
const splitTerms = (text) => text.split(",").map((s) => s.trim()).filter(Boolean);

// IME users confirm a conversion candidate with Enter; that keystroke must not
// act. Safari reports it with isComposing already false but keyCode 229.
const composing = (event) => event.isComposing || event.keyCode === 229;

// Focus stays on the combobox (input or button); the highlighted option is
// conveyed with aria-activedescendant, as in the ARIA combobox pattern.
function activeDescendant(list, owner) {
  const options = () => Array.from(list.querySelectorAll("[role=option]"));
  const active = () => list.querySelector("[role=option].is-active");
  function set(option) {
    for (const o of options()) o.classList.toggle("is-active", o === option);
    if (option) {
      owner.setAttribute("aria-activedescendant", option.id);
      option.scrollIntoView({ block: "nearest" });
    } else {
      owner.removeAttribute("aria-activedescendant");
    }
  }
  function move(key) {
    const all = options();
    if (!all.length) return;
    const index = all.indexOf(active());
    let next;
    if (key === "Home") next = 0;
    else if (key === "End") next = all.length - 1;
    else if (key === "ArrowDown") next = index < 0 ? 0 : (index + 1) % all.length;
    else next = index < 0 ? all.length - 1 : (index - 1 + all.length) % all.length;
    set(all[next]);
  }
  return { options, active, set, move, clear: () => set(null) };
}

function makeOption(list, index, label, icon, onSelect, selected) {
  const option = element("div", "hold-option");
  option.id = `${list.id}-option-${index}`;
  option.setAttribute("role", "option");
  option.setAttribute("aria-selected", String(Boolean(selected)));
  const mark = element("span", "hold-option-icon");
  mark.append(symbol(Boolean(selected)));
  option.append(mark);
  appendIcons(option, icon);
  option.append(element("span", "", label));
  option.addEventListener("click", onSelect);
  return option;
}

function init(root) {
  const existing = controllers.get(root);
  if (existing) {
    existing.refresh();
    return;
  }
  const form = root.querySelector("[data-control-draft]");
  if (!form) return;

  const state = { tab: "new", step: 0, busy: false, targets: [], repositories: [] };
  const $ = (selector) => form.querySelector(selector);
  const duration = $("#hold-duration-source");
  const custom = $("#hold-custom-duration");
  const reason = $("#hold-reason");
  const submit = $("[data-hold-submit]");
  const validation = $("[data-hold-validation]");
  const durationPicker = $("[data-duration-picker]");
  const durationButton = durationPicker.querySelector("button");
  const durationMenu = durationPicker.querySelector(".hold-picker-menu");
  const durationList = durationPicker.querySelector("[role=listbox]");
  const durationNav = activeDescendant(durationList, durationButton);
  const stages = Array.from(form.querySelectorAll("[data-hold-stage]"));
  const pickers = [];

  const targetInfo = (value) =>
    state.targets.find((t) => t.value === value) || { value, label: value, icon: "" };
  const repositoryTerms = () => [...new Set([...repoPicker.values, ...splitTerms(repoPicker.input.value)])];
  const selectedTargets = () => (targetPicker.all ? state.targets : targetPicker.values.map(targetInfo));
  function matchedRepositories() {
    if (repoPicker.all) return state.repositories;
    const terms = repositoryTerms();
    return state.repositories.filter((name) => terms.some((term) => globMatches(term, name)));
  }
  const durationLabel = () =>
    duration.value === "custom" ? custom.value || "Custom duration" : duration.selectedOptions[0].textContent;

  // Returns the first problem in steps up to and including `step`.
  function errorFor(step) {
    const values = targetPicker.values;
    const unknownTarget = values.some((id) => !state.targets.some((t) => t.value === id));
    if (!targetPicker.all && (!values.length || values.length > MAX_TERMS || unknownTarget)) {
      return "Choose at least one configured target.";
    }
    if (step < 1) return "";
    const terms = repositoryTerms();
    const badTerm = terms.some(
      (term) => !validPattern(term) || (!isGlob(term) && !state.repositories.includes(term)),
    );
    if (!repoPicker.all && (!terms.length || terms.length > MAX_TERMS || badTerm || !matchedRepositories().length)) {
      return "Choose admitted repositories or matching owner/name globs using only * and ?. Empty does not mean all.";
    }
    if (step < 2) return "";
    if (duration.value === "custom" && (!custom.value.trim() || custom.value.startsWith("-"))) {
      return "Enter a positive duration such as 45m or 6h.";
    }
    if (new TextEncoder().encode(reason.value).length > 256) return "Reason must be at most 256 bytes.";
    return "";
  }

  function closeMenus() {
    for (const p of pickers) {
      p.menu.hidden = true;
      p.input.setAttribute("aria-expanded", "false");
      p.toggle.setAttribute("aria-expanded", "false");
      p.nav.clear();
    }
    durationMenu.hidden = true;
    durationButton.setAttribute("aria-expanded", "false");
    durationNav.clear();
  }

  function createPicker(kind) {
    const node = $(`[data-picker=${kind}]`);
    const list = node.querySelector("[role=listbox]");
    const input = node.querySelector("input");
    const p = {
      kind, node, input, list,
      tokens: node.querySelector(".hold-tokens"),
      toggle: node.querySelector(".hold-picker-toggle"),
      menu: node.querySelector(".hold-picker-menu"),
      nav: activeDescendant(list, input),
      all: true,
      values: [],
    };
    const allLabel = kind === "targets" ? "All targets" : "All repositories";
    const isAllValue = (value) => value === "*" || value.toLowerCase() === allLabel.toLowerCase();

    p.open = () => {
      if (!p.menu.hidden) return;
      closeMenus();
      p.menu.hidden = false;
      input.setAttribute("aria-expanded", "true");
      p.toggle.setAttribute("aria-expanded", "true");
      p.renderOptions();
    };
    p.add = (value, all) => {
      p.all = Boolean(all);
      p.values = all ? [] : [...new Set([...p.values, value])];
      input.value = "";
      render();
      input.focus();
      p.open();
    };
    p.commit = () => {
      if (kind !== "repositories" || !input.value.trim()) return;
      const values = splitTerms(input.value);
      if (values.some(isAllValue)) {
        p.all = true;
        p.values = [];
      } else {
        p.all = false;
        p.values = [...new Set([...p.values, ...values])];
      }
      input.value = "";
      render();
    };
    p.renderTokens = () => {
      p.tokens.replaceChildren();
      for (const value of p.all ? [allLabel] : p.values) {
        const token = element("span", `hold-token${kind === "repositories" && isGlob(value) ? " pattern" : ""}`);
        token.setAttribute("role", "listitem");
        const info = kind === "targets" && !p.all ? targetInfo(value) : { label: value, icon: "" };
        appendIcons(token, info.icon);
        token.append(element("span", "hold-token-label", info.label));
        const remove = element("button", "", "×");
        remove.type = "button";
        remove.setAttribute("aria-label", `Remove ${info.label}`);
        remove.addEventListener("click", () => {
          if (p.all) p.all = false;
          else p.values = p.values.filter((v) => v !== value);
          render();
          input.focus();
          p.open();
        });
        token.append(remove);
        p.tokens.append(token);
      }
    };
    p.renderOptions = () => {
      const query = input.value.trim().toLowerCase();
      list.replaceChildren();
      p.nav.clear();
      let index = 0;
      if (!p.all && (!query || allLabel.toLowerCase().includes(query))) {
        list.append(makeOption(list, index++, allLabel, "", () => p.add("", true)));
      }
      const candidates =
        kind === "targets" ? state.targets : state.repositories.map((name) => ({ value: name, label: name, icon: "" }));
      const chosen = (item) =>
        kind === "repositories"
          ? p.values.some((term) => globMatches(term, item.value))
          : p.values.includes(item.value);
      const wanted = (item) =>
        kind === "repositories" && isGlob(query)
          ? globMatches(query, item.value.toLowerCase())
          : item.label.toLowerCase().includes(query);
      for (const item of candidates) {
        if (chosen(item) || !wanted(item)) continue;
        list.append(makeOption(list, index++, item.label, item.icon, () => p.add(item.value, false)));
      }
      if (!index) {
        const empty = kind === "repositories" ? "No more suggestions. Enter can still add a glob." : "No more matching targets.";
        list.append(element("p", "hold-picker-empty", empty));
      }
    };

    input.addEventListener("focus", p.open);
    input.addEventListener("click", p.open);
    input.addEventListener("input", () => {
      if (kind === "repositories" && input.value.trim()) p.all = false;
      p.open();
      render();
    });
    input.addEventListener("keydown", (event) => {
      switch (event.key) {
        case "Escape":
          closeMenus();
          return;
        case "ArrowDown":
        case "ArrowUp":
          event.preventDefault();
          p.open();
          p.nav.move(event.key);
          return;
        case "Enter":
          // Always prevented: implicit submission would skip the review step.
          event.preventDefault();
          if (composing(event)) return;
          if (p.nav.active()) p.nav.active().click();
          else if (kind === "repositories") p.commit();
          else if (input.value.trim()) list.querySelector("[role=option]")?.click();
          p.open();
          return;
        case ",":
          if (kind !== "repositories" || composing(event)) return;
          event.preventDefault();
          p.commit();
          p.open();
          return;
        case "Backspace":
          if (input.value) return;
          if (p.all) p.all = false;
          else p.values.pop();
          render();
          p.open();
          return;
        default:
      }
    });
    // Keep focus in the input when an option is clicked.
    list.addEventListener("mousedown", (event) => event.preventDefault());
    p.toggle.addEventListener("click", () => {
      if (p.menu.hidden) {
        input.focus();
        p.open();
      } else {
        closeMenus();
      }
    });
    node.querySelector("[data-picker-done]").addEventListener("click", () => {
      p.commit();
      closeMenus();
      $("[data-hold-next]").focus();
    });
    pickers.push(p);
    node.hidden = false;
    return p;
  }

  const targetPicker = createPicker("targets");
  const repoPicker = createPicker("repositories");

  function syncNative() {
    $("#hold-all-targets").checked = targetPicker.all;
    for (const o of $("#hold-native-targets").options) {
      o.selected = !targetPicker.all && targetPicker.values.includes(o.value);
    }
    $("#hold-all-repositories").checked = repoPicker.all;
    $("#hold-native-repositories").value = repoPicker.values.join("\n");
    custom.disabled = duration.value !== "custom";
    custom.hidden = custom.disabled;
    $("[data-custom-label]").hidden = custom.disabled;
  }

  function renderPreview(targets, repos) {
    const preview = $("[data-preview-targets]");
    preview.replaceChildren();
    for (const t of targets) {
      const label = element("span");
      appendIcons(label, t.icon);
      label.append(document.createTextNode(t.label));
      preview.append(label);
    }
    const targetCount = `${targets.length} ${targets.length === 1 ? "target" : "targets"}`;
    const repoCount = `${repos.length} ${repos.length === 1 ? "known repository" : "known repositories"}`;
    $("[data-preview-count]").textContent =
      `${targetCount} × ${repoCount}${repoPicker.all ? " (including future admissions)" : ""}`;
    const list = $("[data-preview-repositories]");
    list.replaceChildren(...repos.slice(0, PREVIEW_LIMIT).map((name) => element("li", "", name)));
    if (repos.length > PREVIEW_LIMIT) list.append(element("li", "", `… and ${repos.length - PREVIEW_LIMIT} more`));
  }

  function render() {
    root.classList.add("controls-enhanced");
    root.querySelector("[data-control-tabs]").hidden = false;
    syncNative();
    targetPicker.renderTokens();
    repoPicker.renderTokens();
    for (const p of pickers) if (!p.menu.hidden) p.renderOptions();
    form.setAttribute("aria-busy", String(state.busy));
    stages.forEach((node, index) => {
      node.hidden = state.step !== index;
      node.disabled = state.busy;
    });
    for (const button of form.querySelectorAll("[data-hold-step]")) {
      if (Number(button.dataset.holdStep) === state.step) button.setAttribute("aria-current", "step");
      else button.removeAttribute("aria-current");
    }
    $("[data-hold-back]").disabled = state.step === 0;
    $("[data-hold-next]").hidden = state.step === LAST_STEP;
    $("[data-hold-progress]").textContent = `Step ${state.step + 1} of ${STEP_TITLES.length}`;
    $("[data-hold-step-title]").textContent = STEP_TITLES[state.step];
    root.querySelector("#hold-new-panel").hidden = state.tab !== "new";
    root.querySelector("#hold-current-panel").hidden = state.tab !== "current";
    // Roving tabindex: only the selected tab is a Tab stop; arrows move between tabs.
    for (const tab of root.querySelectorAll("[data-hold-tab]")) {
      const selected = tab.dataset.holdTab === state.tab;
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
    }
    const targets = selectedTargets();
    const repos = matchedRepositories();
    $("[data-review-targets]").textContent = targetPicker.all
      ? "All targets"
      : targets.map((t) => t.label).join(", ") || "No targets";
    $("[data-review-repositories]").textContent = repoPicker.all
      ? "All repositories"
      : repositoryTerms().join(", ") || "No repositories";
    $("[data-review-duration]").textContent = durationLabel();
    $("[data-duration-label]").textContent = durationLabel();
    if (targetPicker.all) submit.textContent = "Pause all targets";
    else if (targets.length === 1) submit.textContent = `Pause ${targets[0].label}`;
    else submit.textContent = `Pause ${targets.length} targets`;
    submit.disabled = state.busy || Boolean(errorFor(2));
    renderPreview(targets, repos);
  }

  function go(step) {
    if (state.busy || step < 0 || step > LAST_STEP) return;
    repoPicker.commit();
    const error = errorFor(step > 0 ? step - 1 : -1);
    if (step > 0 && error) {
      validation.textContent = error;
      return;
    }
    validation.textContent = "";
    closeMenus();
    state.step = step;
    render();
  }

  function refresh() {
    state.targets = readOptions(root.querySelector("[data-target-options]"));
    state.repositories = readOptions(root.querySelector("[data-repository-options]")).map((o) => o.value);
    render();
  }

  function selectTab(tab, focus) {
    closeMenus();
    state.tab = tab;
    render();
    if (focus) root.querySelector(`[data-hold-tab=${tab}]`).focus();
  }

  controllers.set(root, {
    refresh,
    started() {
      state.busy = true;
      render();
    },
    created() {
      state.busy = false;
      state.step = 0;
      validation.textContent = "";
      selectTab("current", false);
      root.querySelector("[data-hold-tab=current]").focus({ preventScroll: true });
    },
    finished() {
      state.busy = false;
      render();
    },
  });

  root.classList.add("controls-enhanced");
  form.setAttribute("data-live-preserve", "");
  for (const node of form.querySelectorAll("[data-native-picker]")) node.hidden = true;
  root.querySelector("[data-control-tabs]").hidden = false;
  $("[data-hold-steps]").hidden = false;
  $("[data-wizard-actions]").hidden = false;
  durationPicker.hidden = false;

  root.addEventListener("click", (event) => {
    const tab = event.target.closest("[data-hold-tab]");
    if (tab) selectTab(tab.dataset.holdTab, false);
    const step = event.target.closest("[data-hold-step]");
    if (step) go(Number(step.dataset.holdStep));
    if (event.target.closest("[data-hold-next]")) go(state.step + 1);
    if (event.target.closest("[data-hold-back]")) go(state.step - 1);
    const path = event.composedPath();
    if (!pickers.some((p) => path.includes(p.node)) && !path.includes(durationPicker)) closeMenus();
  });

  root.querySelector("[data-control-tabs]").addEventListener("keydown", (event) => {
    const next = { Home: "new", End: "current" }[event.key] ??
      (["ArrowLeft", "ArrowRight"].includes(event.key) ? (state.tab === "new" ? "current" : "new") : null);
    if (!next) return;
    event.preventDefault();
    selectTab(next, true);
  });

  // Duration is a select-only combobox: the button keeps focus.
  function openDuration() {
    closeMenus();
    durationList.replaceChildren(
      ...Array.from(duration.options, (o, index) => {
        const option = makeOption(durationList, index, o.textContent, "", () => chooseDuration(o.value), duration.value === o.value);
        option.dataset.duration = o.value;
        return option;
      }),
    );
    durationMenu.hidden = false;
    durationButton.setAttribute("aria-expanded", "true");
    durationNav.set(durationList.querySelector("[aria-selected=true]"));
  }
  function chooseDuration(value) {
    duration.value = value;
    closeMenus();
    render();
    if (value === "custom") custom.focus();
    else durationButton.focus();
  }
  durationButton.addEventListener("click", () => {
    if (durationMenu.hidden) openDuration();
    else closeMenus();
  });
  durationButton.addEventListener("keydown", (event) => {
    const open = !durationMenu.hidden;
    if (event.key === "Escape" || (event.key === "Tab" && open)) {
      closeMenus();
      return;
    }
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      if (!open) openDuration();
      if (open || event.key === "Home" || event.key === "End") durationNav.move(event.key);
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      // Prevented so the native button click does not toggle the menu again.
      event.preventDefault();
      if (open && durationNav.active()) durationNav.active().click();
      else if (!open) openDuration();
    }
  });
  // Space activates buttons on keyup; keydown already handled it.
  durationButton.addEventListener("keyup", (event) => {
    if (event.key === " ") event.preventDefault();
  });
  durationList.addEventListener("mousedown", (event) => event.preventDefault());

  custom.addEventListener("input", render);
  reason.addEventListener("input", render);
  form.addEventListener(
    "submit",
    (event) => {
      repoPicker.commit();
      const error = errorFor(2);
      if (!error && state.step === LAST_STEP) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      validation.textContent = error || "Review the complete scope before submitting.";
      if (!error) state.step = LAST_STEP;
      render();
    },
    true,
  );
  refresh();
}

function initAll() {
  for (const root of document.querySelectorAll("[data-control-root]")) init(root);
}

const controllerFor = (node) => {
  const root = node?.closest?.("[data-control-root]") ?? document.querySelector("[data-control-root]");
  return root ? controllers.get(root) : undefined;
};

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initAll);
else initAll();
document.addEventListener("forgemirror:updated", initAll);
document.addEventListener("htmx:afterSwap", initAll);
document.addEventListener("htmx:beforeRequest", (event) => {
  if (event.detail.elt.matches?.(".hold-create-form")) controllerFor(event.detail.elt)?.started();
});
document.addEventListener("htmx:afterRequest", (event) => {
  const source = event.detail.elt;
  const controller = controllerFor(source);
  if (!source || !controller) return;
  const creating = source.matches(".hold-create-form");
  if (creating) {
    if (event.detail.successful) controller.created();
    else controller.finished();
  }
  if (event.detail.successful && (creating || source.getAttribute("action") === "actions/resume-hold")) {
    window.htmx?.trigger(document.getElementById("repos"), "hold-changed");
  }
});
