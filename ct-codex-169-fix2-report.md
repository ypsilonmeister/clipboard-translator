# PR #169 additions: popup anchor and ChromeOS Settings Hotkeys

Branch: `feat/issue-168-chromeos-menu`; starting HEAD: `9972d01`.
Changes are uncommitted. No push, PR creation, merge, or release was performed.
Existing untracked `clipboard-translator`, `ct-codex-168-report.md`, and `ct-codex-168-fix-report.md` were left untouched.

## Changes

- `internal/platform/native_linux.c`: distinguish control-button requests from status-icon requests with one C flag. Control-window requests use `gtk_menu_popup_at_widget` anchored below the control window on both X11 and Wayland. Status-icon activation and right-click explicitly select the existing, unchanged `gtk_status_icon_position_menu` call. The flag survives `ct_wants_menu` consuming the request and is reset after popup and tray teardown. No Go/C API changes or tray restructuring.
- `internal/settings/server.go` and `internal/app/app_settings.go`: add state-only `HotkeysUnavailable` / `hotkeys_unavailable`, populated solely by the existing capability-based `chromeOSProfile()` predicate, alongside `AutoPasteUnavailable`. No Form or config fields or save-path changes.
- Settings HTML/JS/CSS: a native fieldset contains all Hotkeys profile controls and Add. `renderBusy` disables the fieldset and shows exactly: “Not available on ChromeOS: hotkeys cannot be triggered from Chrome or other host apps.” Absent flag re-enables the section and hides/clears the note. Fieldset disabling also applies to rebuilt controls and survives Add button state refreshes. Border/padding/margin/minimum width are reset to preserve normal-page layout. Saved profiles stay intact, including those used by the extension.
- README.md and README.ja.md describe the disabled Settings controls in this capability profile.

## Tests added / modified

**No pre-existing test was modified, deleted, weakened, or skipped by a code change.**

New tests:

- `internal/app/chromeos_hotkeys_test.go`, `TestChromeOSHotkeysUnavailable`: X11/Wayland × capability profile on/off; exact note; JSON flag present only when on; obtaining state leaves configuration byte-for-byte unchanged when serialized.
- `internal/settings/hotkeys_test.go`, `TestHotkeysUnavailableRendering`: verify both profile container and Add reside within the native fieldset; execute the actual `renderBusy` function with Node for profile off → on → off, checking disabled state and note text/visibility. Uses DOM stubs; browser-native fieldset disabling and visual layout are not real-browser tested.

Popup-anchor selection remains entirely in C. **Code inspection only** verifies control clicks set the window-anchor flag, both status-icon callbacks clear it, the consumer preserves it until popup, and the original status-icon positioning call is unchanged. Native C compilation is exercised by Go test/vet. Actual popup coordinates on ChromeOS/X11/Wayland and normal desktop tray clicks require real-device acceptance; no desktop screenshots or key injection were performed. A Go-side seam was not introduced solely to test this small C branch.

## Verification environment and baseline

Commands prefixed PATH with `$HOME/opt/go-1.27.3/bin:$HOME/.config/nvm/versions/node/v24.18.1/bin` (Go 1.27.3 linux/amd64, Node v24.18.1). `GOCACHE=/tmp/ct169-go-cache` was used because the sandbox rejects writes to the normal cache.

An initial pre-edit baseline attempt (`/tmp/ct169-baseline.log`) encountered both read-only normal cache and denied socket operations. A writable-cache baseline was also launched before editing; to avoid any possible overlap with edits during compilation, the authoritative comparison below uses an independent `git archive HEAD` snapshot at `/tmp/ct169-original`, with the same command and writable cache.

The reported all-green baseline **cannot be reproduced inside this sandbox**: loopback listeners fail with `socket: operation not permitted`; fake-server/subprocess checks consequently fail too. No escalation is available. These are existing environment failures, not test changes. Full-suite invocations were made without filtering, but socket-related panics stop some packages before later tests execute; therefore no claim that every pre-existing test passed is possible here.

| Check | Result |
| --- | --- |
| `gofmt -l .` | PASS, empty output (`/tmp/ct169-gofmt.log`) |
| `go vet ./...` | PASS, empty output (`/tmp/ct169-vet.log`) |
| Original `9972d01`: `go test ./... -count=1` | FAIL due to sandbox; 28 named failures, five failing packages (`/tmp/ct169-original-baseline.log`) |
| Working tree: `go test ./... -count=1` | Same 28 named failures and five failing packages; no added or removed named failures (`/tmp/ct169-final.log`) |
| `go test -tags portaltest -run '^TestPortal' ./internal/platform -count=1` | PASS (`/tmp/ct169-portal.log`) |
| `go test ./internal/settings -count=1` | PASS, including Node rendering test |
| `go test ./internal/app -run '^TestChromeOSHotkeysUnavailable$' -count=1` | PASS (`/tmp/ct169-new-app.log`) |
| `go test ./internal/app -run '^(TestChromeOS\|TestSettingsPageKeepsStaleSaveGuards)' -count=1` | PASS, including unchanged ChromeOS and Settings JavaScript/save-guard tests (`/tmp/ct169-focused.log`) |
| `go test -race ./internal/app ./internal/platform ./internal/config -count=1` | FAIL: app/platform hit sandbox-denied sockets; config PASS. No data-race warnings reported (`/tmp/ct169-race.log`) |
| `go test -race ./internal/settings ./internal/app -run '^(TestHotkeysUnavailableRendering\|TestChromeOSHotkeysUnavailable)$' -count=1` | PASS for both new tests (`/tmp/ct169-new-race.log`) |
| Extension `node --test` | Not required: extension files untouched; Settings JS was executed by Node through Go tests |
| `git diff --check` | PASS |

Full-suite failing packages in both baseline and working tree: `internal/app`, `internal/llamaserver`, `internal/models`, `internal/platform`, `internal/update`. Other pre-existing tested packages pass; `internal/settings` now also passes its new test.

### Matching named baseline/final failures

- `TestUnloadModelAndReloadTranslation`
- `TestIdleUnloadTimer`
- `TestServeKeepsIntentionalUnloadIdle`
- `TestIdleUnloadSettingsRoundTripAndConflicts`
- `TestIdleUnloadSettingsResetTimer`
- `TestUnloadReloadFailureStaysUnloaded`
- `TestSettingsWhileUnloadedAndEngineSwitch`
- `TestReloadSupersededBySecondHotkeyDuringLoad`
- `TestReloadSecondHotkeyAfterLoadBehavesAsBefore`
- `TestReloadCancelDuringLoad`
- `TestReloadQuitDuringLoad`
- `TestSettingsEngineSwitchDuringDelayedStart`
- `TestServeIdleAutomaticUnload`
- `TestServerWorkResetAfterFailedStartFallbackAndErrQuit`
- `TestSettingsStateListsModelsAndEngines`
- `TestCheckModelSwitch`
- `TestSwitchModelWorksAndIsSaved`
- `TestSwitchModelFailureRestoresTheOldOneBeforeRestarting`
- `TestSwitchModelFailureWhenRestoreAlsoFails`
- `TestServiceSettingsWhileBusyOnlyAnswersStateRequests`
- `TestPageSwitchesModelThroughTheMainLoop`
- `TestPageSwitchesEngine`
- `TestPageDownloadsAModelAndTheTrayIsTold`
- `TestTranslatePayloadBothWays`
- `TestDownloadRetriesOnlyTemplate`
- `TestCheckServerPort`
- `TestServerDiesWithKilledApp`
- `TestFetchLatest`
