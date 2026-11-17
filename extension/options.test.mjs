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

test('test connection succeeds and saves credentials for English and non-Latin profiles', async () => {
  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, {value: '', textContent: '', addEventListener(type, fn) { this[type] = fn; }});
    return elements.get(id);
  };
  const saved = [];
  const calls = [];
  let translateHandler = async (ep, tok, text) => {
    calls.push({ep, tok, text});
    return 'Translated: ' + text;
  };
  const endpointSource = await readFile(new URL('./endpoint.js', import.meta.url), 'utf8');
  const api = await import('data:text/javascript;base64,' + Buffer.from(endpointSource).toString('base64'));
  const options = (await readFile(new URL('./options.js', import.meta.url), 'utf8')).replace(/^import .*;\n/, '');

  vm.runInNewContext(options, {
    ...api,
    translate: async (ep, tok, text, sig) => translateHandler(ep, tok, text, sig),
    document: {getElementById: get},
    chrome: {storage: {local: {
      get: async () => ({endpoint: 'http://localhost:18121/translate', token: 'saved_token'}),
      set: async config => { saved.push(config); },
    }}},
  });
  await Promise.resolve();

  // 1. Success on initial 'Hello'
  get('endpoint').value = 'http://localhost:18121/translate';
  get('token').value = 'valid_token';
  await get('test').click();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].text, 'Hello');
  assert.equal(saved.length, 1);
  assert.equal(saved[0].endpoint, 'http://localhost:18121/translate');
  assert.equal(saved[0].token, 'valid_token');
  assert.match(get('status').textContent, /^Connected\. Translation: Translated: Hello/);

  // 2. Non-Latin profile returns 422 for 'Hello', retries with appropriate sample text and succeeds
  calls.length = 0;
  saved.length = 0;
  translateHandler = async (ep, tok, text) => {
    calls.push({ep, tok, text});
    if (text === 'Hello') {
      throw new Error('untranslatable input: Nothing was translated: this hotkey translates Japanese ↔ Chinese (Simplified).');
    }
    return '你好';
  };
  await get('test').click();
  assert.equal(calls.length, 2);
  assert.equal(calls[0].text, 'Hello');
  assert.equal(calls[1].text, 'こんにちは');
  assert.equal(saved.length, 1);
  assert.equal(saved[0].endpoint, 'http://localhost:18121/translate');
  assert.equal(saved[0].token, 'valid_token');
  assert.match(get('status').textContent, /^Connected\. Translation: 你好/);

  // 3. Genuine failure (e.g. invalid token) reports accurate failure and does not save
  calls.length = 0;
  saved.length = 0;
  translateHandler = async (ep, tok, text) => {
    calls.push({ep, tok, text});
    throw new Error('Invalid translation token');
  };
  await get('test').click();
  assert.equal(calls.length, 1);
  assert.equal(saved.length, 0);
  assert.equal(get('status').textContent, 'Connection failed: Invalid translation token');
});
