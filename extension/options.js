import {endpointURL, translate, connectionInfo} from './endpoint.js';
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
    const result = await translate(endpoint.value, token.value.trim(), 'Hello');
    status.textContent = `Connected. Translation: ${result.slice(0, 160)}`;
  } catch (error) { status.textContent = `Connection failed: ${error.message}`; }
  finally { test.disabled = false; }
});
