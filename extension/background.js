const menuID = 'translate-selection';
let creating;

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({id: menuID, title: 'Translate with Clipboard Translator', contexts: ['selection']});
  });
  // Keep the per-install secret out of content script contexts.
  chrome.storage.local.setAccessLevel({accessLevel: 'TRUSTED_CONTEXTS'});
});

async function offscreen() {
  const contexts = await chrome.runtime.getContexts({contextTypes: ['OFFSCREEN_DOCUMENT']});
  if (contexts.length) return;
  if (!creating) {
    creating = chrome.offscreen.createDocument({url: 'offscreen.html', reasons: ['CLIPBOARD'],
      justification: 'Copy the user-requested local translation to the clipboard.'});
  }
  try { await creating; } finally { creating = null; }
}

async function notify(ok, text) {
  await chrome.notifications.create({type: 'basic', iconUrl: 'icons/128.png',
    title: ok ? 'Translation copied — paste with Ctrl+V' : 'Translation failed',
    message: text.replace(/\s+/g, ' ').slice(0, 160)});
}

chrome.contextMenus.onClicked.addListener(async (info) => {
  if (info.menuItemId !== menuID) return;
  try {
    if (!info.selectionText?.trim()) throw new Error('Select some text first.');
    const config = await chrome.storage.local.get({endpoint: 'http://localhost:18121/translate', token: ''});
    await offscreen();
    const result = await chrome.runtime.sendMessage({target: 'offscreen', type: 'translate', ...config, text: info.selectionText});
    if (!result?.accepted) throw new Error(result?.error || 'Could not start translation.');
  } catch (error) { await notify(false, error.message); }
});

chrome.runtime.onMessage.addListener((message, sender, respond) => {
  if (sender.id !== chrome.runtime.id || sender.url !== chrome.runtime.getURL('offscreen.html')) return;
  if (message.type === 'translation-done') {
    notify(message.ok, message.text).catch(() => {});
    respond({ok: true});
  } else if (message.type === 'translation-pending') {
    // Offscreen messages keep the worker available during slow local inference.
    respond({ok: true});
  }
});
