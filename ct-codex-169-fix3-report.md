# PR #169 fix3 report

Base: `feat/issue-168-chromeos-menu`, HEAD `80c0725`. Changes remain in the working tree. No commits, pushes, PR creation, merges, or publishing were performed. Pre-existing untracked binary and earlier reports were left alone.

## Translation settings binding and choices

- `internal/app/app_desktop.go:startClipboardTranslation` copies `a.conf.Profiles[0]`, overrides only its input to clipboard, and calls `translateCaptured`. Its language pair or target therefore comes from profile zero.
- `internal/app/translate_api.go:handleAPITranslation` defaults to index zero when POST `/translate` omits `profile`. An explicit configured hotkey or zero-based index still selects another profile; this existing API contract is unchanged. `translateCaptured` uses that profile's `Check`/direction, output, and newline cleanup. The extension's usual request uses the default profile.
- The ChromeOS Languages section edits **profile zero**. It exposes Between two languages / Into one language and the relevant selectors. It contains no key binding, Add/Remove, Text from, Output, or Line breaks controls, and no inactive-hotkey banner. The one-line note is: “Hotkeys are not available on ChromeOS; these language settings apply to translation.” Other Settings sections remain available, including the existing AutoPasteUnavailable behavior.
- Capability detection remains `chromeOSProfile()` (Linux plus PortalWorkflowUnavailable), covering both X11 and Wayland. Normal Linux, Windows, and macOS keep their existing hotkey cards, labels, controls, and save behavior.
- Replaced `HotkeysUnavailable` with the presentation flag `LanguageOnly`; removed the old disabled-fieldset/note code. Hidden hotkey failure state does not offer a retry through Save in this profile.

## Lossless saving

The app validates the selected languages using the existing settings/config validation. It ignores submitted changes to every hidden control and every other profile, retains the existing in-memory bindings and non-language fields, and does not close or re-register hotkeys for a ChromeOS language save.

`config.UpdateTranslationSettings` patches JSON value spans instead of marshaling the profiles array. In a configuration with explicit profiles, only profile zero's `languages` and `target` values change; absent language fields are inserted as necessary. Pair/target switching uses `languages: null` or `target: ""` for the inactive field, both accepted by the existing config loader. Every other byte—including binding spelling, whitespace, unknown fields, other profile objects, and top-level legacy hotkey/language values—is retained. Viewing does not write the file. A save with no actual changes does not write it either. Other global settings are patched only when changed; `.bak` keeps the original bytes and a BOM is preserved.

Legacy configurations without a profiles array continue to update their top-level language pair. Choosing a target in such a file materializes the single effective profile because the legacy format cannot represent a target; existing hotkey text is retained. Missing configuration files can also be saved.

## Control window status

At `app.updateReadyTip`, ChromeOS uses `Clipboard Translator - ready` without any binding, regardless of an active X11 shortcut. The same `readyTip` feeds the control window/tray status. Normal profiles still use `Clipboard Translator - ready (Alt+Shift+E)` for that binding. ChromeOS model-unloaded wording says “next translation loads the model,” while existing Wayland authorization/error status retains precedence. Both READMEs describe the new Settings and ready presentation.

## Tests changed or added

Only the two fix2 test files were rewritten. No other pre-existing tests were modified.

- `internal/app/chromeos_hotkeys_test.go`: replaced disabled-Hotkeys assertions with `TestChromeOSLanguageSettings` (X11/Wayland and capability on/off; state construction does not mutate config); added `TestChromeOSLanguageSavePreservesHotkeys` (multiple profiles, unknown fields, byte-exact file comparison, submitted hidden-field tampering ignored, reload validates the target, and a separate global save); added `TestChromeOSReadyTip` at the actual composition method, including unchanged normal Alt+Shift+E and ChromeOS unloaded wording.
- `internal/settings/hotkeys_test.go`: replaced disabled-fieldset checks with `TestLanguageOnlyRendering`. Node executes the actual rendering functions using a small DOM stub, toggles normal → ChromeOS → normal, checks card count and forbidden controls, and drives both language and translation-mode changes. This checks the Settings code, not a screenshot or browser layout.
- **New file** `internal/config/translation_settings_test.go`: `TestUpdateTranslationSettings` covers target-to-pair and legacy pair saves, exact bytes, BOM, backup, and reloading; `TestUpdateTranslationSettingsMissingFile` covers creating a valid target configuration. Added because the new byte-preserving writer has behavior beyond the app integration fixture. No existing config tests were edited.

## Verification

PATH prefix: `$HOME/opt/go-1.27.3/bin:$HOME/.config/nvm/versions/node/v24.18.1/bin` (Go 1.27.3, Node 24.18.1). Used `GOCACHE=/tmp/ct-fix3-go-cache` after the default cache rejected writes. Final commands use non-login shells to avoid unrelated user-session startup hooks.

The sandbox **cannot run socket-dependent suites**: even loopback listener creation fails with `socket: operation not permitted`. Full-suite invocations are unfiltered, but panics in socket fixtures prevent some later tests from running. Passing every pre-existing test cannot be claimed here.

| Check | Result |
| --- | --- |
| `gofmt -l .` | PASS, empty output |
| `go vet ./...` | PASS, empty output; `/tmp/ct-fix3-verified-vet.log` |
| HEAD snapshot `go test ./... -count=1` | Sandbox failure; 27 named failures, five failing packages; `/tmp/ct-fix3-final-baseline.log` |
| Final tree `go test ./... -count=1` | Same 27 named failures and five failing packages, no added/removed named failures; `/tmp/ct-fix3-verified-test.log` |
| `go test -tags portaltest -run '^TestPortal' ./internal/platform -count=1` | PASS; `/tmp/ct-fix3-final-portal.log` |
| Focused ChromeOS, language-rendering, JSON preservation, and existing Settings save-guard tests | PASS in app/settings/config after final code changes |
| `node --check internal/settings/web/app.js` | PASS; actual renderer assertions also pass under Node |
| `git diff --check` | PASS |
| Extension `node --test` | Not required; extension files untouched |

The five full-suite failing packages are app, llamaserver, models, platform, and update. Config and Settings full suites pass, as do the other tested packages. Baseline uses an independent `git archive HEAD` snapshot at `/tmp/ct-fix3-baseline`. An earlier full run additionally failed existing `TestServerDiesWithKilledApp` because its fake server exited before the helper was killed; that environment/timing-dependent failure was absent from both the final run and baseline. No platform source was changed.

`go test -race ./internal/app ./internal/platform ./internal/config ./internal/settings -count=1`: app/platform fail on sandbox-denied socket fixtures; config/settings PASS. No data-race warnings were reported. Log: `/tmp/ct-fix3-verified-race.log`.

The focused race run also passes in app/settings/config: `go test -race ./internal/app ./internal/settings ./internal/config -run 'TestChromeOS|TestLanguageOnly|TestUpdateTranslationSettings|TestSettingsPageKeepsStaleSaveGuards' -count=1`. Log: `/tmp/ct-fix3-verified-focused-race.log`.

## Coordinator native reruns

On the native coordinator, with the same PATH prefix, run:

```sh
gofmt -l .
go vet ./...
go test ./... -count=1
go test -tags portaltest -run '^TestPortal' ./internal/platform -count=1
go test -race ./internal/app ./internal/platform ./internal/config ./internal/settings -count=1
```

Real ChromeOS/desktop UI acceptance was not performed. No desktop was accessed, photographed, or sent keys. On the designated Chromebook, confirm Languages edits change extension/default endpoint and Translate clipboard translation direction, the saved multi-profile bindings remain intact, the ready window omits the binding, and normal Linux still displays its binding. Node DOM assertions do not establish visual layout or native window behavior.
