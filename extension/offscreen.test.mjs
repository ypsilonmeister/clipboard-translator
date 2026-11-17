import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import vm from 'node:vm';

test('a new translation request supersedes an in-flight one without error', async () => {
  let messageListener = null;
  const sentMessages = [];
  const clipboardWrites = [];
  const translateInFlight = [];

  const offscreenSource = (await readFile(new URL('./offscreen.js', import.meta.url), 'utf8'))
    .replace(/^import .*;\n/, '');

  let translateFn = async (endpoint, token, text, signal) => {
    return new Promise((resolve, reject) => {
      translateInFlight.push({text, signal, resolve, reject});
      signal.addEventListener('abort', () => {
        const err = new Error('The operation was aborted');
        err.name = 'AbortError';
        reject(err);
      });
    });
  };

  const sandbox = {
    chrome: {
      runtime: {
        id: 'test-ext-id',
        onMessage: {
          addListener: fn => { messageListener = fn; },
        },
        sendMessage: async msg => { sentMessages.push(msg); },
      },
    },
    navigator: {
      clipboard: {
        writeText: async text => { clipboardWrites.push(text); },
      },
    },
    window: {
      focus: () => {},
    },
    document: {
      createElement: () => ({ focus: () => {}, select: () => {}, remove: () => {} }),
      body: { appendChild: () => {} },
      execCommand: () => true,
    },
    translate: (endpoint, token, text, signal) => translateFn(endpoint, token, text, signal),
    AbortController,
    setInterval: () => 123,
    clearInterval: () => {},
  };

  vm.runInNewContext(offscreenSource, sandbox);
  assert.ok(messageListener, 'listener registered');

  // 1. Send first translation request
  let rep1 = null;
  messageListener(
    {target: 'offscreen', type: 'translate', endpoint: 'http://localhost:18121/translate', token: 'tok', text: 'first'},
    {id: 'test-ext-id'},
    rep => { rep1 = rep; }
  );

  assert.equal(rep1?.accepted, true);
  assert.equal(translateInFlight.length, 1);
  assert.equal(translateInFlight[0].text, 'first');
  assert.equal(translateInFlight[0].signal.aborted, false);

  // 2. While first translation is running, send a second translation request
  let rep2 = null;
  messageListener(
    {target: 'offscreen', type: 'translate', endpoint: 'http://localhost:18121/translate', token: 'tok', text: 'second'},
    {id: 'test-ext-id'},
    rep => { rep2 = rep; }
  );

  assert.equal(rep2?.accepted, true); // Must be accepted, NOT 'A translation is already running.'
  assert.equal(translateInFlight[0].signal.aborted, true); // First request fetch must be aborted
  assert.equal(translateInFlight.length, 2);
  assert.equal(translateInFlight[1].text, 'second');
  assert.equal(translateInFlight[1].signal.aborted, false);

  // 3. Resolve the second request
  translateInFlight[1].resolve('translated second');
  await new Promise(r => setTimeout(r, 10));

  // Verify only the second request wrote to clipboard and notified completion
  assert.deepEqual(clipboardWrites, ['translated second']);
  const doneMessages = sentMessages.filter(m => m.type === 'translation-done');
  assert.equal(doneMessages.length, 1);
  assert.equal(doneMessages[0].ok, true);
  assert.equal(doneMessages[0].text, 'translated second');
});
