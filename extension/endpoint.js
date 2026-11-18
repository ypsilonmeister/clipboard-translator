// Keep the runtime destination within the manifest's local host allowlist.
export function endpointURL(value) {
  const url = new URL(value);
  if (url.protocol !== 'http:' || !['localhost', '127.0.0.1', 'penguin.linux.test'].includes(url.hostname) ||
      url.username || url.password || url.hash || url.search || url.pathname !== '/translate') {
    throw new Error('Use http://localhost:18121/translate (or 127.0.0.1).');
  }
  return url.href;
}

export async function translate(endpoint, token, text, signal, possiblyFlattened) {
  if (!token) throw new Error('Set the app token in extension options first.');
  const timeoutSignal = AbortSignal.timeout(46 * 60 * 1000);
  const combinedSignal = signal ? AbortSignal.any([signal, timeoutSignal]) : timeoutSignal;
  const response = await fetch(endpointURL(endpoint), {
    method: 'POST', redirect: 'error', credentials: 'omit', cache: 'no-store',
    headers: {'Content-Type': 'application/json', 'X-Clipboard-Translator-Token': token},
    body: JSON.stringify(possiblyFlattened ? {text, possiblyFlattened} : {text}), signal: combinedSignal,
  });
  let result;
  try {
    result = await response.json();
  } catch (err) {
    if (combinedSignal.aborted) throw err;
    throw new Error(response.ok ? 'App returned an invalid response.' : `App returned HTTP ${response.status}`);
  }
  if (!response.ok) throw new Error(result?.error || `App returned HTTP ${response.status}`);
  if (typeof result.translation !== 'string' || !result.translation.trim()) throw new Error('App returned no translation.');
  return result.translation;
}

const SAMPLES = [
  [/Japanese|\bja\b/i, 'こんにちは'],
  [/Chinese|\bzh\b/i, '你好'],
  [/Korean|\bko\b/i, '안녕하세요'],
  [/Russian|\bru\b/i, 'Привет'],
  [/Belarusian|\bbe\b/i, 'Прывітанне'],
  [/Bulgarian|\bbg\b/i, 'Здравейте'],
  [/Kazakh|\bkk\b/i, 'Сәлем'],
  [/Serbian|\bsr\b/i, 'Здраво'],
  [/Persian|\bfa\b/i, 'سلام'],
  [/Urdu|\bur\b/i, 'ہیلو'],
  [/Hebrew|\bhe\b/i, 'שלום'],
  [/Hindi|\bhi\b/i, 'नमस्ते'],
  [/Marathi|\bmr\b/i, 'नमस्कार'],
  [/Nepali|\bne\b/i, 'नमस्ते'],
  [/Bengali|\bbn\b/i, 'নমস্কার'],
  [/Gujarati|\bgu\b/i, 'નમસ્તે'],
  [/Armenian|\bhy\b/i, 'Բարև'],
  [/Georgian|\bka\b/i, 'გამარჯობა'],
  [/Khmer|\bkm\b/i, 'សួស្តី'],
  [/Burmese|\bmy\b/i, 'မင်္ဂလာပါ'],
  [/Tamil|\bta\b/i, 'வணக்கம்'],
  [/Telugu|\bte\b/i, 'నమస్కారం'],
  [/Punjabi|\bpa\b/i, 'ਸਤਿ'],
  [/Sinhala|\bsi\b/i, 'ආයුබෝවන්'],
  [/Malayalam|\bml\b/i, 'നമസ്കാരം'],
  [/Thai|\bth\b/i, 'สวัสดี'],
];

export function sampleTextFor(errorMessage) {
  if (!errorMessage || !errorMessage.includes('untranslatable input')) return null;
  const match = errorMessage.match(/this hotkey translates ([^.]+)/);
  const target = match ? match[1] : errorMessage;
  for (const [pattern, sample] of SAMPLES) {
    if (pattern.test(target)) return sample;
  }
  return null;
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
