import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
const source = await readFile(new URL('./endpoint.js', import.meta.url), 'utf8');
const {connectionInfo, endpointURL} = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
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
