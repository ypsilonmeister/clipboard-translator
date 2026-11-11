# PR #169 bounce-back fix report

Branch: feat/issue-168-chromeos-menu. Changes are uncommitted; no push or PR was made. The pre-existing ct-codex-168-report.md was preserved.

## P1: capability-based profile

Linux startup now introspects the session-bus service org.freedesktop.portal.Desktop at /org/freedesktop/portal/desktop and checks for both org.freedesktop.portal.GlobalShortcuts and org.freedesktop.portal.Clipboard. Missing interfaces, an unreachable bus/service, malformed introspection, or a failed/timed-out call mean the portal workflow is unavailable. The introspection RPC has a two-second timeout and runs before serve, without requesting permissions. The result is a startup snapshot; changing portal packages requires restarting the app.

DesktopStatus.PortalWorkflowUnavailable carries this result for both linuxClipboard (X11) and portalDesktop (Wayland). The app's chromeOSProfile predicate uses that field on Linux. The endpoint menu section, ChromeOS guide and Auto-paste note therefore engage on either backend. The existing Wayland/Manual status and control-window behavior, X11 hotkey/clipboard mechanics and Windows/macOS paths are unchanged. README.md and README.ja.md describe the new condition.

Tests inject a probe into Linux initialization and verify both clipboard status implementations. App tests cover X11 with unavailable portals, both backends with available portals, and Wayland manual mode with unavailable portals. Existing manual-profile fixtures now explicitly supply the capability status.

## P2: Open menu investigation

No missing click/menu connection was found; no speculative GUI change was made.

Verified code path:
1. portalDesktop.connect marks missing required capabilities as manual and invokes showManual.
2. initializePortalDesktop connects showManual to ct_show_control.
3. ct_show_control forces check_tray to create/present the control window.
4. check_tray connects the Open menu button's clicked signal to control_clicked.
5. control_clicked sets menu_requested.
6. PumpMessages calls ct_pump, then ct_wants_menu, calls the registered linuxTray.build callback, builds the GTK menu and invokes ct_menu_popup.
7. ct_menu_popup shows the menu using gtk_menu_popup and gtk_status_icon_position_menu with status_icon.
8. Menu items enqueue commands via menu_activate; PumpMessages transfers these to linuxTray.commands for the app loop.

The popup still relies on legacy tray-icon positioning even on Wayland. This is a possible compatibility issue, not a proven cause of the Crostini failure. A designated Crostini/Sommelier Wayland GUI session should confirm that clicked fires, the flag is consumed, the app's menu builder runs, and GTK maps the popup; capture GTK/GDK warnings, popup position/parent and focus behavior. Also check that activating an item reaches the command queue. No active desktop was inspected, screenshotted or given injected input here. Real Chromebook menu reachability remains unverified.

## P3: ready message

The ready log now uses a hotkey-free sentence when hotkeySummary is empty:
“ready: copy text and choose Translate clipboard, then paste”.
The registered-hotkey sentence remains unchanged. Both forms are unit tested.

## Verification

Environment: PATH prefixed with ~/opt/go-1.27.3/bin and ~/.config/nvm/versions/node/v24.18.1/bin. GOCACHE=/tmp/ct168-go-cache used for repeat checks because the default cache rejected writes.

- `gofmt -l .`: passed, no output.
- `git diff --check`: passed.
- `go vet ./...`: passed after relocating GOCACHE.
- `go test ./internal/app ./internal/config ./internal/platform ./internal/settings`: attempted; app/platform socket-dependent tests blocked by sandbox (`socket: operation not permitted`). The app run stopped at the httptest listener panic in TestPageDownloadsAModelAndTheTrayIsTold, so later app tests were not reached. Config passed; settings has no test files.
- `go test ./internal/config ./internal/settings`: passed (settings has no test files).
- `go test ./internal/platform -skip 'TestCheckServerPort|TestServerDiesWithKilledApp'`: passed. These are the two observed platform failures: the former cannot bind 127.0.0.1:0; the latter's fake-server helper exits before it can test process lifetime.
- `go test -tags portaltest ./internal/platform -skip 'TestCheckServerPort|TestServerDiesWithKilledApp'`: passed. Includes fake portal tests and tagged fixture compilation; native GUI smoke tests remain opt-in and were not enabled.
- `go test ./internal/app -run 'TestChromeOS|TestReadyMessage' -v`: passed. TestChromeOSManualMenu, TestChromeOSMenuEndpointSaveFailure, TestChromeOSProfileCapability and TestReadyMessageWithoutHotkey passed. TestChromeOSMenuEndpointToggle skipped itself because binding 0.0.0.0:18121 is forbidden.
- A broader targeted app run (`-run 'TestChromeOS|TestReadyMessage|TestBuildMenu|TestMenuSettings|TestGuide|TestDesktop'`) was also attempted. Existing socket-backed fixtures prevented menu/guide/desktop tests from reaching their assertions. TestBuildMenuExactLabels was blocked in all 28 variants (seven states × one/three engines × zero/two models); no exact-label expectations were changed. An obsolete manual-only fixture assertion exposed during that run was updated to clear the new capability flag when simulating available portals; the final ChromeOS run above passed.
- Extension files were untouched; node tests were not required/run.
- Full-suite success and live X11/Wayland GUI behavior cannot be claimed in this sandbox.

### Exact blocked test inventory from the attempted runs

The following top-level tests reported socket-related failures (the app run terminated at its first httptest panic). Nested variants are described above or inherited their parent fixture's failure.

- TestUnloadModelAndReloadTranslation
- TestIdleUnloadTimer
- TestServeKeepsIntentionalUnloadIdle
- TestIdleUnloadSettingsRoundTripAndConflicts
- TestIdleUnloadSettingsResetTimer
- TestUnloadReloadFailureStaysUnloaded
- TestSettingsWhileUnloadedAndEngineSwitch
- TestReloadSupersededBySecondHotkeyDuringLoad
- TestReloadSecondHotkeyAfterLoadBehavesAsBefore
- TestReloadCancelDuringLoad
- TestReloadQuitDuringLoad
- TestSettingsEngineSwitchDuringDelayedStart
- TestServeIdleAutomaticUnload
- TestServerWorkResetAfterFailedStartFallbackAndErrQuit
- TestSettingsStateListsModelsAndEngines
- TestCheckModelSwitch
- TestSwitchModelWorksAndIsSaved
- TestSwitchModelFailureRestoresTheOldOneBeforeRestarting
- TestSwitchModelFailureWhenRestoreAlsoFails
- TestServiceSettingsWhileBusyOnlyAnswersStateRequests
- TestPageSwitchesModelThroughTheMainLoop
- TestPageSwitchesEngine
- TestPageDownloadsAModelAndTheTrayIsTold
- TestCheckServerPort
- TestServerDiesWithKilledApp
- TestBuildMenu
- TestBuildMenuExactLabels
- TestDesktopPrimaryDenialDoesNotHideOtherProfiles
- TestDesktopPendingResultGenerationAndCompletion
- TestDesktopPendingResultTimeoutKeepsHistory
- TestDesktopAsyncHistoryAndAbort
- TestDesktopClipboardReadCancellation
- TestDesktopStatusGuideAndSettings
- TestGuideShowsConfiguredHotkey
- TestGuideWithInactiveHotkeyPointsToSettings
- TestGuideShownOnce
- TestGuideInSettingsState
- TestGuideAndSettingsShowActualPortalShortcut
- TestMenuSettingsFirstWithNoActiveHotkeys

TestChromeOSMenuEndpointToggle was a socket-blocked skip, as recorded above. No other functional assertion failures remained in the final focused run.
