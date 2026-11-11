import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import vm from 'node:vm';

test('apply saves valid connection info and leaves fields/storage intact on malformed input', async () => {
  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, {value: '', textContent: '', addEventListener(type, fn) { this[type] = fn; }});
    return elements.get(id);
  };
  const saved = [];
  const endpointSource = await readFile(new URL('./endpoint.js', import.meta.url), 'utf8');
  const api = await import('data:text/javascript;base64,' + Buffer.from(endpointSource).toString('base64'));
  const options = (await readFile(new URL('./options.js', import.meta.url), 'utf8')).replace(/^import .*;\n/, '');
  vm.runInNewContext(options, {
    ...api, document: {getElementById: get},
    chrome: {storage: {local: {
      get: async () => ({endpoint: 'http://localhost:18121/translate', token: 'old'}),
      set: async config => { saved.push(config); },
    }}},
  });
  await Promise.resolve();
  get('connection-info').value = '{"endpoint":"http://localhost:18121/translate","token":"new"}';
  await get('apply-connection').click();
  assert.equal(get('token').value, 'new');
  assert.equal(saved.length, 1);
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
});
