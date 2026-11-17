# Issue #174: Translate API token hardening

Implemented on `feat/issue-174-token-rotation` in the supplied worktree, based on `22c1030`. Read the [full issue](https://github.com/ypsilonmeister/clipboard-translator/issues/174) and `docs/DESIGN_PHILOSOPHY.md` before implementation. The issue has no comments.

## 1. Token regeneration

- Added **Regenerate token** to Settings and the ChromeOS connection-actions tray menu.
- Both actions use the same UI-thread operation: generate 32 random bytes, save the hex token with `config.UpdateFile`, then publish it atomically to the running HTTP handler. No app configuration is read from HTTP goroutines. A failed save leaves the current credentials working.
- The listener stays running. Settings displays the new token beside the endpoint URL; the tray action copies fresh connection-info JSON and explains the clipboard warning. Rotation while disabled does not enable the endpoint.
- The existing config save path retains unknown fields and creates `config.json.bak`. Previously authenticated requests may finish; subsequent requests using an old token return **401**.

Evidence:

- `TestTranslateAPIRotateTokenLiveListener` starts the production listener and handler on loopback port 18121. It uses real HTTP POSTs and the actual UI request channel, with only inference replaced by a deterministic reply. The original token first returns **200**. After each of two Settings regeneration actions, the preceding token returns **401** and the new token returns **200** with a translation response, on the same server object and endpoint. It also verifies disk persistence, the previous-token backup, unknown-key preservation, origin pinning, and continued acceptance of the current token after a failed save.
- Confirmed that regression test detects the original problem: temporarily removing the live token update made it fail because the old token reached inference after regeneration. Restoring the update made it pass.
- `TestChromeOSRegenerateTokenCommand` simulates ChromeOS capability state on Windows, invokes the actual tray command, verifies the new copied JSON and saved token, and calls the real handler to assert old **401** / new **200** without replacing the server. A failed tray save leaves both token and clipboard unchanged and reports an error.
- `TestTranslationAPIRotateActionRoute` exercises the real Settings HTTP route, including refusal without the Settings token. `TestTranslateAPIRotateDisabledAndUnsupported` covers disabled and unsupported-platform behavior.

## 2. Extension hand-off hygiene

- Chose the requested **clipboard-overwrite route**. The extension already has `clipboardWrite` permission, so this uses `navigator.clipboard.writeText` without extra permissions, clipboard reads, dependencies, or a changed pairing workflow. See [Chrome's permission reference](https://developer.chrome.com/docs/extensions/reference/permissions-list).
- After **Apply connection info** successfully saves the configuration, it clears the pasted JSON field and overwrites the current clipboard with `Clipboard Translator connection info applied.`
- A failed overwrite reports that saving succeeded and instructs the user to copy other text. Invalid connection info or a failed storage write never overwrites the clipboard.
- Updated the tray warning, Settings copy warning/help, extension Options help/status, and both READMEs consistently. Overwriting the current clipboard cannot guarantee deletion of previously retained ChromeOS or third-party clipboard history. The text explicitly asks the user to remove any saved token entry; this change does not claim to purge OS history.

Evidence: Node tests execute the actual options script with DOM, storage, and clipboard fakes. They assert save-before-overwrite ordering, the non-secret replacement, pasted-field clearing, invalid-input behavior, storage failure, and clipboard denial with recovery instructions. All five extension tests pass.

## 3. Optional extension origin pinning

- Added optional `translate_api_extension_id` in `config.json`, validated as exactly 32 lowercase a-p characters when nonempty. Find the ID in `chrome://extensions`; restart the app after manually editing this setting.
- When configured, only the exact `chrome-extension://<ID>` Origin is accepted. Other extension IDs, web origins, malformed origins, and missing Origin receive **403**, even with a valid token.
- When omitted or empty, the existing development fallback remains: any syntactically valid extension ID is allowed, and native clients without Origin remain supported. Token authentication remains required in all cases. Native clients can forge Origin headers, so pinning supplements token authentication.

Evidence: `TestTranslateAPIExtensionPin` covers pinned and development modes against the real handler; `TestLoadTranslateAPIExtensionID` covers valid, empty, malformed, wrong-length, uppercase, and out-of-alphabet IDs. The production-listener rotation test additionally verifies that pinning remains active after rotation.

## Verification

All commands below passed on the Windows x64 SSH host, with the full suite rerun after the final test additions:

```text
go test ./... -skip '^TestProcessIsPerMonitorDPIAware$' -count=1
go test -race ./internal/app ./internal/settings ./internal/config -run 'TestTranslateAPI|TestTranslationAPI|TestLoadTranslateAPI|TestConnectionCopy|TestChromeOSRegenerateTokenCommand' -count=1
node --test extension/endpoint.test.mjs extension/options.test.mjs
go vet ./...
gofmt -l .
git diff --check
go build -trimpath -buildvcs=false -ldflags "-H=windowsgui -s -w" -o <TEMP>/ct-issue174.exe .
```

`gofmt -l .` printed nothing. The only explicitly excluded test was the requested `TestProcessIsPerMonitorDPIAware`; existing platform/build-tag skips still apply. The Linux non-cgo app test binary also cross-compiled successfully outside the repository (`GOOS=linux`, `CGO_ENABLED=0`, `go test -c ./internal/app`).

Validation boundary: no real desktop input or screenshots were used. Native Linux/ChromeOS tray rendering, real extension clipboard permissions/history, and Chromebook end-to-end translation were not exercised. WSL Ubuntu could not start (`Wsl/Service/E_UNEXPECTED`), so the Linux-specific menu-order test was not executed here; the portable tray-command test and real HTTP rotation checks did run. Build/test artifacts were written outside the repository. Coordinator-provided untracked files were left untouched.
