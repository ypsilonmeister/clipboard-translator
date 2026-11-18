import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import vm from 'node:vm';

const [background, offscreen, endpoint] = await Promise.all(
  ['background.js', 'offscreen.js', 'endpoint.js']
    .map(file => readFile(new URL(file, import.meta.url), 'utf8'))
);

for (const capture of ['selection', 'denied', 'empty']) {
  test(`context-menu translation with ${capture} injection result`, async () => {
    const text = 'First paragraph.\n\nSecond paragraph.';
    const selectionText = 'First paragraph. Second paragraph.';
    const translation = 'First paragraph.\n訳一\n\nSecond paragraph.\n訳二';
    const clipboard = [];
    let click, workerListener, offscreenListener, target, payload, notification;
    let done;
    const finished = new Promise(resolve => { done = resolve; });
    const sender = {id: 'test-extension', url: 'chrome-extension://test-extension/offscreen.html'};

    const sandbox = vm.createContext({
      chrome: {
        runtime: {
          id: sender.id,
          onInstalled: {addListener() {}},
          getContexts: async () => [{}],
          getURL: () => sender.url,
          onMessage: {addListener: fn => { workerListener = fn; }},
          sendMessage: async message => new Promise(resolve => offscreenListener(message, sender, resolve)),
        },
        contextMenus: {onClicked: {addListener: fn => { click = fn; }}},
        scripting: {
          executeScript: async injection => {
            target = injection.target;
            if (capture === 'denied') throw new Error('Cannot access frame');
            return [{result: injection.func()}];
          },
        },
        storage: {local: {get: async () => ({endpoint: 'http://localhost:18121/translate', token: 'tok'})}},
        notifications: {create: async value => { notification = value; done(); }},
      },
      window: {getSelection: () => capture === 'empty' ? '' : text},
    });
    const offscreenSandbox = vm.createContext({
      chrome: {
        runtime: {
          id: sender.id,
          onMessage: {addListener: fn => { offscreenListener = fn; }},
          sendMessage: async message => workerListener(message, sender, () => {}),
        },
      },
      navigator: {clipboard: {writeText: async value => { clipboard.push(value); }}},
      window: {focus() {}},
      AbortController, AbortSignal, URL,
      setInterval: () => 1,
      clearInterval() {},
      fetch: async (_url, request) => {
        payload = JSON.parse(request.body);
        return {ok: true, json: async () => ({translation})};
      },
    });
    vm.runInContext(background, sandbox);
    vm.runInContext(endpoint.replace(/^export /gm, ''), offscreenSandbox);
    vm.runInContext(offscreen.replace(/^import .*;\r?\n/, ''), offscreenSandbox);

    const frameId = capture === 'empty' ? undefined : 7;
    await click({menuItemId: 'translate-selection', selectionText, frameId}, {id: 42});
    await finished;
    assert.equal(target.tabId, 42);
    assert.deepEqual(Array.from(target.frameIds), [frameId ?? 0]);
    assert.deepEqual(payload, capture === 'selection' ? {text} : {text: selectionText, possiblyFlattened: true});
    assert.deepEqual(clipboard, [translation]);
    assert.equal(notification.message.startsWith('Some line breaks may be missing.'), capture !== 'selection');
  });
}
