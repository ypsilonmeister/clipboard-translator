(() => {
  'use strict';
  const token = new URLSearchParams(location.search).get('t') || '';
  const HOTKEY_KEYS = [
    ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split(''),
    ...'0123456789'.split(''),
    'F1', 'F2', 'F3', 'F4', 'F5', 'F6', 'F7', 'F8', 'F9', 'F10', 'F11', 'F12',
  ];
  const $ = (id) => document.getElementById(id);

  let state = null;   // what the app last told us
  let model = null;   // what the page is editing (hotkeys, auto-paste, longest text)
  let dirty = false;
  let saving = false;
  let acting = false; // a model / engine / download request is in flight
  let confirmingDelete = null; // { key: string, kind: 'model' | 'partial' }
  let closed = false; // the page was closed via "Close this page"
  // The revision the unsaved edits are based on. Syncing with the app moves it only
  // while nothing is being edited: moving it under a dirty section would hide a newer
  // change made outside this page from the server's stale-save check.
  let baseRevision = 0;
  let pollTimer = null;
  let pingTimer = null;

  const dirtyFields = {
    profiles: false,
    auto_paste: false,
    notify_on_complete: false,
    max_chars: false,
    idle_unload_minutes: false,
  };

  const editSeq = {
    profiles: 0,
    auto_paste: 0,
    notify_on_complete: 0,
    max_chars: 0,
    idle_unload_minutes: 0,
  };

  function touchField(key) {
    dirtyFields[key] = true;
    editSeq[key]++;
    markDirty();
  }

  function isDirty() {
    return dirtyFields.profiles || dirtyFields.auto_paste || dirtyFields.notify_on_complete || dirtyFields.max_chars || dirtyFields.idle_unload_minutes;
  }

  function isSaveNeeded(dirtyState, noActiveHotkey) {
    return Boolean(dirtyState || noActiveHotkey);
  }

  function markDirty() {
    dirty = isDirty();
    setStatus(dirty ? 'Unsaved changes' : '');
  }

  function clearDirty() {
    dirtyFields.profiles = false;
    dirtyFields.auto_paste = false;
    dirtyFields.notify_on_complete = false;
    dirtyFields.max_chars = false;
    dirtyFields.idle_unload_minutes = false;
    dirty = false;
    setStatus('');
  }

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
      notify_on_complete: !!st.notify_on_complete,
      max_chars: st.max_chars || 4000,
      idle_unload_minutes: st.idle_unload_minutes || 0,
      profiles: (st.profiles || []).map((p) => ({
        ctrl: !!p.ctrl, alt: !!p.alt, shift: !!p.shift, win: !!p.win, key: p.key || 'E',
        mode: p.target ? 'target' : 'pair',
        a: (p.languages && p.languages[0]) || 'ja',
        b: (p.languages && p.languages[1]) || 'en',
        target: p.target || 'en',
        output: p.output || 'replace',
        input: p.input || 'selection',
        cleanup_newlines: p.cleanup_newlines || 'off',
      })),
    };
  }

  function toPayload(m) {
    const changed = [];
    if (dirtyFields.profiles || (state && state.no_active_hotkey)) changed.push('profiles');
    if (dirtyFields.auto_paste) changed.push('auto_paste');
    if (dirtyFields.notify_on_complete) changed.push('notify_on_complete');
    if (dirtyFields.max_chars) changed.push('max_chars');
    if (dirtyFields.idle_unload_minutes) changed.push('idle_unload_minutes');
    if (changed.length === 0) {
      changed.push('profiles', 'auto_paste', 'notify_on_complete', 'max_chars', 'idle_unload_minutes');
    }
    return {
      base_revision: baseRevision,
      changed,
      auto_paste: m.auto_paste,
      notify_on_complete: m.notify_on_complete,
      max_chars: m.max_chars,
      idle_unload_minutes: m.idle_unload_minutes,
      profiles: m.profiles.map((p) => {
        const out = { ctrl: p.ctrl, alt: p.alt, shift: p.shift, win: p.win, key: p.key, output: p.output || 'replace', input: p.input || 'selection', cleanup_newlines: p.cleanup_newlines || 'off' };
        if (p.mode === 'target') out.target = p.target; else out.languages = [p.a, p.b];
        return out;
      }),
    };
  }

  function setStatus(text, kind) {
    const el = $('status');
    if (!el) return;
    el.textContent = text || '';
    el.className = kind || '';
  }

  function showBanner(lines, kind) {
    const el = $('banner');
    if (!el) return;
    el.replaceChildren();
    el.className = 'banner' + (kind === 'error' ? ' error' : '');
    if (!lines || lines.length === 0) { el.hidden = true; return; }
    el.hidden = false;
    if (lines.length === 1) { el.append(h('p', { text: lines[0] })); return; }
    el.append(h('p', { text: lines[0] }), h('ul', {}, lines.slice(1).map((l) => h('li', { text: l }))));
  }

  function showConflictBanner(msg, newState) {
    const el = $('banner');
    if (!el) return;
    el.replaceChildren();
    el.className = 'banner error';
    el.hidden = false;

    const reloadBtn = h('button', {
      type: 'button',
      class: 'secondary',
      onclick: () => {
        state = Object.assign({}, newState);
        baseRevision = state.revision || 0;
        model = toModel(state);
        clearDirty();
        renderAll();
        renderHotkeys();
        const autoPasteEl = $('auto-paste');
        if (autoPasteEl) autoPasteEl.checked = model.auto_paste;
        const notifyOnCompleteEl = $('notify-on-complete');
        if (notifyOnCompleteEl) notifyOnCompleteEl.checked = model.notify_on_complete;
        const maxCharsEl = $('max-chars');
        if (maxCharsEl) maxCharsEl.value = String(model.max_chars);
        $('idle-unload-minutes').value = String(model.idle_unload_minutes);
        showBanner([]);
        setStatus('Reloaded settings from app', 'ok');
      },
    }, 'Reload settings');

    const overwriteBtn = h('button', {
      type: 'button',
      class: 'secondary',
      onclick: () => {
        baseRevision = newState.revision || 0;
        showBanner([]);
        save();
      },
    }, 'Overwrite');

    const actions = h('div', { class: 'banner-actions' }, reloadBtn, overwriteBtn);
    el.append(h('p', { text: msg }), actions);
  }

  function showNotice(text, kind) {
    const el = $('notice');
    if (!el) return;
    el.textContent = text || '';
    el.className = 'banner' + (kind === 'ok' ? ' ok' : ' error');
    el.hidden = !text;
  }

  function inactiveBanner(list) {
    if (!list || list.length === 0) return showBanner([]);
    showBanner(['These hotkeys are not active right now. Choose another hotkey below and click Save. If none are active, you can also retry Save after the other program releases them:'].concat(list));
  }

  function fmtBytes(n) {
    if (n >= 1e9) return (n / 1e9).toFixed(1) + ' GB';
    if (n >= 1e6) return Math.round(n / 1e6) + ' MB';
    if (n >= 1e3) return Math.round(n / 1e3) + ' KB';
    return n + ' B';
  }

  const isRunning = (phase) => phase === 'checking' || phase === 'downloading' || phase === 'template';
  const appBusy = () => !!(state && state.busy);

  // ---- hotkeys ------------------------------------------------------------------------

  function langSelect(value, onchange, label) {
    const sel = h('select', { class: 'lang', 'aria-label': label, onchange: (e) => { onchange(e.target.value); touchField('profiles'); } },
      state.language_options.map((o) => h('option', { value: o.code, text: o.name + ' (' + o.code + ')' })));
    sel.value = value;
    return sel;
  }

  function profileCard(p, i) {
    const mods = [['ctrl', 'Ctrl'], ['alt', 'Alt'], ['shift', 'Shift'], ['win', state.win_key]];
    const card = h('div', { class: 'profile', 'data-index': String(i) });

    const remove = h('button', { type: 'button', class: 'danger', disabled: model.profiles.length <= 1,
      'aria-label': 'Remove hotkey ' + (i + 1),
      onclick: () => { model.profiles.splice(i, 1); touchField('profiles'); renderHotkeys(); } }, 'Remove');
    card.append(h('div', { class: 'profile-head' },
      h('h3', { text: 'Hotkey ' + (i + 1) }),
      h('span', { class: 'spacer' }), remove));

    const keyRow = h('div', { class: 'row' }, h('span', { class: 'label', text: 'Keys' }));
    for (const [id, name] of mods) {
      keyRow.append(h('label', { class: 'chip' },
        h('input', { type: 'checkbox', checked: p[id], onchange: (e) => { p[id] = e.target.checked; touchField('profiles'); } }), name));
    }
    keyRow.append(h('span', { class: 'plus', text: '+' }));
    const letter = h('select', { class: 'letter', 'aria-label': 'Key of hotkey ' + (i + 1), onchange: (e) => { p.key = e.target.value; touchField('profiles'); } },
      HOTKEY_KEYS.map((l) => h('option', { value: l, text: l })));
    letter.value = p.key;
    keyRow.append(letter);
    card.append(keyRow);

    const name = 'mode-' + i;
    const seg = h('div', { class: 'segmented', role: 'radiogroup', 'aria-label': 'How hotkey ' + (i + 1) + ' translates' },
      [['pair', 'Between two languages'], ['target', 'Into one language']].map(([v, text]) =>
        h('label', {}, h('input', { type: 'radio', name, value: v, checked: p.mode === v,
          onchange: () => { p.mode = v; touchField('profiles'); renderHotkeys(); } }), text)));
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

    const inputId = 'input-' + i;
    const inputSelect = h('select', {
      id: inputId,
      class: 'input',
      'aria-label': 'Text source for hotkey ' + (i + 1),
      onchange: (e) => {
        p.input = e.target.value;
        touchField('profiles');
      },
    },
      h('option', { value: 'selection', text: 'Selected text (copied first)' }),
      h('option', { value: 'clipboard', text: 'Clipboard (no copy, no waiting)' }),
    );
    inputSelect.value = p.input || 'selection';
    card.append(h('div', { class: 'row' }, h('label', { class: 'label', for: inputId, text: 'Text from' }), inputSelect));

    const outputId = 'output-' + i;
    const outputSelect = h('select', {
      id: outputId,
      class: 'output',
      'aria-label': 'Output format for hotkey ' + (i + 1),
      onchange: (e) => {
        p.output = e.target.value;
        touchField('profiles');
      },
    },
      h('option', { value: 'replace', text: 'Replace with the translation' }),
      h('option', { value: 'bilingual', text: 'Keep the original, then the translation' }),
    );
    outputSelect.value = p.output || 'replace';
    card.append(h('div', { class: 'row' }, h('label', { class: 'label', for: outputId, text: 'Output' }), outputSelect));

    const cleanupId = 'cleanup-' + i;
    const cleanupSelect = h('select', {
      id: cleanupId,
      class: 'cleanup-newlines',
      'aria-label': 'Newline cleanup for hotkey ' + (i + 1),
      onchange: (e) => {
        p.cleanup_newlines = e.target.value;
        touchField('profiles');
      },
    },
      h('option', { value: 'off', text: 'Keep original line breaks' }),
      h('option', { value: 'auto', text: 'Clean up spurious line breaks (e.g. from PDFs)' }),
    );
    cleanupSelect.value = p.cleanup_newlines || 'off';
    card.append(h('div', { class: 'row' }, h('label', { class: 'label', for: cleanupId, text: 'Line breaks' }), cleanupSelect));

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
    const ready = m.installed && m.template_installed;

    if (confirmingDelete && confirmingDelete.key === m.key) {
      const isPartial = confirmingDelete.kind === 'partial';
      const valid = isPartial
        ? (m.can_delete_partial || (m.partial && !running))
        : m.can_delete;

      if (valid) {
        const sizeStr = isPartial
          ? fmtBytes(m.partial_bytes)
          : (m.installed ? m.size : fmtBytes(m.file_bytes));
        const confirmLabel = isPartial ? 'Confirm remove (' + sizeStr + ')' : 'Confirm delete (' + sizeStr + ')';
        const ariaLabel = isPartial ? 'Confirm remove partial download for ' + m.name : 'Confirm delete ' + m.name;

        const cancelBtn = h('button', {
          type: 'button',
          class: 'secondary',
          disabled: lock,
          'aria-label': 'Cancel delete ' + m.name,
          'data-key': m.key,
          'data-action': 'cancel-delete',
          onclick: () => {
            confirmingDelete = null;
            renderModels();
          },
        }, 'Cancel');

        const confirmBtn = h('button', {
          type: 'button',
          class: 'danger',
          disabled: lock,
          'aria-label': ariaLabel,
          'data-key': m.key,
          'data-action': 'confirm-delete',
          onclick: () => {
            if (confirmingDelete && Date.now() - (confirmingDelete.openedAt || 0) < 400) {
              return;
            }
            const target = confirmingDelete;
            confirmingDelete = null;
            act('/api/model/delete', { key: target.key, kind: target.kind });
          },
        }, confirmLabel);

        // Put Cancel first so Confirm does not appear directly under the cursor.
        actions.append(cancelBtn, confirmBtn);
      }
    } else if (running) {
      actions.append(h('button', {
        type: 'button',
        class: 'secondary',
        'data-key': m.key,
        'data-action': 'cancel-download',
        onclick: cancelDownload,
      }, 'Cancel'));
    } else {
      if (m.active) {
        actions.append(h('span', { class: 'badge on', text: 'In use' }));
      } else if (m.installed) {
        if (!m.template_installed) {
          actions.append(h('button', {
            type: 'button',
            class: 'secondary',
            disabled: lock || otherRunning,
            'data-key': m.key,
            'data-action': 'retry-template',
            onclick: () => act('/api/model/download', { key: m.key }),
          }, 'Retry chat template'));
        } else {
          actions.append(h('button', {
            type: 'button',
            class: 'secondary',
            disabled: lock || running,
            'data-key': m.key,
            'data-action': 'use',
            onclick: () => act('/api/model/use', { key: m.key }),
          }, 'Use this model'));
        }
        if (m.can_delete) {
          actions.append(h('button', {
            type: 'button',
            class: 'danger',
            disabled: lock,
            'aria-label': 'Delete ' + m.name,
            'data-key': m.key,
            'data-action': 'delete-model',
            onclick: () => {
              confirmingDelete = { key: m.key, kind: 'model', openedAt: Date.now() };
              renderModels();
            },
          }, 'Delete'));
        }
      } else {
        actions.append(h('button', {
          type: 'button',
          class: 'secondary',
          disabled: lock || otherRunning,
          'data-key': m.key,
          'data-action': 'download',
          onclick: () => act('/api/model/download', { key: m.key }),
        }, 'Download (' + m.size + ')'));

        if (m.has_file && m.can_delete) {
          actions.append(h('button', {
            type: 'button',
            class: 'danger',
            disabled: lock,
            'aria-label': 'Delete incomplete file for ' + m.name,
            'data-key': m.key,
            'data-action': 'delete-model',
            onclick: () => {
              confirmingDelete = { key: m.key, kind: 'model', openedAt: Date.now() };
              renderModels();
            },
          }, 'Delete'));
        }
      }

      if (m.partial && (m.can_delete_partial || (!running && !otherRunning))) {
        actions.append(h('button', {
          type: 'button',
          class: 'danger',
          disabled: lock,
          'aria-label': 'Remove partial download for ' + m.name,
          'data-key': m.key,
          'data-action': 'delete-partial',
          onclick: () => {
            confirmingDelete = { key: m.key, kind: 'partial', openedAt: Date.now() };
            renderModels();
          },
        }, 'Remove partial download'));
      }
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
    } else if (mine && d.phase === 'cancelled' && !ready) {
      row.append(h('p', { class: 'msg', text: 'Cancelled. The part that was downloaded is kept; Download or Retry continues from there.' }));
    }
    return row;
  }

  let lastModelSnapshot = '';

  function renderModels() {
    const modelsBox = $('models');
    if (!modelsBox) return;

    const currentSnapshot = JSON.stringify({
      models: state ? state.models : null,
      download: state ? state.download : null,
      custom: state ? state.custom_model : null,
      confirm: confirmingDelete,
      lock: appBusy() || acting,
    });

    if (lastModelSnapshot === currentSnapshot) {
      return;
    }
    lastModelSnapshot = currentSnapshot;

    const activeEl = document.activeElement;
    let focusedKey = null;
    let focusedAction = null;
    if (activeEl && modelsBox.contains(activeEl)) {
      focusedKey = activeEl.getAttribute('data-key');
      focusedAction = activeEl.getAttribute('data-action');
    }

    modelsBox.replaceChildren(...state.models.map(modelRow));
    const custom = $('custom-model');
    if (custom) {
      custom.hidden = !state.custom_model;
      custom.textContent = state.custom_model ? 'The model in use is ' + state.custom_model + ' (set in config.json). Choosing one of the models above replaces it.' : '';
    }
    const lic = $('license-link');
    if (lic) lic.href = state.license_url || '#';

    if (focusedKey && focusedAction) {
      const toFocus = modelsBox.querySelector(`[data-key="${focusedKey}"][data-action="${focusedAction}"]`);
      if (toFocus) {
        toFocus.focus();
      }
    }
  }

  function renderEngines() {
    const list = state.engines || [];
    $('engine-card').hidden = list.length === 0;
    const box = $('engines');
    if (list.length <= 1) {
      box.replaceChildren(h('p', { class: 'hint', text: 'Installed: ' + (list[0] ? list[0].label : 'none') + '. GPU engines are installed by the setup script (see the README).' }));
      if (state.configured_engine && state.running_engine && state.configured_engine !== state.running_engine) {
        renderEngineFallback(box, list);
      }
      return;
    }
    const lock = appBusy() || acting;
    box.replaceChildren(h('div', { class: 'segmented', role: 'radiogroup', 'aria-label': 'Translation engine' },
      list.map((e) => h('label', {}, h('input', { type: 'radio', name: 'engine', value: e.name, checked: e.active, disabled: lock,
        onchange: () => act('/api/engine', { name: e.name }) }), e.label))));
    box.append(h('p', { class: 'hint', text: 'Switching reloads the model. If the chosen engine cannot start, the previous one stays.' }));
    if (state.configured_engine && state.running_engine && state.configured_engine !== state.running_engine) {
      renderEngineFallback(box, list);
    }
  }

  function renderEngineFallback(box, list) {
    const lock = appBusy() || acting;
    const running = (list || []).find((e) => e.active) || { name: state.running_engine, label: state.running_engine };
    const runningLabel = (running && running.label) || state.running_engine || 'CPU';
    const runningName = (running && running.name) || state.running_engine;
    const keepBtn = h('button', {
      type: 'button',
      class: 'secondary',
      disabled: lock,
      onclick: () => act('/api/engine', { name: runningName }),
    }, 'Keep using ' + runningLabel);
    box.append(h('div', { class: 'engine-fallback' },
      h('p', { class: 'hint', text: 'The GPU engine did not start, so the ' + runningLabel + ' engine is in use. It will be tried again at the next start.' }),
      keepBtn));
  }

  let lastServerNotice = '';

  function renderBusy() {
    if (closed) return;
    const bar = $('busybar');
    if (bar) bar.hidden = !appBusy();
    const busyText = $('busytext');
    if (busyText) busyText.textContent = appBusy() ? 'The app is busy: ' + state.busy + '. Loading a model can take a minute.' : '';
    const saveBtn = $('save');
    if (saveBtn) saveBtn.disabled = saving || appBusy();
    const currentNotice = state ? (state.notice || '') : '';
    if (currentNotice) {
      showNotice(currentNotice, 'error');
    } else if (lastServerNotice) {
      showNotice('');
    }
    lastServerNotice = currentNotice;
  }

  // The quick start card: open on the first run or while no hotkey works, otherwise
  // collapsed so it can be read again without taking space. The user's own
  // open/close is kept across refreshes.
  let guideTouched = false;
  function renderGuide() {
    const g = state && state.guide;
    const box = $('guide');
    if (!g || !box) return;
    $('guide-intro').textContent = g.intro || '';
    $('guide-steps').replaceChildren(...(g.steps || []).map((s) => h('li', { text: s })));
    $('guide-extras').textContent = g.extras || '';
    if (!guideTouched) box.open = !!(g.initial || !g.active);
  }

  function renderAll() {
    renderGuide();
    renderModels();
    renderEngines();
    renderBusy();
  }

  // Take new data from the app without touching what is being edited.
  function mergeState(st) {
    if (closed) return;
    const wasBusy = appBusy();
    state = Object.assign({}, st);
    if (!isDirty()) baseRevision = st.revision || 0;
    renderAll();
    if (!dirtyFields.profiles && st.profiles) {
      // Rebuild the cards only when the app's profiles really differ: this runs every
      // second during a download or a switch, and a rebuild closes an open dropdown and
      // takes the focus away from what is being typed.
      const next = toModel(st).profiles;
      if (JSON.stringify(next) !== JSON.stringify(model.profiles)) {
        model.profiles = next;
        renderHotkeys();
      }
    }
    const addBtn = $('add');
    if (addBtn) addBtn.disabled = model.profiles.length >= state.max_hotkeys || appBusy();
    if (!dirtyFields.auto_paste && st.auto_paste !== undefined) {
      model.auto_paste = !!st.auto_paste;
      const autoPasteEl = $('auto-paste');
      if (autoPasteEl) autoPasteEl.checked = model.auto_paste;
    }
    if (!dirtyFields.notify_on_complete && st.notify_on_complete !== undefined) {
      model.notify_on_complete = !!st.notify_on_complete;
      const notifyOnCompleteEl = $('notify-on-complete');
      if (notifyOnCompleteEl) notifyOnCompleteEl.checked = model.notify_on_complete;
    }
    if (!dirtyFields.max_chars && st.max_chars !== undefined) {
      model.max_chars = st.max_chars;
      const maxCharsEl = $('max-chars');
      if (maxCharsEl) maxCharsEl.value = String(model.max_chars);
    }
    if (!dirtyFields.idle_unload_minutes && st.idle_unload_minutes !== undefined) {
      model.idle_unload_minutes = st.idle_unload_minutes;
      $('idle-unload-minutes').value = String(model.idle_unload_minutes);
    }
    dirty = isDirty();
    if (wasBusy && !appBusy()) setStatus(dirty ? 'Unsaved changes' : '');
    else if (!dirty) setStatus('');
  }

  function needsPolling() {
    return appBusy() || (state && state.download && isRunning(state.download.phase)) || acting;
  }

  function schedulePoll() {
    if (closed) return;
    clearTimeout(pollTimer);
    if (needsPolling()) pollTimer = setTimeout(poll, 1000);
  }

  async function poll() {
    if (closed) return;
    const r = await api('/api/state');
    if (closed) return;
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
    if (!r.ok && r.status !== 503) {
      showNotice((r.data && r.data.error) || 'That did not work.', 'error');
    } else if (r.data && r.data.notice) {
      showNotice(r.data.notice, 'ok');
    }
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
      showFieldError('');
      showBanner(['The longest text must be a whole number from 1 to 100000.'], 'error');
      $('max-chars').focus();
      return;
    }
    if (model.max_chars !== n) {
      model.max_chars = n;
      touchField('max_chars');
    }
    const minutes = Number($('idle-unload-minutes').value);
    if (!Number.isInteger(minutes) || minutes < 0 || minutes > 1440) {
      setStatus('Not saved', 'bad');
      showBanner(['Idle unload minutes must be a whole number from 0 to 1440 (0 means never).'], 'error');
      $('idle-unload-minutes').focus();
      return;
    }
    if (model.idle_unload_minutes !== minutes) {
      model.idle_unload_minutes = minutes;
      touchField('idle_unload_minutes');
    }
    if (!isSaveNeeded(isDirty(), state && state.no_active_hotkey)) {
      // Nothing to send. Sending every field with an old revision would report a
      // conflict, or overwrite a change made in the tray, for edits that were never made.
      setStatus('No changes to save');
      return;
    }
    saving = true;
    $('save').disabled = true;
    setStatus('Saving...');
    showFieldError('');
    showBanner([]);
    const submittedDirty = Object.assign({}, dirtyFields);
    const submittedSeq = Object.assign({}, editSeq);
    const r = await api('/api/save', toPayload(model));
    saving = false;
    renderBusy();
    if (!r.ok) {
      const msg = (r.data && r.data.error) || 'The settings could not be saved.';
      setStatus('Not saved', 'bad');
      if (r.status === 409 && r.data && r.data.state) {
        showConflictBanner(msg, r.data.state);
        return;
      }
      if (showFieldError(msg)) {
        showBanner([]);
      } else {
        showBanner([msg], 'error');
      }
      return;
    }
    state = Object.assign({}, r.data.state);
    const freshModel = toModel(state);
    const hasNewEdits = (key) => editSeq[key] !== submittedSeq[key];
    const keepProfiles = hasNewEdits('profiles');
    if (!keepProfiles) {
      model.profiles = freshModel.profiles;
      dirtyFields.profiles = false;
      renderHotkeys();
    }
    if (!hasNewEdits('auto_paste')) {
      model.auto_paste = freshModel.auto_paste;
      dirtyFields.auto_paste = false;
      $('auto-paste').checked = model.auto_paste;
    }
    if (!hasNewEdits('notify_on_complete')) {
      model.notify_on_complete = freshModel.notify_on_complete;
      dirtyFields.notify_on_complete = false;
      $('notify-on-complete').checked = model.notify_on_complete;
    }
    if (!hasNewEdits('max_chars')) {
      model.max_chars = freshModel.max_chars;
      dirtyFields.max_chars = false;
      $('max-chars').value = String(model.max_chars);
    }
    if (!hasNewEdits('idle_unload_minutes')) {
      model.idle_unload_minutes = freshModel.idle_unload_minutes;
      dirtyFields.idle_unload_minutes = false;
      $('idle-unload-minutes').value = String(model.idle_unload_minutes);
    }
    let advanceBaseRevision = !isDirty();
    for (const key of Object.keys(dirtyFields)) {
      if (submittedDirty[key] && hasNewEdits(key)) {
        advanceBaseRevision = true;
      }
    }
    if (advanceBaseRevision) {
      baseRevision = state.revision || 0;
    }
    dirty = isDirty();
    renderAll();
    const warnings = r.data.warnings || [];
    showBanner(warnings.length ? ['Saved, but note:'].concat(warnings) : []);
    setStatus(dirty ? 'Unsaved changes' : 'Saved', dirty ? '' : 'ok');
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
    baseRevision = state.revision || 0;
    model = toModel(state);
    renderAll();
    renderHotkeys();
    $('auto-paste').checked = model.auto_paste;
    $('notify-on-complete').checked = model.notify_on_complete;
    $('max-chars').value = String(model.max_chars);
    $('idle-unload-minutes').value = String(model.idle_unload_minutes);
    inactiveBanner(state.inactive);
    schedulePoll();
    return true;
  }

  function goodbye(text) {
    closed = true;
    dirty = false;
    clearDirty();
    clearTimeout(pollTimer);
    clearInterval(pingTimer);
    document.title = 'Clipboard Translator';
    document.body.replaceChildren(h('main', { class: 'gone' }, h('h1', { text: 'Clipboard Translator' }), h('p', { text }),
      h('p', { text: 'To change settings again, choose Settings... in the tray menu.' })));
  }

  async function init() {
    $('guide').addEventListener('toggle', () => { guideTouched = true; });
    $('add').addEventListener('click', () => {
      model.profiles.push({ ctrl: true, alt: true, shift: false, win: false, key: nextFreeKey(), mode: 'target', a: 'ja', b: 'en', target: 'en', output: 'replace', input: 'selection', cleanup_newlines: 'off' });
      touchField('profiles');
      renderHotkeys();
    });
    $('auto-paste').addEventListener('change', (e) => {
      model.auto_paste = e.target.checked;
      touchField('auto_paste');
    });
    $('notify-on-complete').addEventListener('change', (e) => {
      model.notify_on_complete = e.target.checked;
      touchField('notify_on_complete');
    });
    $('max-chars').addEventListener('input', () => {
      touchField('max_chars');
    });
    $('idle-unload-minutes').addEventListener('input', () => { touchField('idle_unload_minutes'); });
    $('save').addEventListener('click', save);
    $('close').addEventListener('click', async () => {
      if (dirty && !confirm('Close without saving your changes?')) return;
      dirty = false;
      await api('/api/bye', {});
      goodbye('You can close this tab now.');
    });
    window.addEventListener('beforeunload', (e) => { if (dirty) { e.preventDefault(); e.returnValue = ''; } });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && confirmingDelete) {
        confirmingDelete = null;
        renderModels();
      }
    });
    window.addEventListener('focus', async () => {
      if (closed || saving || acting) return;
      const r = await api('/api/state');
      if (r.ok && r.data && r.data.state) {
        mergeState(r.data.state);
      }
    });
    if (!(await load())) { $('save').disabled = true; $('add').disabled = true; return; }
    // The app closes this page's server a few minutes after the last request.
    pingTimer = setInterval(async () => {
      if (closed) return;
      const r = await api('/api/ping', {});
      if (closed) return;
      if (!r.ok && r.status === 0) showNotice(r.data.error);
    }, 60000);
  }

  init();
})();
