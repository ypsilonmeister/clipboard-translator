(() => {
  'use strict';
  const token = new URLSearchParams(location.search).get('t') || '';
  const LETTERS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split('');
  const $ = (id) => document.getElementById(id);

  let state = null;   // what the app last told us
  let model = null;   // what the page is editing (hotkeys, auto-paste, longest text)
  let dirty = false;
  let saving = false;
  let acting = false; // a model / engine / download request is in flight
  let pollTimer = null;

  // h builds an element; text is always set as text, never as HTML.
  function h(tag, attrs, ...children) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v === null || v === undefined) continue;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
    for (const c of children.flat()) {
      if (c === null || c === undefined || c === false) continue;
      el.append(c.nodeType ? c : document.createTextNode(String(c)));
    }
    return el;
  }

  async function api(path, body) {
    const opt = { headers: { 'X-Settings-Token': token } };
    if (body !== undefined) {
      opt.method = 'POST';
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(path, opt);
    } catch (e) {
      return { ok: false, status: 0, data: { error: 'Cannot reach the app. If it was closed or the page was idle for a while, choose Settings... in the tray menu again.' } };
    }
    let data = {};
    try { data = await res.json(); } catch (e) { /* no body */ }
    return { ok: res.ok, status: res.status, data };
  }

  function toModel(st) {
    return {
      auto_paste: !!st.auto_paste,
      max_chars: st.max_chars || 4000,
      profiles: (st.profiles || []).map((p) => ({
        ctrl: !!p.ctrl, alt: !!p.alt, shift: !!p.shift, win: !!p.win, key: p.key || 'E',
        mode: p.target ? 'target' : 'pair',
        a: (p.languages && p.languages[0]) || 'ja',
        b: (p.languages && p.languages[1]) || 'en',
        target: p.target || 'en',
      })),
    };
  }

  function toPayload(m) {
    return {
      auto_paste: m.auto_paste,
      max_chars: m.max_chars,
      profiles: m.profiles.map((p) => {
        const out = { ctrl: p.ctrl, alt: p.alt, shift: p.shift, win: p.win, key: p.key };
        if (p.mode === 'target') out.target = p.target; else out.languages = [p.a, p.b];
        return out;
      }),
    };
  }

  function setStatus(text, kind) {
    const el = $('status');
    el.textContent = text || '';
    el.className = kind || '';
  }

  function markDirty() {
    dirty = true;
    setStatus('Unsaved changes');
  }

  function showBanner(lines, kind) {
    const el = $('banner');
    el.replaceChildren();
    el.className = 'banner' + (kind === 'error' ? ' error' : '');
    if (!lines || lines.length === 0) { el.hidden = true; return; }
    el.hidden = false;
    if (lines.length === 1) { el.append(h('p', { text: lines[0] })); return; }
    el.append(h('p', { text: lines[0] }), h('ul', {}, lines.slice(1).map((l) => h('li', { text: l }))));
  }

  function showNotice(text) {
    const el = $('notice');
    el.textContent = text || '';
    el.hidden = !text;
  }

  function inactiveBanner(list) {
    if (!list || list.length === 0) return showBanner([]);
    showBanner(['These hotkeys are not active right now:'].concat(list));
  }

  function fmtBytes(n) {
    if (n >= 1e9) return (n / 1e9).toFixed(2) + ' GB';
    return Math.round(n / 1e6) + ' MB';
  }

  const isRunning = (phase) => phase === 'checking' || phase === 'downloading' || phase === 'template';
  const appBusy = () => !!(state && state.busy);

  // ---- hotkeys ------------------------------------------------------------------------

  function langSelect(value, onchange, label) {
    const sel = h('select', { class: 'lang', 'aria-label': label, onchange: (e) => { onchange(e.target.value); markDirty(); } },
      state.language_options.map((o) => h('option', { value: o.code, text: o.name + ' (' + o.code + ')' })));
    sel.value = value;
    return sel;
  }

  function profileCard(p, i) {
    const mods = [['ctrl', 'Ctrl'], ['alt', 'Alt'], ['shift', 'Shift'], ['win', state.win_key]];
    const card = h('div', { class: 'profile', 'data-index': String(i) });

    const remove = h('button', { type: 'button', class: 'danger', disabled: model.profiles.length <= 1,
      'aria-label': 'Remove hotkey ' + (i + 1),
      onclick: () => { model.profiles.splice(i, 1); markDirty(); renderHotkeys(); } }, 'Remove');
    card.append(h('div', { class: 'profile-head' },
      h('h3', { text: 'Hotkey ' + (i + 1) }),
      i === 0 ? h('span', { class: 'badge', text: 'also changeable from the tray menu' }) : null,
      h('span', { class: 'spacer' }), remove));

    const keyRow = h('div', { class: 'row' }, h('span', { class: 'label', text: 'Keys' }));
    for (const [id, name] of mods) {
      keyRow.append(h('label', { class: 'chip' },
        h('input', { type: 'checkbox', checked: p[id], onchange: (e) => { p[id] = e.target.checked; markDirty(); } }), name));
    }
    keyRow.append(h('span', { class: 'plus', text: '+' }));
    const letter = h('select', { class: 'letter', 'aria-label': 'Key of hotkey ' + (i + 1), onchange: (e) => { p.key = e.target.value; markDirty(); } },
      LETTERS.map((l) => h('option', { value: l, text: l })));
    letter.value = p.key;
    keyRow.append(letter);
    card.append(keyRow);

    const name = 'mode-' + i;
    const seg = h('div', { class: 'segmented', role: 'radiogroup', 'aria-label': 'How hotkey ' + (i + 1) + ' translates' },
      [['pair', 'Between two languages'], ['target', 'Into one language']].map(([v, text]) =>
        h('label', {}, h('input', { type: 'radio', name, value: v, checked: p.mode === v,
          onchange: () => { p.mode = v; markDirty(); renderHotkeys(); } }), text)));
    card.append(h('div', { class: 'row' }, h('span', { class: 'label', text: 'Translate' }), seg));

    if (p.mode === 'pair') {
      card.append(h('div', { class: 'row' }, h('span', { class: 'label', text: 'Languages' }),
        langSelect(p.a, (v) => { p.a = v; }, 'First language'),
        h('span', { class: 'plus', text: '↔' }),
        langSelect(p.b, (v) => { p.b = v; }, 'Second language')));
      card.append(h('p', { class: 'hint', text: 'Text in either language is translated into the other. The two languages need different scripts so the app can tell them apart (for example English and German cannot share one hotkey).' }));
    } else {
      card.append(h('div', { class: 'row' }, h('span', { class: 'label', text: 'Into' }),
        langSelect(p.target, (v) => { p.target = v; }, 'Target language')));
      card.append(h('p', { class: 'hint', text: 'Any language is translated into this one; the model works out the source language itself.' }));
    }
    card.append(h('p', { class: 'field-error', role: 'alert', hidden: true }));
    return card;
  }

  function renderHotkeys() {
    $('profiles').replaceChildren(...model.profiles.map(profileCard));
    const max = state.max_hotkeys;
    const full = model.profiles.length >= max;
    $('add').hidden = max <= 1;
    $('add').disabled = full || appBusy();
    const limit = $('limit');
    if (max <= 1) {
      limit.hidden = false;
      limit.textContent = 'This system can use one hotkey at a time for now.';
    } else if (full) {
      limit.hidden = false;
      limit.textContent = 'This system can use up to ' + max + ' hotkeys.';
    } else {
      limit.hidden = true;
    }
  }

  function showFieldError(message) {
    for (const el of document.querySelectorAll('.profile')) {
      el.classList.remove('invalid');
      const f = el.querySelector('.field-error');
      f.hidden = true; f.textContent = '';
    }
    const m = /profiles\[(\d+)\]:\s*/.exec(message || '');
    if (!m) return false;
    const card = document.querySelector('.profile[data-index="' + m[1] + '"]');
    if (!card) return false;
    card.classList.add('invalid');
    const f = card.querySelector('.field-error');
    f.hidden = false;
    // The server's wording is made for config.json; say it in terms of this page.
    f.textContent = message.replace(m[0], '')
      .replace(/^(languages|target):\s*/, '')
      .replace(/\s*Pick a language with a different script for now\.?$/, ' Give each language its own hotkey set to "Into one language" instead.');
    card.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    return true;
  }

  // ---- models and engines --------------------------------------------------------------

  function modelRow(m) {
    const d = state.download;
    const mine = d && d.key === m.key;
    const running = mine && isRunning(d.phase);
    const otherRunning = d && isRunning(d.phase) && !mine;
    const lock = appBusy() || acting;

    const actions = h('div', { class: 'actions' });
    if (m.active) {
      actions.append(h('span', { class: 'badge on', text: 'In use' }));
    } else if (m.installed) {
      actions.append(h('button', { type: 'button', class: 'secondary', disabled: lock || running,
        onclick: () => act('/api/model/use', { key: m.key }) }, 'Use this model'));
    } else if (running) {
      actions.append(h('button', { type: 'button', class: 'secondary', onclick: cancelDownload }, 'Cancel'));
    } else {
      actions.append(h('button', { type: 'button', class: 'secondary', disabled: lock || otherRunning,
        onclick: () => act('/api/model/download', { key: m.key }) }, 'Download (' + m.size + ')'));
    }

    const row = h('div', { class: 'model' + (m.active ? ' active' : '') },
      h('div', { class: 'info' },
        h('div', { class: 'name' }, m.name, h('span', { class: 'size', text: m.size })),
        h('p', { class: 'note', text: m.note })),
      actions);

    if (running) {
      let label;
      if (d.phase === 'checking') label = 'Checking the part that was already downloaded...';
      else if (d.phase === 'template') label = 'Downloading the chat template...';
      else if (d.total > 0) label = fmtBytes(d.done) + ' of ' + fmtBytes(d.total) + ' (' + Math.floor(100 * d.done / d.total) + '%)';
      else label = fmtBytes(d.done) + ' downloaded';
      const bar = h('progress', { max: String(d.total > 0 ? d.total : 1) });
      if (d.total > 0 && d.phase === 'downloading') bar.value = d.done;
      row.append(h('div', { class: 'progress' }, bar, h('p', { class: 'ptext', text: label })));
    } else if (mine && d.phase === 'failed') {
      row.append(h('p', { class: 'msg err', text: 'The download failed: ' + d.error }));
    } else if (mine && d.phase === 'cancelled' && !m.installed) {
      row.append(h('p', { class: 'msg', text: 'Cancelled. The part that was downloaded is kept; Download continues from there.' }));
    }
    return row;
  }

  function renderModels() {
    $('models').replaceChildren(...state.models.map(modelRow));
    const custom = $('custom-model');
    custom.hidden = !state.custom_model;
    custom.textContent = state.custom_model ? 'The model in use is ' + state.custom_model + ' (set in config.json). Choosing one of the models above replaces it.' : '';
    $('license-link').href = state.license_url || '#';
  }

  function renderEngines() {
    const list = state.engines || [];
    $('engine-card').hidden = list.length === 0;
    const box = $('engines');
    if (list.length <= 1) {
      box.replaceChildren(h('p', { class: 'hint', text: 'Installed: ' + (list[0] ? list[0].label : 'none') + '. GPU engines are installed by the setup script (see the README).' }));
      return;
    }
    const lock = appBusy() || acting;
    box.replaceChildren(h('div', { class: 'segmented', role: 'radiogroup', 'aria-label': 'Translation engine' },
      list.map((e) => h('label', {}, h('input', { type: 'radio', name: 'engine', value: e.name, checked: e.active, disabled: lock,
        onchange: () => act('/api/engine', { name: e.name }) }), e.label))));
    box.append(h('p', { class: 'hint', text: 'Switching reloads the model. If the chosen engine cannot start, the previous one stays.' }));
  }

  function renderBusy() {
    const bar = $('busybar');
    bar.hidden = !appBusy();
    $('busytext').textContent = appBusy() ? 'The app is busy: ' + state.busy + '. Loading a model can take a minute.' : '';
    $('save').disabled = saving || appBusy();
    showNotice(state.notice || '');
  }

  function renderAll() {
    renderModels();
    renderEngines();
    renderBusy();
  }

  // Take new data from the app without touching what is being edited.
  function mergeState(st) {
    const wasBusy = appBusy();
    state = Object.assign({}, st);
    renderAll();
    // The hotkey cards are not rebuilt here: this runs every second while a download
    // or a switch is going on, and that would take the focus away from what is being typed.
    $('add').disabled = model.profiles.length >= state.max_hotkeys || appBusy();
    if (wasBusy && !appBusy()) setStatus(dirty ? 'Unsaved changes' : '');
  }

  function needsPolling() {
    return appBusy() || (state && state.download && isRunning(state.download.phase)) || acting;
  }

  function schedulePoll() {
    clearTimeout(pollTimer);
    if (needsPolling()) pollTimer = setTimeout(poll, 1000);
  }

  async function poll() {
    const r = await api('/api/state');
    if (r.ok && r.data.state) {
      mergeState(r.data.state);
    } else if (r.status === 503) {
      // The app is loading a model and could not answer in time: it is still working.
      if (state) { state.busy = state.busy || 'working'; renderBusy(); }
    } else if (r.status === 0) {
      showNotice(r.data.error);
      return;
    }
    schedulePoll();
  }

  // act sends a model / engine / download request and then follows its progress.
  async function act(path, body) {
    if (acting) return;
    acting = true;
    showNotice('');
    renderAll();
    const r = await api(path, body);
    acting = false;
    if (r.data && r.data.state) mergeState(r.data.state); else renderAll();
    if (!r.ok && r.status !== 503) showNotice((r.data && r.data.error) || 'That did not work.');
    schedulePoll();
  }

  async function cancelDownload() {
    await api('/api/model/cancel', {});
    schedulePoll();
    poll();
  }

  // ---- saving --------------------------------------------------------------------------

  async function save() {
    if (saving || appBusy()) return;
    const n = Number($('max-chars').value);
    if (!Number.isInteger(n) || n < 1 || n > 100000) {
      setStatus('Not saved', 'bad');
      showBanner(['The longest text must be a whole number from 1 to 100000.'], 'error');
      $('max-chars').focus();
      return;
    }
    model.max_chars = n;
    saving = true;
    $('save').disabled = true;
    setStatus('Saving...');
    showFieldError('');
    const r = await api('/api/save', toPayload(model));
    saving = false;
    renderBusy();
    if (!r.ok) {
      const msg = (r.data && r.data.error) || 'The settings could not be saved.';
      setStatus('Not saved', 'bad');
      if (!showFieldError(msg)) showBanner([msg], 'error');
      return;
    }
    state = Object.assign({}, r.data.state);
    model = toModel(state);
    dirty = false;
    renderAll();
    renderHotkeys();
    $('auto-paste').checked = model.auto_paste;
    $('max-chars').value = String(model.max_chars);
    const warnings = r.data.warnings || [];
    showBanner(warnings.length ? ['Saved, but note:'].concat(warnings) : []);
    setStatus('Saved', 'ok');
  }

  function nextFreeKey() {
    const used = new Set(model.profiles.map((p) => p.key));
    return 'KDFGHJLMNPQRSTUVWXYZEABC'.split('').find((l) => !used.has(l)) || 'K';
  }

  async function load() {
    const r = await api('/api/state');
    if (!r.ok || !r.data.state) {
      if (r.status === 503) { showNotice('The app is busy right now. Reload this page in a moment.'); }
      else showBanner([(r.data && r.data.error) || 'The settings could not be loaded.'], 'error');
      return false;
    }
    state = Object.assign({}, r.data.state);
    model = toModel(state);
    renderAll();
    renderHotkeys();
    $('auto-paste').checked = model.auto_paste;
    $('max-chars').value = String(model.max_chars);
    inactiveBanner(state.inactive);
    schedulePoll();
    return true;
  }

  function goodbye(text) {
    document.title = 'Clipboard Translator';
    document.body.replaceChildren(h('main', { class: 'gone' }, h('h1', { text: 'Clipboard Translator' }), h('p', { text }),
      h('p', { text: 'To change settings again, choose Settings... in the tray menu.' })));
  }

  async function init() {
    $('add').addEventListener('click', () => {
      model.profiles.push({ ctrl: true, alt: true, shift: false, win: false, key: nextFreeKey(), mode: 'target', a: 'ja', b: 'en', target: 'en' });
      markDirty();
      renderHotkeys();
    });
    $('auto-paste').addEventListener('change', (e) => { model.auto_paste = e.target.checked; markDirty(); });
    $('max-chars').addEventListener('input', markDirty);
    $('save').addEventListener('click', save);
    $('close').addEventListener('click', async () => {
      if (dirty && !confirm('Close without saving your changes?')) return;
      await api('/api/bye', {});
      goodbye('You can close this tab now.');
    });
    window.addEventListener('beforeunload', (e) => { if (dirty) { e.preventDefault(); e.returnValue = ''; } });
    if (!(await load())) { $('save').disabled = true; $('add').disabled = true; return; }
    // The app closes this page's server a few minutes after the last request.
    setInterval(async () => {
      const r = await api('/api/ping', {});
      if (!r.ok && r.status === 0) showNotice(r.data.error);
    }, 60000);
  }

  init();
})();
