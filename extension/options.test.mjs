import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import vm from 'node:vm';

async function optionsHarness({failSave = false, failClipboard = false} = {}) {
  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, {value: '', textContent: '', addEventListener(type, fn) { this[type] = fn; }});
    return elements.get(id);
  };
  const saved = [], writes = [], events = [];
  const endpointSource = await readFile(new URL('./endpoint.js', import.meta.url), 'utf8');
  const api = await import('data:text/javascript;base64,' + Buffer.from(endpointSource).toString('base64'));
  const options = (await readFile(new URL('./options.js', import.meta.url), 'utf8')).replace(/^import .*;\r?\n/, '');
  vm.runInNewContext(options, {
    ...api, document: {getElementById: get},
    navigator: {clipboard: {writeText: async text => {
      events.push('clipboard');
      if (failClipboard) throw new Error('Clipboard denied');
      writes.push(text);
    }}},
    chrome: {storage: {local: {
      get: async () => ({endpoint: 'http://localhost:18121/translate', token: 'old'}),
      set: async config => {
        events.push('save');
        if (failSave) throw new Error('Storage failed');
        saved.push(config);
      },
    }}},
  });
  await Promise.resolve();
  return {get, saved, writes, events};
}

const validInfo = '{"endpoint":"http://localhost:18121/translate","token":"new"}';

test('apply saves then overwrites the clipboard, clears pasted info and rejects malformed input', async () => {
  const {get, saved, writes, events} = await optionsHarness();
  get('connection-info').value = validInfo;
  await get('apply-connection').click();
  assert.equal(get('token').value, 'new');
  assert.equal(saved.length, 1);
  assert.equal(writes.length, 1);
  assert.equal(writes[0], 'Clipboard Translator connection info applied.');
  assert.deepEqual(events, ['save', 'clipboard']);
  assert.equal(get('connection-info').value, '');
  assert.match(get('status').textContent, /Clipboard overwritten.*clipboard history/);
  get('connection-info').value = '{"endpoint":"http://example.com/translate","token":"bad"}';
  await get('apply-connection').click();
  assert.equal(get('token').value, 'new');
  assert.equal(saved.length, 1);
  assert.match(get('status').textContent, /Use http/);
  get('connection-info').value = '{';
  await get('apply-connection').click();
  assert.match(get('status').textContent, /Invalid connection info/);
  assert.equal(saved.length, 1);
  await get('options').submit({preventDefault() {}});
  assert.equal(saved.length, 2);
  assert.equal(writes.length, 1, 'invalid info and manual Save do not touch the clipboard');
});


test('clipboard failure preserves successful save and gives recovery instructions', async () => {
  const {get, saved, writes} = await optionsHarness({failClipboard: true});
  get('connection-info').value = validInfo;
  await get('apply-connection').click();
  assert.equal(saved.length, 1);
  assert.equal(get('token').value, 'new');
  assert.equal(get('connection-info').value, '');
  assert.equal(writes.length, 0);
  assert.match(get('status').textContent, /Saved, but could not overwrite.*Copy other text.*clipboard history/);
});

test('failed storage never overwrites the clipboard or changes connection fields', async () => {
  const {get, saved, writes, events} = await optionsHarness({failSave: true});
  get('connection-info').value = validInfo;
  await get('apply-connection').click();
  assert.equal(saved.length, 0);
  assert.equal(writes.length, 0);
  assert.deepEqual(events, ['save']);
  assert.equal(get('token').value, 'old');
  assert.equal(get('connection-info').value, validInfo);
  assert.equal(get('status').textContent, 'Storage failed');
});
