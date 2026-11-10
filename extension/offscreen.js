import {translate} from './endpoint.js';
let busy = false;
chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (sender.id !== chrome.runtime.id || message.target !== 'offscreen' || message.type !== 'translate') return;
  if (busy) { respond({error: 'A translation is already running.'}); return; }
  busy = true;
  respond({accepted: true});
  run(message);
});

async function run({endpoint, token, text}) {
  // The offscreen document can survive a long fetch without the worker's fetch
  // response deadline. It never reads or monitors the clipboard.
  const heartbeat = setInterval(() => {
    chrome.runtime.sendMessage({type: 'translation-pending'}).catch(() => {});
  }, 20000);
  let result;
  try {
    const translated = await translate(endpoint, token, text);
    await navigator.clipboard.writeText(translated);
    result = {ok: true, text: translated};
  } catch (error) {
    result = {ok: false, text: error.message || 'Cannot reach the local app. Check app settings and port forwarding.'};
  } finally {
    clearInterval(heartbeat);
    busy = false;
  }
  await chrome.runtime.sendMessage({type: 'translation-done', ...result});
}
