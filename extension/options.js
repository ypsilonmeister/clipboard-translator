import {endpointURL, translate, connectionInfo, sampleTextFor} from './endpoint.js';
const endpoint = document.getElementById('endpoint');
const token = document.getElementById('token');
const status = document.getElementById('status');
const test = document.getElementById('test');
chrome.storage.local.get({endpoint: 'http://localhost:18121/translate', token: ''}).then(config => {
  endpoint.value = config.endpoint;
  token.value = config.token;
});
async function save(config) {
  await chrome.storage.local.set(config);
  status.textContent = 'Saved.';
}
document.getElementById('apply-connection').addEventListener('click', async () => {
  try {
    const config = connectionInfo(document.getElementById('connection-info').value);
    await save(config);
    endpoint.value = config.endpoint;
    token.value = config.token;
  } catch (error) { status.textContent = error.message; }
});
document.getElementById('options').addEventListener('submit', async event => {
  event.preventDefault();
  try {
    if (!token.value.trim()) throw new Error('Enter the token from app Settings.');
    await save({endpoint: endpointURL(endpoint.value), token: token.value.trim()});
  } catch (error) { status.textContent = error.message; }
});
test.addEventListener('click', async () => {
  test.disabled = true;
  status.textContent = 'Connecting and translating locally…';
  try {
    const verified = {endpoint: endpointURL(endpoint.value), token: token.value.trim()};
    let result;
    try {
      result = await translate(verified.endpoint, verified.token, 'Hello');
    } catch (error) {
      const sample = sampleTextFor(error.message);
      if (sample) {
        try {
          result = await translate(verified.endpoint, verified.token, sample);
        } catch {
          // Fall through to verify via untranslatable input check below.
        }
      }
      if (!result) {
        const match = error.message.match(/this hotkey translates ([^.]+)/);
        if (match) {
          await chrome.storage.local.set(verified);
          status.textContent = `Connected (endpoint verified for ${match[1]}).`;
          return;
        }
        throw error;
      }
    }
    await chrome.storage.local.set(verified);
    status.textContent = `Connected. Translation: ${result.slice(0, 160)}`;
  } catch (error) { status.textContent = `Connection failed: ${error.message}`; }
  finally { test.disabled = false; }
});
