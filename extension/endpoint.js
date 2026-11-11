// Keep the runtime destination within the manifest's local host allowlist.
export function endpointURL(value) {
  const url = new URL(value);
  if (url.protocol !== 'http:' || !['localhost', '127.0.0.1', 'penguin.linux.test'].includes(url.hostname) ||
      url.username || url.password || url.hash || url.search || url.pathname !== '/translate') {
    throw new Error('Use http://localhost:18121/translate (or 127.0.0.1 / penguin.linux.test).');
  }
  return url.href;
}

export async function translate(endpoint, token, text) {
  if (!token) throw new Error('Set the app token in extension options first.');
  const response = await fetch(endpointURL(endpoint), {
    method: 'POST', redirect: 'error', credentials: 'omit', cache: 'no-store',
    headers: {'Content-Type': 'application/json', 'X-Clipboard-Translator-Token': token},
    body: JSON.stringify({text}), signal: AbortSignal.timeout(46 * 60 * 1000),
  });
  let result;
  try {
    result = await response.json();
  } catch {
    throw new Error(response.ok ? 'App returned an invalid response.' : `App returned HTTP ${response.status}`);
  }
  if (!response.ok) throw new Error(result?.error || `App returned HTTP ${response.status}`);
  if (typeof result.translation !== 'string' || !result.translation.trim()) throw new Error('App returned no translation.');
  return result.translation;
}

export function connectionInfo(value) {
  let info;
  try { info = JSON.parse(value); }
  catch { throw new Error('Invalid connection info: paste the JSON from the app menu.'); }
  if (!info || typeof info.endpoint !== 'string' || typeof info.token !== 'string' || !info.token.trim()) {
    throw new Error('Invalid connection info: endpoint and token are required.');
  }
  return {endpoint: endpointURL(info.endpoint), token: info.token.trim()};
}
