// Progressive enhancement: draft state is local; current holds stay server-owned.
(function () {
  'use strict';
  function element(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }
  function symbol(check) {
    var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 16 16'); svg.setAttribute('width', '16'); svg.setAttribute('height', '16');
    svg.setAttribute('fill', 'none'); svg.setAttribute('stroke', 'currentColor'); svg.setAttribute('stroke-width', '1.5');
    svg.setAttribute('stroke-linecap', 'round'); svg.setAttribute('stroke-linejoin', 'round'); svg.setAttribute('aria-hidden', 'true');
    var path = document.createElementNS(svg.namespaceURI, 'path'); path.setAttribute('d', check ? 'm3.5 8 3 3 6-6' : 'M8 3.5v9M3.5 8h9'); svg.append(path); return svg;
  }
  function icons(parent, icon) {
    if (['github', 'gitlab', 'codeberg'].indexOf(icon) < 0) return;
    function image(name, cls) { var img = element('img', 'forge-icon ' + cls); img.src = 'static/icons/' + name + '.svg'; img.alt = ''; img.setAttribute('aria-hidden', 'true'); parent.append(img); }
    image(icon, icon === 'codeberg' ? 'forge-icon-light' : 'forge-icon-monochrome');
    if (icon === 'codeberg') image('codeberg-white', 'forge-icon-dark');
  }
  function options(select) { return Array.from(select.options).map(function (o) { return {value:o.value, label:o.textContent, icon:o.dataset.icon || ''}; }); }
  function matches(pattern, name) {
    var escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '[^/]*').replace(/\?/g, '[^/]');
    return new RegExp('^' + escaped + '$', 'u').test(name);
  }
  function validPattern(pattern) {
    var parts = pattern.split('/');
    return pattern.length <= 256 && parts.length === 2 && parts.every(function (part) { return part && part !== '.' && part !== '..'; }) && !/[\s\x00-\x1f\x7f\[\]{}!\\]/u.test(pattern);
  }
  function init(root) {
    if (root._holdControls) { root._holdControls.refresh(); return; }
    var form = root.querySelector('[data-control-draft]');
    if (!form) return;
    var state = {tab:'new', step:0, busy:false, targets:[], repositories:[]};
    var duration = form.querySelector('#hold-duration-source');
    var custom = form.querySelector('#hold-custom-duration');
    var reason = form.querySelector('#hold-reason');
    var submit = form.querySelector('[data-hold-submit]');
    var validation = form.querySelector('[data-hold-validation]');
    var durationPicker = form.querySelector('[data-duration-picker]');
    var durationButton = durationPicker.querySelector('button');
    var durationMenu = durationPicker.querySelector('.hold-picker-menu');
    var pickers = [];
    var stages = Array.from(form.querySelectorAll('[data-hold-stage]'));
    function targetInfo(value) { return state.targets.find(function (t) { return t.value === value; }) || {value:value,label:value,icon:''}; }
    function repositoryTerms() { return Array.from(new Set(repoPicker.values.concat(repoPicker.input.value.split(',').map(function (s) { return s.trim(); }).filter(Boolean)))); }
    function selectedTargets() { return targetPicker.all ? state.targets : targetPicker.values.map(targetInfo); }
    function matchedRepositories() { if (repoPicker.all) return state.repositories; var terms = repositoryTerms(); return state.repositories.filter(function (name) { return terms.some(function (term) { return matches(term, name); }); }); }
    function durationLabel() { return duration.value === 'custom' ? custom.value || 'Custom duration' : duration.selectedOptions[0].textContent; }
    function errorFor(step) {
      if (!targetPicker.all && (!targetPicker.values.length || targetPicker.values.length > 64 || targetPicker.values.some(function (id) { return !state.targets.some(function (t) { return t.value === id; }); }))) return 'Choose at least one configured target.';
      if (step < 1) return '';
      var terms = repositoryTerms();
      if (!repoPicker.all && (!terms.length || terms.length > 64 || terms.some(function (term) { return !validPattern(term) || (!/[?*]/.test(term) && state.repositories.indexOf(term) < 0); }) || !matchedRepositories().length)) return 'Choose admitted repositories or matching owner/name globs using only * and ?. Empty does not mean all.';
      if (step < 2) return '';
      if (duration.value === 'custom' && (!custom.value.trim() || /^-/.test(custom.value))) return 'Enter a positive duration such as 45m or 6h.';
      if (new TextEncoder().encode(reason.value).length > 256) return 'Reason must be at most 256 bytes.';
      return '';
    }
    function closeMenus() { pickers.forEach(function (p) { p.menu.hidden = true; p.input.setAttribute('aria-expanded','false'); p.toggle.setAttribute('aria-expanded','false'); }); durationMenu.hidden = true; durationButton.setAttribute('aria-expanded','false'); }
    function makeOption(label, icon, click, checked) {
      var button = element('button','hold-option'); button.type = 'button'; button.setAttribute('role','option'); button.setAttribute('aria-selected', String(Boolean(checked)));
      var mark = element('span','hold-option-icon'); mark.append(symbol(Boolean(checked))); button.append(mark); icons(button,icon); button.append(element('span','',label)); button.addEventListener('click',click); return button;
    }
    function menuKeys(event, menu, input) {
      var buttons = Array.from(menu.querySelectorAll('[role=option]'));
      if (event.key === 'Escape') { event.preventDefault(); input.focus(); closeMenus(); }
      if (['ArrowDown','ArrowUp','Home','End'].indexOf(event.key) < 0 || !buttons.length) return;
      event.preventDefault(); var index = buttons.indexOf(document.activeElement); var next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length; buttons[next].focus();
    }
    function picker(kind) {
      var node = form.querySelector('[data-picker=' + kind + ']');
      var p = {kind:kind, node:node, input:node.querySelector('input'), tokens:node.querySelector('.hold-tokens'), toggle:node.querySelector('.hold-picker-toggle'), menu:node.querySelector('.hold-picker-menu'), list:node.querySelector('[role=listbox]'), all:true, values:[]};
      var allLabel = kind === 'targets' ? 'All targets' : 'All repositories';
      function allValue(value) { return value === '*' || value.toLowerCase() === allLabel.toLowerCase(); }
      p.open = function () { closeMenus(); p.menu.hidden = false; p.input.setAttribute('aria-expanded','true'); p.toggle.setAttribute('aria-expanded','true'); p.renderOptions(); };
      p.add = function (value, all) { p.all = Boolean(all); p.values = all ? [] : Array.from(new Set(p.values.concat(value))); p.input.value = ''; render(); p.input.focus(); p.open(); };
      p.commit = function () {
        if (!p.input.value.trim()) return;
        if (kind === 'repositories') { var values = p.input.value.split(',').map(function (s) { return s.trim(); }).filter(Boolean); if (values.some(allValue)) { p.all = true; p.values = []; } else { p.all = false; p.values = Array.from(new Set(p.values.concat(values))); } p.input.value = ''; render(); }
      };
      p.renderTokens = function () {
        p.tokens.replaceChildren();
        (p.all ? [allLabel] : p.values).forEach(function (value) {
          var token = element('span','hold-token' + (kind === 'repositories' && /[?*]/.test(value) ? ' pattern' : '')); token.setAttribute('role','listitem');
          var info = kind === 'targets' && !p.all ? targetInfo(value) : {label:value,icon:''}; icons(token,info.icon); token.append(element('span','hold-token-label',info.label));
          var remove = element('button','', '×'); remove.type = 'button'; remove.setAttribute('aria-label','Remove ' + info.label); remove.addEventListener('click',function () { if(p.all) p.all = false; else p.values = p.values.filter(function (v) { return v !== value; }); render(); p.input.focus(); p.open(); }); token.append(remove); p.tokens.append(token);
        });
      };
      p.renderOptions = function () {
        var query = p.input.value.trim().toLowerCase(); p.list.replaceChildren();
        if (!p.all && (!query || allLabel.toLowerCase().includes(query))) p.list.append(makeOption(allLabel,'',function () { p.add('',true); }));
        var candidates = kind === 'targets' ? state.targets : state.repositories.map(function (name) { return {value:name,label:name,icon:''}; });
        candidates.filter(function (item) { var chosen = kind === 'repositories' ? p.values.some(function (term) { return matches(term,item.value); }) : p.values.includes(item.value); return !chosen && (/[?*]/.test(query) && kind === 'repositories' ? matches(query,item.value.toLowerCase()) : item.label.toLowerCase().includes(query)); }).forEach(function (item) { p.list.append(makeOption(item.label,item.icon,function () { p.add(item.value,false); })); });
        if (!p.list.children.length) p.list.append(element('p','hold-picker-empty',kind === 'repositories' ? 'No more suggestions. Enter can still add a glob.' : 'No more matching targets.'));
      };
      p.input.addEventListener('focus',p.open); p.input.addEventListener('click',p.open);
      p.input.addEventListener('input',function () { if(kind === 'repositories' && p.input.value.trim()) p.all = false; p.open(); render(); });
      p.input.addEventListener('keydown',function (event) {
        if(event.key === 'Escape') closeMenus();
        if(event.key === 'ArrowDown') { event.preventDefault(); p.open(); p.list.querySelector('button')?.focus(); }
        if(event.key === 'Enter' || (event.key === ',' && kind === 'repositories')) { event.preventDefault(); if(kind === 'repositories') p.commit(); else if(p.input.value.trim()) p.list.querySelector('button')?.click(); p.open(); }
        if(event.key === 'Backspace' && !p.input.value) { if(p.all) p.all=false; else p.values.pop(); render(); p.open(); }
      });
      p.list.addEventListener('keydown',function (event) { menuKeys(event,p.list,p.input); });
      p.toggle.addEventListener('click',function () { if(p.menu.hidden) p.open(); else closeMenus(); });
      node.querySelector('[data-picker-done]').addEventListener('click',function () { p.commit(); closeMenus(); form.querySelector('[data-hold-next]').focus(); });
      pickers.push(p); node.hidden = false; return p;
    }
    var targetPicker = picker('targets');
    var repoPicker = picker('repositories');
    function syncNative() {
      form.querySelector('#hold-all-targets').checked = targetPicker.all;
      Array.from(form.querySelector('#hold-native-targets').options).forEach(function (o) { o.selected = !targetPicker.all && targetPicker.values.includes(o.value); });
      form.querySelector('#hold-all-repositories').checked = repoPicker.all;
      form.querySelector('#hold-native-repositories').value = repoPicker.values.join('\n');
      custom.disabled = duration.value !== 'custom'; custom.hidden = custom.disabled; form.querySelector('[data-custom-label]').hidden = custom.disabled;
    }
    function render() {
      root.classList.add('controls-enhanced');
      root.querySelector('[data-control-tabs]').hidden = false;
      syncNative(); targetPicker.renderTokens(); repoPicker.renderTokens();
      pickers.forEach(function (p) { if(!p.menu.hidden) p.renderOptions(); });
      form.setAttribute('aria-busy',String(state.busy));
      stages.forEach(function (node,index) { node.hidden = state.step !== index; node.disabled = state.busy; });
      form.querySelectorAll('[data-hold-step]').forEach(function (button) { if(Number(button.dataset.holdStep) === state.step) button.setAttribute('aria-current','step'); else button.removeAttribute('aria-current'); });
      form.querySelector('[data-hold-back]').disabled = state.step === 0;
      form.querySelector('[data-hold-next]').hidden = state.step === 3;
      form.querySelector('[data-hold-progress]').textContent = 'Step ' + (state.step + 1) + ' of 4';
      form.querySelector('[data-hold-step-title]').textContent = ['Choose targets','Choose repositories','Set duration and reason','Review publication hold'][state.step];
      root.querySelector('#hold-new-panel').hidden = state.tab !== 'new'; root.querySelector('#hold-current-panel').hidden = state.tab !== 'current';
      root.querySelectorAll('[data-hold-tab]').forEach(function (button) { button.setAttribute('aria-selected',String(button.dataset.holdTab === state.tab)); });
      var targets = selectedTargets(); var repos = matchedRepositories();
      var targetLabel = targetPicker.all ? 'All targets' : targets.map(function (t) { return t.label; }).join(', ') || 'No targets';
      var repositoryLabel = repoPicker.all ? 'All repositories' : repositoryTerms().join(', ') || 'No repositories';
      form.querySelector('[data-review-targets]').textContent = targetLabel;
      form.querySelector('[data-review-repositories]').textContent = repositoryLabel;
      form.querySelector('[data-review-duration]').textContent = durationLabel();
      form.querySelector('[data-duration-label]').textContent = durationLabel();
      submit.textContent = targetPicker.all ? 'Pause all targets' : targets.length === 1 ? 'Pause ' + targets[0].label : 'Pause ' + targets.length + ' targets';
      submit.disabled = state.busy || Boolean(errorFor(2));
      var preview = form.querySelector('[data-preview-targets]'); preview.replaceChildren(); targets.forEach(function (t) { var label = element('span'); icons(label,t.icon); label.append(document.createTextNode(t.label)); preview.append(label); });
      form.querySelector('[data-preview-count]').textContent = targets.length + (targets.length === 1 ? ' target × ' : ' targets × ') + repos.length + (repos.length === 1 ? ' known repository' : ' known repositories') + (repoPicker.all ? ' (including future admissions)' : '');
      var list = form.querySelector('[data-preview-repositories]'); list.replaceChildren(); repos.slice(0,30).forEach(function (name) { list.append(element('li','',name)); }); if(repos.length>30) list.append(element('li','', '… and ' + (repos.length-30) + ' more'));
    }
    function go(step) {
      if(state.busy || step < 0 || step > 3) return;
      repoPicker.commit(); var error = errorFor(step > 0 ? step - 1 : -1);
      if(step > 0 && error) { validation.textContent=error; return; }
      validation.textContent=''; closeMenus(); state.step=step; render();
    }
    function refresh() { state.targets=options(root.querySelector('[data-target-options]')); state.repositories=options(root.querySelector('[data-repository-options]')).map(function (o) { return o.value; }); render(); }
    root._holdControls={refresh:refresh,started:function(){state.busy=true;render();},created:function(){state.busy=false;state.step=0;state.tab='current';validation.textContent='';closeMenus();render();root.querySelector('[data-hold-tab=current]').focus({preventScroll:true});},finished:function(){state.busy=false;render();}};
    root.classList.add('controls-enhanced'); form.setAttribute('data-live-preserve','');
    form.querySelectorAll('[data-native-picker]').forEach(function (node) { node.hidden=true; });
    root.querySelector('[data-control-tabs]').hidden=false; form.querySelector('[data-hold-steps]').hidden=false; form.querySelector('[data-wizard-actions]').hidden=false; durationPicker.hidden=false;
    root.addEventListener('click',function (event) {
      var tab=event.target.closest('[data-hold-tab]'); if(tab){closeMenus();state.tab=tab.dataset.holdTab;render();}
      var step=event.target.closest('[data-hold-step]'); if(step)go(Number(step.dataset.holdStep));
      if(event.target.closest('[data-hold-next]'))go(state.step+1);
      if(event.target.closest('[data-hold-back]'))go(state.step-1);
      var path=event.composedPath(); if(!pickers.some(function(p){return path.includes(p.node);})&&!path.includes(durationPicker))closeMenus();
    });
    root.querySelector('[data-control-tabs]').addEventListener('keydown',function(event){if(['ArrowLeft','ArrowRight','Home','End'].includes(event.key)){event.preventDefault();state.tab=event.key==='Home'?'new':event.key==='End'?'current':state.tab==='new'?'current':'new';render();root.querySelector('[data-hold-tab='+state.tab+']').focus();}});
    function openDuration() {
      closeMenus(); var list=durationPicker.querySelector('[role=listbox]'); list.replaceChildren();
      Array.from(duration.options).forEach(function (o) { var option=makeOption(o.textContent,'',function(){duration.value=o.value;closeMenus();render();if(o.value==='custom')custom.focus();else durationButton.focus();},duration.value===o.value); option.dataset.duration=o.value; list.append(option); });
      durationMenu.hidden=false;durationButton.setAttribute('aria-expanded','true');
    }
    durationButton.addEventListener('click',function(){if(durationMenu.hidden)openDuration();else closeMenus();});
    durationButton.addEventListener('keydown',function(event){if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();openDuration();durationPicker.querySelector('[aria-selected=true]')?.focus();}if(event.key==='Escape')closeMenus();});
    durationPicker.querySelector('[role=listbox]').addEventListener('keydown',function(event){menuKeys(event,durationPicker.querySelector('[role=listbox]'),durationButton);});
    custom.addEventListener('input',render); reason.addEventListener('input',render);
    form.addEventListener('submit',function(event){repoPicker.commit();var error=errorFor(2);if(error||state.step!==3){event.preventDefault();event.stopImmediatePropagation();validation.textContent=error||'Review the complete scope before submitting.';state.step=error?state.step:3;render();}},true);
    refresh();
  }
  function initAll() { document.querySelectorAll('[data-control-root]').forEach(init); }
  document.addEventListener('DOMContentLoaded',initAll);
  document.addEventListener('forgemirror:updated',initAll);
  document.addEventListener('htmx:afterSwap',initAll);
  document.addEventListener('htmx:beforeRequest',function(event){if(event.detail.elt.matches?.('.hold-create-form')){var root=event.detail.elt.closest('[data-control-root]');if(root?._holdControls)root._holdControls.started();}});
  document.addEventListener('htmx:afterRequest',function(event){
    var source=event.detail.elt;var controls=document.querySelector('[data-control-root]');if(!source||!controls?._holdControls)return;
    if(source.matches('.hold-create-form')) { if(event.detail.successful)controls._holdControls.created();else controls._holdControls.finished(); }
    if(event.detail.successful&&(source.matches('.hold-create-form')||source.getAttribute('action')==='actions/resume-hold')){if(window.htmx)window.htmx.trigger(document.getElementById('repos'),'hold-changed');}
  });
})();
