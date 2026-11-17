import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
const source = await readFile(new URL('./endpoint.js', import.meta.url), 'utf8');
const {connectionInfo, endpointURL, sampleTextFor, translate} = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
test('connection hand-off validates before returning fields', () => {
  assert.deepEqual(connectionInfo('{"endpoint":"http://localhost:18121/translate","token":" abc "}'),
    {endpoint: 'http://localhost:18121/translate', token: 'abc'});
  for (const value of ['no JSON', 'null', '{}', '[]', '{"endpoint":42,"token":"abc"}',
    '{"endpoint":"http://localhost:18121/translate","token":""}',
    '{"endpoint":"https://example.com/translate","token":"abc"}']) {
    assert.throws(() => connectionInfo(value));
  }
});
test('manual endpoints keep their local allowlist', () => {
  for (const host of ['localhost', '127.0.0.1', 'penguin.linux.test']) {
    assert.equal(endpointURL('http://' + host + ':18121/translate'), 'http://' + host + ':18121/translate');
  }
  assert.throws(() => endpointURL('http://example.com/translate'));
});

test('sampleTextFor extracts suitable test text for non-Latin profiles', () => {
  assert.equal(sampleTextFor(''), null);
  assert.equal(sampleTextFor('Invalid translation token'), null);
  assert.equal(sampleTextFor('Failed to fetch'), null);
  assert.equal(sampleTextFor('untranslatable input: Nothing was translated: this hotkey translates Japanese ↔ Chinese (Simplified).'), 'こんにちは');
  assert.equal(sampleTextFor('untranslatable input: Nothing was translated: this hotkey translates Korean ↔ Chinese (Simplified).'), '你好');
  assert.equal(sampleTextFor('untranslatable input: Nothing was translated: this hotkey translates Arabic ↔ Russian.'), 'Привет');
  assert.equal(sampleTextFor('untranslatable input: Nothing was translated: this hotkey translates to Korean.'), '안녕하세요');
  assert.equal(sampleTextFor('untranslatable input: Nothing was translated: this hotkey translates to Russian.'), 'Привет');
});

test('translate respects custom abort signal', async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(
    () => translate('http://localhost:18121/translate', 'token', 'test', controller.signal),
    {name: 'AbortError'}
  );
});
