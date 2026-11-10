# Clipboard Translator extension

Manifest V3, Chrome 116+, plain JavaScript/HTML/CSS. Load this directory unpacked in `chrome://extensions` (Developer mode). No dependencies or build step. Follow the ChromeOS setup in the repository README to enable the app endpoint and forward TCP 18121. In extension Options, save the endpoint and per-install token, then test the connection.

Select text → right-click → **Translate with Clipboard Translator** → wait for the notification → paste. The click event's `selectionText` is the only input. No content scripts, clipboard reads, analytics, remote assets, automatic requests, or background clipboard monitoring are installed. Test connection deliberately translates “Hello” without writing the clipboard.

The offscreen document performs the local HTTP request and writes the returned translation with `navigator.clipboard.writeText`. This keeps slow inference outside the service worker's fetch-response lifetime limit. It exists only after a context-menu action and does no work while idle. A busy guard prevents simultaneous requests from overwriting each other's results. Failures leave the clipboard unchanged and show a notification. See [Chrome's offscreen documentation](https://developer.chrome.com/docs/extensions/reference/api/offscreen).

Permissions:

- `contextMenus`: the selection menu action.
- `storage`: endpoint and token in `chrome.storage.local`, without sync; restricted to trusted extension contexts.
- `offscreen` and `clipboardWrite`: write the result after explicit translation, without reading the clipboard.
- `notifications`: success/failure and a short preview.
- `host_permissions`: only HTTP `localhost`, `127.0.0.1`, and `penguin.linux.test`. Chrome match patterns cannot restrict ports, so runtime URL validation further requires exactly `/translate`, no credentials/query/fragment, and fetch rejects redirects. Only the saved endpoint is used. Direct arbitrary container IPs and external URLs are deliberately unsupported; use ChromeOS forwarding or the named container address.

Icons are resized copies of the application's `assets/icon.png` (16/32/48/128). No new icon artwork or third-party assets were introduced.

In-Chrome end-to-end testing is pending. Check unpacked loading and console errors, test connection, English → Japanese and Japanese → English clipboard/paste, invalid token, stopped app, a slow translation, and repeated clicks. No Chrome Web Store distribution is included.
