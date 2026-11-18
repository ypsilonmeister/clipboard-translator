import {translate} from './endpoint.js';
let currentController = null;
let heartbeat = null;
chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (sender.id !== chrome.runtime.id || message.target !== 'offscreen' || message.type !== 'translate') return;
  if (currentController) {
    currentController.abort();
  }
  const controller = new AbortController();
  currentController = controller;
  respond({accepted: true});
  run(message, controller);
});

async function run({endpoint, token, text, possiblyFlattened}, controller) {
  // The offscreen document can survive a long fetch without the worker's fetch
  // response deadline. It never reads or monitors the clipboard.
  if (heartbeat) clearInterval(heartbeat);
  heartbeat = setInterval(() => {
    chrome.runtime.sendMessage({type: 'translation-pending'}).catch(() => {});
  }, 20000);
  let result;
  try {
    const translated = await translate(endpoint, token, text, controller.signal, possiblyFlattened);
    if (controller.signal.aborted) return;
    await writeClipboard(translated, controller.signal);
    if (controller.signal.aborted) return;
    result = {ok: true, text: translated};
  } catch (error) {
    if (controller.signal.aborted) return;
    result = {ok: false, text: error.message || 'Cannot reach the local app. Check app settings and port forwarding.'};
  } finally {
    if (currentController === controller) {
      clearInterval(heartbeat);
      heartbeat = null;
      currentController = null;
    }
  }
  if (controller.signal.aborted) return;
  await chrome.runtime.sendMessage({type: 'translation-done', ...result, possiblyFlattened});
}

async function writeClipboard(text, signal) {
  if (signal?.aborted) return;
  try {
    window.focus();
    await navigator.clipboard.writeText(text);
    return;
  } catch {
    if (signal?.aborted) return;
    const textarea = document.createElement('textarea');
    textarea.value = text;
    try {
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      if (signal?.aborted) return;
      if (!document.execCommand('copy')) throw new Error('Clipboard write failed.');
    } finally {
      textarea.remove();
    }
  }
}
