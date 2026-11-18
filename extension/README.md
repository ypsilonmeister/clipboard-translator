# Clipboard Translator extension

Manifest V3, Chrome 116+, plain JavaScript/HTML/CSS. Load this directory unpacked in `chrome://extensions` (Developer mode). No dependencies or build step. Follow the ChromeOS setup in the repository README to enable the app endpoint and forward TCP 18121. In extension Options, save the endpoint and per-install token, then test the connection. **Apply connection info** saves the pasted JSON, clears that field, and overwrites the current clipboard with a non-secret confirmation. A clipboard failure is reported separately from the successful save. Overwriting does not guarantee removal of older clipboard history entries; remove any saved token entry yourself. App Settings and the ChromeOS tray offer **Regenerate token** to invalidate a leaked token immediately; apply the new connection info afterwards. The optional `translate_api_extension_id` app config pins the exact extension Origin after an app restart (find the ID in `chrome://extensions`); leave it empty for the unpacked development fallback.

Select text → right-click → **Translate with Clipboard Translator** → wait for the notification → paste. The explicit menu click runs a one-shot script in the clicked frame to read the current selection with its line breaks. The extension has no installed or persistent content scripts. It does not read or monitor the clipboard, use analytics or remote assets, or make automatic requests.

If the script cannot read the selection, such as on Chrome internal pages, the Web Store, the built-in PDF viewer or a cross-origin frame, the extension sends the click event's `selectionText` with `possiblyFlattened: true`. The app skips newline cleanup, paragraph-aware chunking, and bilingual interleaving for that request. A bilingual profile still returns the source followed by the translation. The success notification warns that line breaks may be missing. Test connection deliberately translates “Hello” without writing the clipboard.

The offscreen document performs the local HTTP request and writes the returned translation with `navigator.clipboard.writeText`. This keeps slow inference outside the service worker's fetch-response lifetime limit. It exists only after a context-menu action and does no work while idle. A busy guard prevents simultaneous requests from overwriting each other's results. Failures leave the clipboard unchanged and show a notification. See [Chrome's offscreen documentation](https://developer.chrome.com/docs/extensions/reference/api/offscreen).

Permissions:

- `contextMenus`: the selection menu action.
- `scripting` and `activeTab`: read the selection once in the clicked frame using the temporary access granted by the menu click.
- `storage`: endpoint and token in `chrome.storage.local`, without sync; restricted to trusted extension contexts.
- `offscreen` and `clipboardWrite`: write the result after explicit translation; the Options page also uses `clipboardWrite` to overwrite the clipboard after applying connection info. Neither reads the clipboard.
- `notifications`: success/failure and a short preview.
- `host_permissions`: only HTTP `localhost`, `127.0.0.1`, and `penguin.linux.test`. Chrome match patterns cannot restrict ports, so runtime URL validation further requires exactly `/translate`, no credentials/query/fragment, and fetch rejects redirects. Only the saved endpoint is used. Direct arbitrary container IPs and external URLs are deliberately unsupported; use ChromeOS forwarding (localhost). The named container address (penguin.linux.test) is permitted in host permissions, but direct connections from Chrome may fail.

Icons are resized copies of the application's `assets/icon.png` (16/32/48/128). No new icon artwork or third-party assets were introduced.

In-Chrome end-to-end testing is pending. Check unpacked loading and console errors, test connection, English → Japanese and Japanese → English clipboard/paste, invalid token, stopped app, a slow translation, and repeated clicks. Reload the extension on a real Chromebook and check paragraph breaks, bilingual interleaving, and fallback on a restricted page. No Chrome Web Store distribution is included.
