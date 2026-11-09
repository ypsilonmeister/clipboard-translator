# Package split — design pass (Issue #104, step 1)

> **Status: proposal, no code moved.** This document is the deliverable of step 1
> of Issue #104. It settles nothing by itself: every layout choice below is a
> proposal for the maintainer, and the decisions that need an owner are collected
> in [Open questions](#6-open-questions). Existing explicit requirements in
> `AGENTS.md` (one UI thread, generation counter, clipboard safety, no network on
> its own, dependency-free `go.mod`) still apply to every step.

The repository root is one `package main`. At the commit this was written against
it holds 106 Go files (54 non-test, 52 test) and 5 native files
(`native_linux.c`, `native_linux.h`, `native_darwin.m`, `native_darwin.h`,
`native_darwin_testbridge.m`). The Issue quotes 92 files (49 + 43); `main` has
grown since, which does not change the conclusions.

How the inventory below was produced: a throwaway program (not committed) parsed
every root `.go` file with `go/parser`, **ignoring build tags**, mapped each
top-level identifier to the file that declares it, and listed every identifier a
file mentions that another group declares. The host was Windows, so the
Linux/macOS files were read but not compiled here. Two known sources of noise,
both filtered by hand below: (a) a struct field or method with the same name as a
package-level identifier (`Config.Hotkey` vs. the `Hotkey` interface, `main`) and
(b) identifiers that are declared once per OS (`newHotkey`, `newTray`, …), which
the scan attributes to whichever per-OS file it parsed last. Anything marked
"false positive" was checked against the source.

---

## 1. Current-state coupling inventory

### 1.1 Groups

Group names follow the brief. "Defines (used elsewhere)" lists unexported names
that other groups reference; "Uses" lists unexported names taken from other
groups. Test files are assigned in section 2.

| Group | Files |
| --- | --- |
| **platform-common** | `tray.go`, `tray_other.go`, `clipboard.go`, `capture.go`, `platform.go`, `platform_other.go`, `hotkey_nonwindows.go`, `proc_other.go`, `open_other.go`, `data_other.go` |
| **platform-Windows** | `tray_windows.go`, `hotkey_windows.go`, `clipboard_windows.go`, `autostart_windows.go`, `proc_windows.go`, `open_windows.go`, `platform_windows.go`, `update_windows.go` (the installer/registry half of the update check) |
| **platform-Linux** | `desktop_linux.go`, `native_linux.c`, `native_linux.h`, `clipboard_linux.go`, `clipboard_linux_nocgo.go`, `hotkey_linux_registry.go`, `autostart_linux.go`, `proc_linux.go`; test-only: `desktop_linux_testbridge.go` (`x11test`) |
| **platform-Darwin** | `desktop_darwin.go`, `native_darwin.m`, `native_darwin.h`, `clipboard_darwin.go`, `clipboard_native_darwin.go`, `autostart_darwin.go`, `proc_darwin.go`, `data_darwin.go`, `setup_ui_darwin_nocgo.go`; test-only: `desktop_darwin_testbridge.go`, `native_darwin_testbridge.m` (`mactest`) |
| **server supervisor** | `server.go`, `leftover.go`, `leftover_other.go` |
| **Settings server / web** | `settings_server.go`, `settings_web/{index.html,app.css,app.js}`, `app_settings.go` (methods on `*app`, so really app glue — see 1.3) |
| **models / catalog** | `models.go`, `modeldl.go`, `app_models.go` (methods on `*app`, app glue) |
| **config** | `config.go`, `settings.go` (order-preserving `config.json` edits, backend detection) |
| **languages** | `languages.go` |
| **update check** | `update.go`, `update_other.go`, (`update_windows.go`, listed under Windows) |
| **setup** | `setup_flow.go`, `psscript.go` |
| **app glue** | `app.go`, `app_idle.go`, `app_menu.go`, `app_models.go`, `app_settings.go`, `guide.go`, `history.go`, `main.go` |

Per-OS files exist as build-tagged *variants of the same names*. This is the
single most important structural fact for the platform step: `Hotkey` (the
interface) is **declared four times**, in `hotkey_windows.go`,
`hotkey_nonwindows.go`, `desktop_linux.go` and `desktop_darwin.go`, each under
its own build tag. `newHotkey`, `foregroundWindow`, `sendPaste`,
`clipboardSequence`, `beginSelectionCapture`, `captureSelectedText`,
`waitModifiersReleased`, `newClipboard`, `newTray`, `pumpMessages`, `showError`,
`showInfo`, `askYesNo`, `openTarget`, `platformRuntimeError`, `runSetup`,
`hideConsole`, `bindToParentLifetime`, `startFallbackEditor`, `writableAppDir`
are likewise one function with up to four implementations selected by file
suffix/tag. That set is the de-facto platform API already; the split makes it
explicit.

### 1.2 Per-group coupling

Only cross-group edges are listed. "Noise" entries are omitted.

**platform-common** (`tray.go`, `clipboard.go`, `capture.go`, `platform.go`, stubs)

- Defines, used by others: `Tray`, `MenuItem`, `firstAppCommand`, `noopTray`
  (app, Windows/Linux/Darwin tray code, tests); `Clipboard`,
  `sameClipboardText`, `normalizeClipboardText`, `classifyClipboardDiff`
  (app, all clipboard impls, `main.go` `applyTranslation`); `captureJob`,
  `captureTask`, `captureOps`, `primaryOps`, `newCaptureJob`, `runCapture`,
  `captureWith`, `captureWithPrimary`, `errCaptureModifiersHeld`,
  `errCaptureFocusChanged` (app, Windows/Linux/Darwin desktop code);
  `maxHotkeys`, `maxWindowsHotkeys`, `hotkeyLimit`, `serverBinaryName`,
  `setupScriptName`, `pasteShortcut`, `completionNotification`,
  `vcRuntimeAvailable` (app, config, setup, supervisor, tests).
- Uses: nothing from app logic. Its per-OS siblings supply the 20-odd functions
  listed above.
- Defect to fix on the way: `serverBinaryName` (`platform.go`) is called by
  `config.go` (`defaultConfig`) and `settings.go` (`availableBackends`,
  `backendSettings`), so **config depends on platform**.

**platform-Windows / Linux / Darwin**

- Define: the per-OS implementations above, plus per-OS private helpers
  (`windowsHotkey`, `winTray`, `winClipboard`, `autostartStore`,
  `newKillOnCloseJob`, `hotkeyRegistry`, `parseX11Hotkey`, `parseMacHotkey`,
  `withCString`, `boolInt`, …) that nothing outside their group uses. Exceptions:
  `autostartStore` is also used by `update_windows.go`
  (`installRecord`); `pump`/`pumpHotkeys` are used only inside the Windows group.
- Use from other groups:
  - from **config**: `parseFunctionKey`, `parseHotkeySpec` (Windows),
    `parseHotkeyNames` (Linux), `parseFunctionKey` (Darwin) — the shared hotkey
    grammar lives in `config.go` (`hotkeySpec`, `parseFunctionKey`,
    `parseHotkeyNames`, `parseHotkeySpec`, `validateHotkey`).
    **This is a platform → config edge, and the config → platform edge above
    makes a cycle** if both become packages (see 2.4).
  - from **supervisor**: `proc_darwin.go` uses `childRecord`,
    `encodeChildRecord`, `parseChildRecord`, `isLeftoverServer`
    (`leftover.go`).
  - from **setup**: `proc_windows.go` uses `setupPowerShell`,
    `cleanPowerShellEnv` (`psscript.go`); `update_windows.go` uses
    `installerPowerShell`, `installerEnv`; `proc_darwin.go` uses
    `beginSetupProgress`/`endSetupProgress`/`setupCancelled`, which are defined in
    `desktop_darwin.go` and stubbed by `setup_ui_darwin_nocgo.go`.
  - from **app**: `desktop_darwin.go` and `desktop_linux.go` `pumpMessages` build
    the tray menu from `MenuItem` (platform-common) only; the app command IDs are
    passed through `Tray.SetMenu`. No real dependency on app logic. (The scan's
    "result" and "main" hits are false positives: `result` is a local name.)

**server supervisor** (`server.go`, `leftover.go`)

- Defines, used elsewhere: `supervisor`, `restartLimiter`, `errQuit`,
  `restartBackoff`, `dllNotFoundError`, `isDLLNotFound`, `exitCodeIsDLLNotFound`,
  `checkServerPort`, `childRecordPath` (app, `app_models.go`).
- Uses: `waitReady` (`main.go`), `vcRuntimeAvailable` (platform.go),
  `hideConsole`, `bindToParentLifetime` (proc_*.go), `recordChild`, `forgetChild`
  (`proc_darwin.go` / `leftover_other.go`). `leftover.go`'s `childRecord` is
  consumed by `proc_darwin.go`, so the macOS process code depends on a file that
  looks like supervisor code but is really platform process bookkeeping.
- Tests: `server_key_test.go` also uses `translate` and `translationDirection`
  (`main.go`).

**Settings server / web** (`settings_server.go`, `app_settings.go`)

- `settings_server.go` defines `settingsServer`, `startSettingsServer`,
  `settingsRequest`, `settingsReply`, `settingsState`, `engineState`,
  `modelState`, `settingsForm`, `profileForm`, `newSettingsRequest`,
  `newActionRequest`, `errSettingsBusy`, `formFromProfile`, `languageOptions`,
  `winKeyName`, `(settingsForm).toConfig` — used by `app_settings.go`,
  `app_models.go`, `app.go` and many tests.
- Uses: `Config`, `Profile`, `parseHotkeyNames`, `updateConfigFile`,
  `validateIdleUnloadMinutes`, `validateMaxChars` (config); the `languages`
  map directly (`languageOptions`); `downloadManager`, `downloadState`
  (modeldl.go); `hotkeyLimit` (platform); `plamoLicenseURL` (update.go);
  **`quickstartGuide`, defined in `guide.go` (app)**. That last edge points from
  the Settings server back into app glue and has to be cut by moving the type.
- It embeds the web assets: `//go:embed settings_web` (`settings_server.go`).

**models / catalog** (`models.go`, `modeldl.go`)

- Leaf code: `models.go` and `modeldl.go` mention nothing from other groups
  (stdlib only). Defined and used elsewhere: `modelEntry` and its methods
  (`path`, `installed`, `partialInfo`, `fileInfo`, `templatePath`,
  `installedTemplatePath`, `templateInstalled`), `modelCatalog`, `modelByKey`,
  `modelForFile`, `isCustomTemplate`, `templateInstalled`, `verifyFileSha256`,
  `isRemovableFile`, `downloadManager` and its methods, `downloadState`,
  `downloadTransport`. All unexported names that app/settings/tests need would
  become exported.
- `app_models.go` is not part of this group in substance: it is methods on
  `*app` (`switchModel`, `startModelSwitch`, `handleDeleteModelRequest`, …) and
  uses `settingsState`, `settingsRequest`, `engineState`, `modelState`,
  `errSettingsBusy`, `availableBackends`, `currentBackend`, `resolvePaths`,
  `checkFile`, `errQuit`, `version`, `pathWithin`. It stays with app.

**config** (`config.go`, `settings.go`)

- Defines, used elsewhere: `Config`, `Profile`, `loadConfig`, `defaultConfig`,
  `updateConfigFile`, `parseConfigEntries`, `validateHotkey`/`validateHotkeyFor`,
  `validateMaxChars`, `validateIdleUnloadMinutes`, `parseHotkeyNames`,
  `parseHotkeySpec`, `parseFunctionKey`, `hotkeySpec`, `availableBackends`,
  `currentBackend`, `backendSettings`, `backendNames`, `OutputReplace`,
  `OutputBilingual`, `InputSelection`, `InputClipboard`.
- Uses: `languages` (`defaultLanguages`, `languageCodes`, `validateLanguages`,
  the `languages` map), `serverBinaryName` (platform), `checkFile`
  (`setup_flow.go`), `pathWithin` (`main.go`; used from `config_test.go`,
  `setup_flow.go`, `app_models.go`).
- `Profile` is *declared* in `config.go` but its methods `check`, `skipReason`,
  `direction`, `label` are declared in `main.go` (and `inputMode`/`outputMode`
  in `config.go`). Go requires methods in the type's package, so these move with
  `Profile`. They in turn use `skipReason` (a type in `main.go`),
  `translationDirection` (`main.go`) and `directionFor`/`isURL` (languages).
- `parseHotkeyNames` and `validateHotkey` branch on `runtime.GOOS` (config.go),
  i.e. config already encodes platform knowledge.

**languages** (`languages.go`)

- Defines, used elsewhere: `directionFor`, `validateLanguages`, `languageName`,
  `pairLabel`, `languageCodes`, `samePair`, `isURL`, `defaultLanguages`, the
  `languages` map (read directly by `settings_server.go`).
- Uses: `translationDirection`, which is declared in `main.go`
  (`type translationDirection struct{ Source, Target string }`). Everything else
  is stdlib. A one-type move makes it a leaf.

**update check** (`update.go`, `update_other.go`, `update_windows.go`)

- Defines, used elsewhere: `version` (var, `-X main.version`), `releaseInfo`,
  `fetchLatest`, `isNewerVersion`, `compareVersions`, `versionParts`,
  `detectInstallKind`, `installKind`/`kindInstaller`/`kindScoop`,
  `installScriptURL`, `plamoLicenseURL`, `releasesPage`, `latestReleaseAPI`;
  per-OS `recordedInstallLocation`, `runInstaller`.
- Uses: `autostartStore`, `createNewConsole` (Windows), `installerPowerShell`,
  `installerEnv` (setup/`psscript.go`).
- `update.go` itself is platform-free; only `update_windows.go` /
  `update_other.go` are platform code.

**setup** (`setup_flow.go`, `psscript.go`)

- Defines: `appPaths`, `resolvePaths`, `checkFile`, `ensureInstalled`,
  `ensureSetupScript`, `setupContents`, `embeddedSetup`, `embeddedLinuxSetup`,
  `embeddedMacSetup`, `setupProgressDescription`; and in `psscript.go`
  `setupPowerShell`, `installerPowerShell`, `cleanPowerShellEnv`, `installerEnv`,
  `psFailurePrompt`.
- Uses: `fatal`, `pathWithin` (`main.go`), `Config`/`loadConfig` (config),
  `isCustomTemplate`, `modelForFile`, `templateInstalled` (models),
  `setupScriptName` (platform), `askYesNo`, `runSetup` (platform).
- Used by: `resolvePaths`/`appPaths` by app, models/app tests and
  `supervisor`'s tests; `psscript.go` by Windows platform code and the update
  check.
- Contains the three `//go:embed` of the root scripts (`setup.ps1`, `setup.sh`,
  `setup-macos.sh`).

**app glue** (`app.go`, `app_idle.go`, `app_menu.go`, `app_models.go`,
`app_settings.go`, `guide.go`, `history.go`, `main.go`)

- `app` is one struct whose methods are spread across all of these files, so
  they cannot be separated from each other.
- Uses almost everything: config, languages, models/downloads, platform
  (`Hotkey`, `newHotkey`, `newTray`, `newClipboard`, `foregroundWindow`,
  `sendPaste`, `captureSelectedText`, `beginSelectionCapture`, `showError`,
  `askYesNo`, `openTarget`, `startFallbackEditor`, `pumpMessages`,
  `platformRuntimeError`, `writableAppDir`, `hotkeyLimit`, `pasteShortcut`,
  `completionNotification`), supervisor (`supervisor`, `restartLimiter`,
  `errQuit`, `checkServerPort`, `childRecordPath`, `restartBackoff`), update
  (`fetchLatest`, `detectInstallKind`, `runInstaller`, `version`, …), settings
  server (`settingsServer`, `settingsRequest`, `settingsIdle`), setup
  (`appPaths`, `resolvePaths`, `ensureInstalled`).
- `main.go` additionally *defines* things that belong elsewhere:
  `translationDirection` (languages), `Profile` methods and `skipReason`
  (config), `pathWithin` (paths/config), `waitReady` and `translate` (the HTTP
  client to the loopback `llama-server`, used by the supervisor and its tests),
  `appDir`, `fatal`, `result`, `applyTranslation`, `formatBilingual`.
- `app` is created by `newApp(dir, conf, paths, tray, serverLog)`; every OS
  function above enters `app` through that one constructor or through
  `app.run`/`app.serve`.

### 1.3 Test seams named in the Issue

All six replaceable seams are **unexported fields of `*app`**, assigned once in
`newApp` (`app.go`) from the per-OS function of the same purpose:

| Seam | Field on `app` | Default | Per-OS provider | Test files using it (count of lines) |
| --- | --- | --- | --- | --- |
| `newHotkey` | `newHotkey func(spec string) (Hotkey, error)` | `newHotkey` | `hotkey_windows.go`, `desktop_linux.go`, `desktop_darwin.go`, `hotkey_nonwindows.go` | 9 files, 50 lines |
| `capture` | `capture func(cb Clipboard, win uintptr) (string, bool, error)` | `captureSelectedText` | same four files | 7 files, 21 lines |
| `beginCapture` | `beginCapture func(cb, win) captureTask` | `beginSelectionCapture` | Windows and macOS implement it; Linux and the stubs return nil (the app then uses `capture`) | `app_test.go`, `capture_async_test.go` |
| `paste` | `paste func(target uintptr) error` | `pasteIntoForeground` (`app.go`, wraps `sendPaste`) | `sendPaste` per OS | 4 files, 8 lines |
| `foreground` | `foreground func() uintptr` | `foregroundWindow` | per OS | 5 files, 8 lines |
| `openURL` | `openURL func(target string) error` | `openTarget` | `tray_*.go` / `desktop_*.go` | `open_test.go`, `settings_page_test.go` |
| `showErr` | `showErr func(msg string)` | `showError` | same | `app_test.go`, `app_idle_test.go` |
| (also) | `askYesNo`, `fallbackEditor`, `now`, `dl` | `askYesNo`, `startFallbackEditor`, `time.Now` | per OS | `app_test.go`, `open_test.go` |

Consequence: the seams survive a platform split **unchanged** as long as `app`
and its tests stay in one package. Only the right-hand side of the assignment in
`newApp` changes (`newHotkey` → `platform.NewHotkey`). That is the reason the
split is feasible as a mechanical series.

`fakeTray` is declared in `helper_test.go`, implements `Tray`, and is used by 9
test files (`app_idle_test.go`, `app_models_test.go`, `app_test.go`,
`app_ux_test.go`, `capture_async_test.go`, `lifecycle_test.go`,
`settings_page_test.go`, `state_consistency_test.go`, `helper_test.go`). Same
for `fakeHotkey`. Both are tests of `app` and stay with it; they need `Tray`,
`MenuItem` and `Hotkey` to be importable (exported) from the platform package.

The **fake `llama-server`** is the test binary itself:

- `helper_test.go` `TestMain` checks `CT_TEST_FAKE_SERVER=1` and then runs
  `runFakeServer()` instead of the tests (flags it honours: `--port`, a model
  path containing `FAIL`, `CT_TEST_FAKE_EXIT_CODE`, `CT_TEST_FAKE_SERVER_DELAY`,
  `LLAMA_API_KEY`, answer text `fakeTranslation`).
- Tests start it by copying `os.Executable()` into a temp
  `runtime/<backend>/llama-server` (`newTestApp` in `app_test.go`).
- Users of `CT_TEST_FAKE_SERVER`: `app_test.go`, `app_idle_test.go`,
  `state_consistency_test.go`, `job_windows_test.go`, `proc_linux_test.go`,
  plus `helper_test.go`. Several of these are *platform* tests
  (`job_windows_test.go` exercises the kill-on-close job object;
  `proc_linux_test.go` `hideConsole` + pdeathsig, whose `init` also relies on
  the same re-exec trick) and `server_test.go`/`server_key_test.go` are
  *supervisor* tests. After a split, **each of those packages needs its own
  `TestMain` that can act as the fake server**. A Go test binary cannot borrow
  another package's `TestMain`.
- `TestMain` also sets `downloadTransport = loopbackOnly{}` (`modeldl.go`) and
  overrides the package variable `vcRuntimeAvailable` (`platform.go`) so tests
  never touch the real Hugging Face host or depend on the VC++ runtime.
  Those two package-level variables are read in other packages after the split.
- `llama_smoke_test.go` (`llamasmoke` tag) uses `supervisor` against a real
  binary.

### 1.4 Things the scan surfaced that the split has to handle

1. **`go:embed` cannot reach a parent directory.** `setup_flow.go` embeds
   `setup.ps1`, `setup.sh`, `setup-macos.sh`; `settings_server.go` embeds
   `settings_web`; `desktop_linux.go` embeds `assets/icon.png`. The first three
   files must stay at the repository root (one-line installer, flat zip, tools,
   READMEs). See 3.3.
2. **Two tests exercise the linked Windows resources**
   (`tray_windows_test.go`: `TestAppIconIsEmbedded`,
   `TestProcessIsPerMonitorDPIAware`). They pass only in a test binary that has
   `resource_windows_amd64.syso` linked in, i.e. the package that *holds the
   `.syso`*. If the tray moves to `internal/platform` and the `.syso` stays in
   the main package, these two tests fail in the platform test binary. They have
   to stay in (or move to) the package next to the `.syso`.
   `TestProcessIsPerMonitorDPIAware` is also the one that fails in non-interactive
   sessions (Appendix A).
3. **Profile/skip logic lives in `main.go`.** See config above.
4. **Cycles.** config → platform (`serverBinaryName`, `checkFile`), platform →
   config (hotkey grammar), platform → supervisor (`childRecord`), Settings
   server → app (`quickstartGuide`), supervisor → app (`waitReady`, `translate`).
   Each is removable by moving one declaration, listed in 2.4.
5. **Tests that cross groups** (listed per file in section 2) and tests that read
   repo files by relative path: `setup_script_test.go` (`setup.ps1`,
   `setup-macos.sh`), `modeldl_test.go` (`setup.ps1`), `models_test.go`
   (`tools/print_model_pin.py`), `chat_template_test.go` (`testdata/…`),
   `install_script_test.go` (`install.ps1`, via `filepath.Abs(".")`). Their
   relative paths all assume the package directory is the repository root.

---

## 2. Proposed package layout

Module path is `github.com/ypsilonmeister/clipboard-translator`, `go 1.21`,
standard library only. All new packages go under `internal/` so no API is
promised to outside importers. Dependency direction (an arrow means "imports"):

```
main ──► internal/app ──► internal/settings ──► internal/config ──► internal/hotkeyspec
            │   │   │            │   │                  │
            │   │   │            │   └► internal/models │
            │   │   └► internal/update ─► internal/platform
            │   └► internal/llamaserver ─► internal/platform, internal/languages
            └► internal/platform ◄── internal/config (ServerBinaryName only)
internal/config ──► internal/languages, internal/hotkeyspec
internal/platform ──► internal/hotkeyspec
```

(`internal/platform` imports nothing else from the module except
`hotkeyspec`; `internal/hotkeyspec` and `internal/languages` import nothing.)

### 2.1 Packages

| # | Package | Responsibility (one sentence) |
| --- | --- | --- |
| P1 | `internal/languages` | The 53 model languages and which direction a language pair translates a given text. |
| P2 | `internal/hotkeyspec` | Parse and validate hotkey strings such as `Ctrl+Alt+E`, with no OS dependency. |
| P3 | `internal/config` | `config.json` schema, validation, in-place edits, profiles, backend detection, and path resolution. |
| P4 | `internal/models` | The pinned model catalog and the download manager. |
| P5 | `internal/platform` | Everything that differs per OS: tray, hotkeys, clipboard, selection capture, autostart, child-process lifetime, data dir, installer/registry bits, native cgo bridges. |
| P6 | `internal/update` | "Check for updates": release lookup, version compare, install-kind detection. |
| P7 | `internal/llamaserver` | The `llama-server` child: supervisor, restart limiter, readiness wait, and the loopback HTTP translate call. |
| P8 | `internal/settings` | The loopback-only Settings HTTP server, its request/state types and the embedded page. |
| P9 | `internal/app` | The `app` struct and main loop; everything that wires the others together. |
| — | `main` | Entry point (`main()` only), see section 3. |

### 2.2 Files that move, exported API, tests

Signatures are sketches: names may be settled in the step PR, but the *shape*
(what must become exported) is determined by section 1. "Was" gives the current
unexported name so reviewers can check that a PR is a pure rename.

#### P1 `internal/languages`

- Files: `languages.go` → `internal/languages/languages.go`.
- Type move: `translationDirection` (`main.go`) → `languages.Direction`.
- API:
  ```go
  type Direction struct{ Source, Target string }               // was main.translationDirection
  type Language struct{ Code, Name string }                    // new; replaces direct read of the `languages` map
  var Default = []string{"ja", "en"}                           // was defaultLanguages
  func Name(code string) string                                // was languageName
  func PairLabel(pair []string) string                         // was pairLabel
  func Validate(in []string) ([]string, error)                 // was validateLanguages
  func Codes() []string                                        // was languageCodes
  func IsURL(s string) bool                                    // was isURL
  func DirectionFor(pair []string, s string) (Direction, bool) // was directionFor
  func SamePair(a, b []string) bool                            // was samePair
  func All() []Language                                        // new; settings_server.go languageOptions() sorts it
  ```
  `All()` is the one genuinely new function; the rest are renames. If the
  maintainer wants zero additions in the first PR, `languageOptions()` can keep
  reading the data through a `languages.Each(func(code, name string))`
  — either way it needs *some* accessor.
- Seams: none.
- Tests: `languages_test.go` splits. Tests of `directionFor`/`validateLanguages`
  move to `internal/languages`; the part that calls `loadConfig` and
  `settingsState` (config round-trip through the page) stays in `internal/app`
  (or `internal/settings`) until those exist. `main_test.go` (`Profile.check`,
  `skipReason`) uses `defaultLanguages`, `directionFor`, `sameClipboardText`
  and moves to `internal/config` (see P3) with the `Profile` methods.

#### P2 `internal/hotkeyspec`

- Files: the hotkey grammar cut out of `config.go` (`hotkeySpec`,
  `parseFunctionKey`, `parseHotkeySpec`, `parseHotkeyNames`, `validateHotkey`,
  `validateHotkeyFor`). Lines 196-303 of `config.go` at the time of writing.
- Why a package of its own: it is the only way to remove the platform ↔ config
  cycle without changing behaviour (2.4). Its API is small:
  ```go
  type Spec struct{ Ctrl, Alt, Shift, Win bool; Key string }   // was hotkeySpec (fields exported)
  func ParseFunctionKey(s string) (int, bool)                  // was parseFunctionKey
  func Parse(s string, allowSuper bool) (Spec, error)          // was parseHotkeySpec
  func ParseNames(s string, allowSuper, allowMac bool) (Spec, error) // was parseHotkeyNames
  func Validate(goos, s string) error                          // was validateHotkeyFor
  ```
  (`validateHotkey` — the one reading `runtime.GOOS` — stays in `config` as
  `func ValidateHotkey(s string) error { return hotkeyspec.Validate(runtime.GOOS, s) }`.)
- Tests: the parse/validate cases of `config_test.go`.
- This package is optional: see open question Q4 for the alternative (keep the
  grammar in `config` and let platform import `config`).

#### P3 `internal/config`

- Files: `config.go`, `settings.go`; from `main.go`: `Profile` methods
  (`check`, `skipReason`, `direction`, `label`), `skipReason` type + `String`,
  `skipMessage`, `skipStatus`, `pathWithin`; from `setup_flow.go`: `appPaths`,
  `resolvePaths`, `checkFile`.
- Imports: `languages`, `hotkeyspec`, `platform` (for `ServerBinaryName()` only).
- API (renames plus exports of the names app/settings/models need):
  ```go
  type Config struct{ … }                    // already exported; field set unchanged
  type Profile struct{ … }
  const OutputReplace, OutputBilingual, InputSelection, InputClipboard
  func Load(dir string) (Config, error)                      // was loadConfig
  func Default() Config                                      // was defaultConfig
  func UpdateFile(dir string, set map[string]any) error      // was updateConfigFile
  func (c *Config) NormalizeProfiles()                       // was normalizeProfiles
  func (c *Config) ResolveProfiles(explicit bool) error      // was resolveProfiles
  func (p Profile) InputMode() string; OutputMode() string   // was inputMode/outputMode
  func (p Profile) Check(s string, maxChars int) (SkipReason, languages.Direction)
  func (p Profile) Direction(s string, maxChars int) (languages.Direction, bool)
  func (p Profile) Label() string
  type SkipReason int; func (r SkipReason) String() string
  func SkipMessage(r SkipReason, count, limit int, label string) string
  func SkipStatus(r SkipReason, label string) string
  func ValidateHotkey(s string) error
  func ValidateMaxChars(n int) error; ValidateIdleUnloadMinutes(n int) error
  func AvailableBackends(dir string) []string
  func CurrentBackend(serverFile string) string
  func BackendSettings(name string) (serverFile string, gpuLayers int)
  var BackendNames = []string{"cpu","cuda","vulkan","metal"}
  func PathWithin(base, p string) (string, error)            // was pathWithin
  type Paths struct{ Model, Template, Server, Fallback string } // was appPaths
  func ResolvePaths(dir string, c Config) (Paths, error)
  func CheckFile(path string) error
  ```
  `Config.explicitProfiles` is an unexported field that `app_settings.go:287`
  copies (`a.conf.explicitProfiles = cand.explicitProfiles`) and several tests
  set directly (`profiles_test.go`, `state_consistency_test.go`); it needs an
  exported field or accessor, a small non-mechanical addition.
- Seams: none. `config_test.go` writes real temp `config.json` files.
- Tests: `config_test.go` (minus the `formFromProfile`/`settingsForm` cases
  that belong to P8), `settings_test.go` (backend detection; it uses
  `serverBinaryName` and `formFromProfile` — the latter case moves to P8),
  `main_test.go`.

#### P4 `internal/models`

- Files: `models.go`, `modeldl.go` (no outward dependencies).
- API:
  ```go
  type Entry struct{ Key, Name, Note, File, Repo, Revision, Sha256, TemplateSha256, SizeLabel string; MinBytes, Bytes int64 }
  var Catalog []Entry                                          // was modelCatalog
  func ByKey(key string) (Entry, bool); func ForFile(modelFile string) (Entry, bool)
  func (m Entry) Path(dir string) string                       // was path
  func (m Entry) Installed(dir string) bool
  func (m Entry) PartialInfo(dir string) (bool, int64); FileInfo(dir string) (bool, int64)
  func (m Entry) TemplateFile() string; TemplatePath(dir string) string
  func (m Entry) InstalledTemplatePath(dir string) (string, bool); TemplateInstalled(dir string) bool
  func TemplateInstalled(dir string) bool; IsCustomTemplate(templateFile string) bool
  func VerifyFileSha256(path, expected string) bool; IsRemovableFile(os.FileInfo) bool
  type DownloadState struct{ Key, Phase string; Done, Total int64; Error string }
  type Manager struct{ … }                                     // was downloadManager
  func NewManager(dir string) *Manager
  func (m *Manager) State() DownloadState; Downloading(key string) bool; ClearKey(key string)
  func (m *Manager) ReserveDelete(key string) (func(), error); Start(key string) error; Cancel(); Close()
  func (m *Manager) Events() <-chan DownloadState              // new accessor for today's `dl.events` field
  var DownloadTransport http.RoundTripper                      // was downloadTransport (set by tests)
  ```
- `internal/models` must remain **free of platform and settings imports**: the
  header comment in `modeldl.go` says the manager "belongs to the app, not to the
  settings server" and is "the only code that contacts the model host"; keeping
  it a leaf keeps the privacy claim in README auditable from one package.
- Seams: `downloadTransport` becomes the exported `DownloadTransport`; every test
  package that downloads sets it in `TestMain`.
- Tests: `modeldl_test.go`, `models_test.go` (`../../setup.ps1`,
  `../../tools/print_model_pin.py` after the move). `chat_template_test.go` and
  `app_models_test.go` use `*app` and stay with P9;
  `app_models_windows_test.go` too (Windows file-lock test via `modelByKey`).

#### P5 `internal/platform`

- Files that move (all of them keep their build tags and suffixes):
  - common: `tray.go`, `tray_other.go`, `clipboard.go`, `capture.go`,
    `platform.go`, `platform_other.go`, `hotkey_nonwindows.go`, `proc_other.go`,
    `open_other.go`, `data_other.go`, `leftover.go`, `leftover_other.go`,
    `psscript.go`, `update_other.go`;
  - Windows: `tray_windows.go`, `hotkey_windows.go`, `clipboard_windows.go`,
    `autostart_windows.go`, `proc_windows.go`, `open_windows.go`,
    `platform_windows.go`, `update_windows.go`;
  - Linux: `desktop_linux.go`, `native_linux.c`, `native_linux.h`,
    `clipboard_linux.go`, `clipboard_linux_nocgo.go`, `hotkey_linux_registry.go`,
    `autostart_linux.go`, `proc_linux.go`, `desktop_linux_testbridge.go`;
  - Darwin: `desktop_darwin.go`, `native_darwin.m`, `native_darwin.h`,
    `native_darwin_testbridge.m`, `clipboard_darwin.go`,
    `clipboard_native_darwin.go`, `autostart_darwin.go`, `proc_darwin.go`,
    `data_darwin.go`, `setup_ui_darwin_nocgo.go`, `desktop_darwin_testbridge.go`.
  The cgo sources **must** sit next to the Go files that `import "C"`; this is
  why one directory, not `platform/linux`, `platform/darwin`.
- Decision recorded here, not hidden: one package, not three. A package per OS
  would need an interface layer and per-OS registration (a design change). One
  package keeps files byte-identical (a `git mv` plus a `package` line), which is
  what "mechanical" requires.
- De-duplicating the `Hotkey` interface: today it is declared four times, once
  per build-tagged file. In the package it should be declared once, in a
  tag-free file (`hotkey.go`); the four per-OS declarations are deleted. This is
  the one edit in the platform step that is not a pure move; call it out in the
  PR description.
- Exported API (what `app`, `config`, `update`, `llamaserver` call):
  ```go
  // tray.go
  const FirstAppCommand = 100
  type MenuItem struct{ ID int; Label string; Checked, Disabled, Separator bool; Sub []MenuItem }
  type Tray interface{ QuitRequested() bool; SetStatus(string); Notify(title, message string, isError bool); SetMenu(func() []MenuItem); NextCommand() (int, bool); Close() }
  type NoopTray struct{}                       // was noopTray
  func NewTray(logPath string) Tray            // was newTray
  func PumpMessages() bool                     // was pumpMessages
  func ShowError(msg string); ShowInfo(msg string); AskYesNo(msg string) bool
  func OpenTarget(target string) error         // was openTarget
  func StartFallbackEditor(path string) error  // was startFallbackEditor
  // hotkey / capture / clipboard
  type Hotkey interface{ Pressed() (bool, error); Close() error }
  func NewHotkey(spec string) (Hotkey, error)
  func ForegroundWindow() uintptr
  func WaitModifiersReleased(max time.Duration) bool
  func SendPaste() error
  func ClipboardSequence() uint32              // only if used outside; today only desktop_*/capture
  type CaptureTask interface{ Step() (done bool, text string, fromSelection bool, err error); Wait() time.Duration }
  func BeginSelectionCapture(cb Clipboard, win uintptr) CaptureTask
  func CaptureSelectedText(cb Clipboard, win uintptr) (string, bool, error)
  var ErrCaptureModifiersHeld, ErrCaptureFocusChanged error
  type Clipboard interface{ Read() (string, error); Write(string) error; ReplaceIfUnchanged(expected, translated string) (bool, error); Close() error }
  func NewClipboard() (Clipboard, error)
  func SameClipboardText(current, expected string) bool
  func ClassifyClipboardDiff(expected, current string, elapsed time.Duration) ClipboardDiff
  // process / install / misc
  func HideConsole(cmd *exec.Cmd); BindToParentLifetime(cmd *exec.Cmd)
  func RunSetup(dir string) error
  func RecordChild(file string, cmd *exec.Cmd); ForgetChild(file string); StopLeftoverServer(file string)
  func ChildRecordPath(dir string, port int) string; CheckServerPort(port int) error
  func PlatformRuntimeError() error
  func WritableAppDir(dir string) (string, error)
  func ServerBinaryName() string; SetupScriptName() string
  func HotkeyLimit() int; PasteShortcut() string; CompletionNotification() (title, body string)
  var VCRuntimeAvailable func(serverDir string) bool   // was vcRuntimeAvailable, overridden by tests
  func RecordedInstallLocation() string; RunInstaller(dir, tag string) error
  ```
  `captureTask`'s methods `step`/`wait` are unexported today; they must be
  exported (or the interface hidden behind a function) because `app` implements
  and consumes it. `capture.go` + `capture_test.go` stay together, so `captureJob`
  itself can remain unexported.
- Seams after the move: **unchanged.** `app` keeps `newHotkey`, `capture`,
  `beginCapture`, `paste`, `foreground`, `openURL`, `showErr`, `askYesNo`,
  `fallbackEditor` fields; `newApp` assigns `platform.NewHotkey`, … . No test
  that sets a seam needs to change except its import list.
  `fakeTray`/`fakeHotkey` implement `platform.Tray`/`platform.Hotkey`.
- Platform-internal tests (move with the package, same build tags):
  `capture_test.go` (the `app`/`decidePaste`/`pasteNone` hits are `decidePaste`
  in `history.go`; this test needs either `decidePaste` moved to `platform` or
  the assertions that mention it left behind — decide in the step PR),
  `clipboard_test.go`, `autostart_{linux,darwin,windows}_test.go`,
  `clipboard_windows_test.go`, `hotkey_windows_test.go`,
  `hotkey_linux_registry_test.go`, `desktop_linux_test.go`,
  `desktop_linux_integration_test.go` (`x11test`), `desktop_darwin_test.go`,
  `native_harness_darwin_test.go`/`native_harness_other_test.go` (`mactest`),
  `job_windows_test.go`, `proc_windows_test.go`, `proc_linux_test.go`,
  `open_windows_test.go`, `ps_windows_test.go`, `psscript_test.go`,
  `leftover_test.go`, `leftover_darwin_test.go`, `tray_windows_test.go`,
  `tray_menu_windows_test.go`, `update` Windows bits.
  Exceptions that stay behind: `capture_async_test.go` (drives `*app`),
  `open_test.go` (`cmdOpenConfig`, `cmdPlamoLicense`), and the two
  syso-dependent tests `TestAppIconIsEmbedded` /
  `TestProcessIsPerMonitorDPIAware` (see 1.4 item 2 and 3.3).
- Each platform test binary needs its own `TestMain` for the fake server
  (`job_windows_test.go`, `proc_linux_test.go`) and for
  `runPlatformSmokeTests()` (the macOS `mactest` harness, whose `TestMain` must
  run on the main thread — `AGENTS.md`).

#### P6 `internal/update`

- Files: `update.go` (platform-free part).
- API:
  ```go
  var Version = "dev"                                        // was version; see Q2 on -X
  type ReleaseInfo struct{ Tag, URL string }
  func FetchLatest(ctx context.Context, apiURL string) (ReleaseInfo, error)
  func IsNewerVersion(latest, current string) bool; CompareVersions(a, b string) int
  type InstallKind int; const KindInstaller, KindScoop …; func DetectInstallKind(dir, installLocation string) InstallKind
  func InstallScriptURL(tag string) (string, error)
  const PlamoLicenseURL, ReleasesPage, LatestReleaseAPI
  ```
  It calls `platform.RecordedInstallLocation` and `platform.RunInstaller`
  through `app`, not itself, so it can stay a leaf.
- Tests: `update_test.go`.

#### P7 `internal/llamaserver`

- Files: `server.go`; `waitReady` and `translate` (+ `errTruncated`) cut out of
  `main.go`.
- Imports: `platform` (`HideConsole`, `BindToParentLifetime`,
  `RecordChild`, `VCRuntimeAvailable`), `languages` (`Direction` in `Translate`).
- API (fields are set directly by `app` today; exported fields or an options
  struct are needed):
  ```go
  type Supervisor struct{ Model, Template string; ContextTokens, Port int; Endpoint string; LogOut io.Writer; StartupTimeout time.Duration; UI func(label string, elapsed time.Duration) (quit bool); PIDFile string; OnStarted func(); … }
  func (s *Supervisor) Start(binary string, gpuLayers int, label string) error
  func (s *Supervisor) Stop(); Exited() <-chan struct{}; Running() bool
  type RestartLimiter struct{ … }; Allow(now time.Time) bool; Attempt() int
  func RestartBackoff(attempt int) time.Duration
  var ErrQuit = errors.New("quit requested")
  type DLLNotFoundError struct{ … }; func IsDLLNotFound(err error) bool
  func WaitReady(ctx context.Context, endpoint string, stopped <-chan struct{}) error
  func Translate(ctx context.Context, endpoint, apiKey, source string, d languages.Direction, maxTokens int) (string, error)
  ```
  `apiKey` is per-start private state (`server.go` `newAPIKey`, "never
  logged"); `app` currently reads it through the struct. An accessor
  `(*Supervisor).APIKey()` replaces that.
- Tests: `server_test.go`, `server_key_test.go`, `llama_smoke_test.go`
  (`llamasmoke`), each package gets a `TestMain` for the fake server; the shared
  fake server is factored out (2.3).
- `leftover.go` is *not* here: it moves to platform (1.2).

#### P8 `internal/settings`

- Files: `settings_server.go`, `settings_web/` (see 3.3 for the embed path),
  `quickstartGuide` type cut out of `guide.go`; `app_settings.go` **stays**
  in `app` (it is `*app` methods).
- API: `Server`, `Start(ask func(Request) Reply, dl *models.Manager, idle time.Duration) (*Server, error)`,
  `(*Server).URL/Running/Close`, `Request`, `Reply`, `State`, `Form`,
  `ProfileForm`, `EngineState`, `ModelState`, `QuickstartGuide`,
  `NewRequest`, `NewActionRequest`, `ErrBusy`, `FormFromProfile`,
  `(Form).ToConfig(base config.Config)`, `WinKeyName`. This is the largest
  exported surface in the series because `app_settings.go` and `app_models.go`
  fill these structs field by field.
- Tests: `settings_page_test.go` (1,224 lines; it uses `*app`, `noHotkey`,
  `cmdSettings` — an app-level test, stays in P9), `settings_test.go`
  (conversion bits move here), the `settingsForm`/`formFromProfile` cases of
  `config_test.go`. Server-level tests that need only `Start`+HTTP can move;
  deciding which of `settings_page_test.go`'s cases are server-only is part of
  the step.

#### P9 `internal/app`

- Files: `app.go`, `app_idle.go`, `app_menu.go`, `app_models.go`,
  `app_settings.go`, `guide.go` (minus `quickstartGuide`), `history.go`,
  `setup_flow.go` (the `ensureInstalled`/`ensureSetupScript` half), `main.go`
  minus everything moved above: `result`, `appDir`, `fatal`,
  `applyTranslation`, `formatBilingual`, `ignoreWriteErrors`, and the body of
  `main()` as `func Run() int` (or `Main`). Embedded scripts enter through
  parameters (see 3.3).
- API: very small — `func Run(opts Options) int` where `Options` carries the
  embedded script bytes and the version string. Everything else is unexported
  and stays so.
- Seams: all remain unexported fields; nothing about them changes.
- Tests (all stay together because they use `*app`, `newTestApp` or the seams):
  `app_test.go`, `app_idle_test.go`, `app_ux_test.go`, `app_models_test.go`,
  `app_models_windows_test.go`, `capture_async_test.go`, `chat_template_test.go`,
  `guide_test.go`, `helper_test.go` (`TestMain`, `fakeTray`, `fakeHotkey`, fake
  server), `history_test.go`, `lifecycle_test.go`, `open_test.go`,
  `paste_test.go`, `profiles_test.go`, `settings_page_test.go`,
  `state_consistency_test.go`, `setup_script_test.go` (embedded-vs-disk check),
  `install_script_test.go` and `setup_ps1_windows_test.go` (they run PowerShell
  on the root scripts: they test scripts, not Go, and are best left in the
  directory next to the scripts; see Q6), `testdata/` (read by
  `chat_template_test.go`).

### 2.3 Shared test support: the fake `llama-server`

Because every package that starts the fake needs a `TestMain`, the fake and its
`loopbackOnly` transport should be extracted once into
`internal/testsupport` (non-`_test` package, importable only from tests inside
the module because it lives under `internal/`):

```go
package testsupport
// Main is called first in every TestMain: it runs the fake server and exits the
// process when CT_TEST_FAKE_SERVER=1, and returns otherwise.
func MaybeRunFakeServer()
const FakeTranslation = "fake translation"
func LoopbackOnly() http.RoundTripper
```

`TestMain` in each package then reads: `testsupport.MaybeRunFakeServer();
models.DownloadTransport = testsupport.LoopbackOnly(); …`. `fakeTray` and
`fakeHotkey` stay in `internal/app`'s `helper_test.go` (they are only needed
there). This is new code, so it is the first non-trivial addition of the series
and should land in its own tiny PR ahead of the first package that needs it
(P7 or P5, whichever goes first). `proc_linux_test.go` currently relies on an
`init` that "runs before TestMain"; that trick stays valid in the platform
package unchanged.

### 2.4 Cycle breakers (summary)

| Cycle | Break by |
| --- | --- |
| config ↔ platform (`serverBinaryName`; hotkey grammar) | grammar → `hotkeyspec` (P2); `config` imports `platform` for `ServerBinaryName` only |
| platform → supervisor (`childRecord`) | `leftover.go` moves into `platform` |
| supervisor → app (`waitReady`, `translate`) | both move into `llamaserver` |
| settings → app (`quickstartGuide`) | the type moves into `settings` |
| config → setup (`checkFile`, `resolvePaths`) and → `main.go` (`pathWithin`, `Profile` methods) | move into `config` |
| languages → `main.go` (`translationDirection`) | type moves into `languages` |

---

## 3. The `cmd/clipboard-translator` question

### 3.1 Options

- **Option A — keep `package main` at the repository root.** After the split the
  root holds `main.go` (a few lines calling `app.Run`), the `.syso`, and a small
  file with the `go:embed` directives. Nothing outside the Go tree changes.
- **Option B — add `cmd/clipboard-translator`** with its own `main.go`,
  `resource_windows_amd64.syso`, and the embed glue.

### 3.2 Consequences

| Aspect | A: main at root | B: `cmd/clipboard-translator` |
| --- | --- | --- |
| Build command | unchanged: `go build … -o ClipboardTranslator.exe .` (CI, `AGENTS.md`, both READMEs, `release.yml`, `package-*.sh`) | `go build … -o ClipboardTranslator.exe ./cmd/clipboard-translator` everywhere those are listed (see section 4) |
| `-X main.version=…` | unchanged if `var version` stays in `main` (the root package) and is passed to `app.Run`. If the variable instead lives in `internal/update`, the flag must become `-X github.com/ypsilonmeister/clipboard-translator/internal/update.Version=…` in `release.yml`, `package-linux.sh`, `package-macos.sh` and the README sentence about `main.version` | same: `-X main.version` still valid if the variable is in the `cmd` main package |
| `resource_windows_amd64.syso` | stays at the root, beside `main.go`; `go run ./tools/mksyso` default `-o` unchanged | **moves** to `cmd/clipboard-translator/`; `tools/mksyso` default `-o` (flag in `tools/mksyso/main.go`), the `release.yml` step "Embed icon and version info", the CI "version info" step and the `AGENTS.md`/README text all change. The `.syso` is linked only into the package in its directory, so a mis-placed file silently produces an exe without icon/DPI manifest (CI catches it: "Check the exe carries its version info", `TestAppIconIsEmbedded`) |
| `go:embed` paths | `setup.ps1`, `setup.sh`, `setup-macos.sh` embed from the root package directly; `settings_web` and `assets/icon.png` need a home (below) | the three scripts **cannot** be embedded from `cmd/...` (embed forbids `..`); a root-level non-main package must export them, or the scripts must be copied/moved under `cmd/` — the latter breaks the one-line installer, `Setup.cmd`, the flat zip, `tools/bump_llama_pin.py`, `tools/test_linux_setup.py`, and `package-linux.sh` |
| Release zip layout | unchanged (flat: exe + `Setup.cmd`, `Start.cmd`, `Stop.cmd`, `setup.ps1`, `config.json`, `README.txt`, `LICENSE`) | unchanged in content; only the `go build` line differs. The Compress-Archive file list in `release.yml` still points at the repo root for everything but the exe |
| Test layout | tests of `package main` (e.g. `TestAppIconIsEmbedded`) stay at root where the `.syso` is linked | those two tests move to `cmd/clipboard-translator` |
| Conventional layout | non-standard but common for single-binary apps | conventional (`cmd/<name>`), useful if a second binary ever appears (none is planned in this repository) |
| README/AGENTS | minimal diff | build instructions in README.md/README.ja.md (lines ~437–456 / ~406–423), AGENTS.md "Commands" and "Embedded resources" change |
| CI path filter | `llama-smoke.yml` lists `main.go`, `server.go`, `proc_*.go` … which all move regardless of A or B | same |

### 3.3 Embedded assets: where each embed lives

`go:embed` needs the files to be in or below the package directory. Current
embeds and where they go under each option:

| Embed | Source of truth | Option A | Option B |
| --- | --- | --- | --- |
| `setup.ps1`, `setup.sh`, `setup-macos.sh` | repo root (installer/zip/tools depend on it) | `embed.go` in root `package main`: `//go:embed setup.ps1 setup.sh setup-macos.sh`, passed as `app.Options` | requires a root *library* package, e.g. `package clipboardtranslator` in `embed.go`, importing nothing; `cmd/…/main.go` imports it. Root can't hold both `package main` and `package clipboardtranslator`, so B forces this root package |
| `settings_web/*` | `settings_web/` | stays at root *and* embedded from the root package, passed to `settings.Start`; or the directory moves to `internal/settings/web/` (then README/`AGENTS.md` mentions `settings_web/` update) | same two options; only the second keeps `internal/settings` self-contained |
| `assets/icon.png` (`desktop_linux.go`) | `assets/` (also read by `package-macos.sh`, `tools/mkicon`, `tools/mksyso`) | add `assets/assets.go` (`package assets`, `//go:embed icon.png`), `platform` imports `assets` | same |

Recommendation for the series (proposal, Q1/Q2): **Option A**, with the root
holding `main.go` + `embed.go` + the `.syso`. It is the only option under which
steps 2 – 4 touch no build command, no release path and no installer URL.
Option B can be done later as a separate, equally mechanical PR (move `main.go`,
`embed.go`'s replacement and the `.syso`, then update the list in section 4)
once the internal packages exist; nothing in P1–P9 forecloses it. Doing B first
would couple the largest risk (release artefacts) to the first PR.

---

## 4. Path-reference inventory

Every file below names a root path or assumes the flat layout. "Step" refers to
section 5.

| Where | What it references | What must be updated and when |
| --- | --- | --- |
| `setup_flow.go` | `//go:embed setup.ps1`, `setup.sh`, `setup-macos.sh` (lines 17, 20, 43) | S5 (app): embeds become root-level `embed.go` (A) or a root library package (B) |
| `settings_server.go` | `//go:embed settings_web`; `settingsWeb.ReadFile("settings_web/index.html")` | S4 (settings): keep path via root embed, or move the dir and change the string |
| `desktop_linux.go` | `//go:embed assets/icon.png` | S2 (platform): needs `assets/assets.go` or a copy next to the file |
| `-X main.version` | `.github/workflows/release.yml` (Build step), `tools/package-linux.sh:13`, `tools/package-macos.sh:14` | only if `version` leaves package `main` (Q2); then also the README sentence that mentions `main.version` |
| `resource_windows_amd64.syso` | root; produced by `go run ./tools/mksyso`; default `-o` in `tools/mksyso/main.go`; regenerated in `release.yml` ("Embed icon and version info"); described in `AGENTS.md` ("Embedded resources") and README.md/README.ja.md | Option A: none. Option B: move file, change flag default, `release.yml`, docs |
| `.github/workflows/ci.yml` | `gofmt -l .`, `go vet ./...`, `go test ./...`, `go build … .`, `CGO_ENABLED=0 go test ./...`, `go test -race -count=1 ./...`; `go test -tags x11test -run '^TestX11' … .`; `CT_TEST_MACOS=1 go test -tags mactest -run '^TestMacNative' … .`; `bash -n setup.sh`, `tools/package-*.sh`; PowerShell parse of `install.ps1`, `setup.ps1` | S2: the two tagged-test lines end in `.` (= the root package) and must become `./internal/platform`; the `go vet/test ./...` lines need no change; S5 (B only): the build line |
| `.github/workflows/release.yml` | same test lines for the macOS and Linux jobs (`… -run '^TestMacNative' … .`, `… x11test … .`), `go build … .`, `-X main.version`, `go run ./tools/mksyso`, zip file list | S2: the two tagged lines; S5/B: build line and `.syso` |
| `.github/workflows/llama-smoke.yml` | `paths:` filters (push **and** pull_request) list `server.go`, `models.go`, `main.go`, `proc_*.go`, `platform.go`, `go.mod`, `go.sum`, `llama_smoke_test.go`; the job runs `go test -tags llamasmoke -run '^TestLlamaSmoke' … .` | each step that moves one of those files must rewrite the path (S1 `models.go`, S2 `proc_*.go`/`platform.go`, S3 `server.go`/`main.go`/`llama_smoke_test.go`) **and** the trailing `.` of the test command; otherwise the workflow silently stops triggering on the files it was written to guard |
| `tools/package-linux.sh` | `go build … .`; `cp setup.sh README.md README.ja.md LICENSE` | only the build line under B |
| `tools/package-macos.sh` | `go build … .`; `assets/icon.png`, `LICENSE`, `README*` | only the build line under B |
| `tools/print_model_pin.py` | reads `Path(root_dir) / 'models.go'` (line 84) and says so in messages | S1 (models): path → `internal/models/models.go`; `tools/test_print_model_pin.py` fixtures if they create a `models.go` |
| `tools/bump_llama_pin.py` | `setup.ps1`, `setup.sh`, `setup-macos.sh` at root only | none (scripts stay) |
| `tools/test_linux_setup.py`, `tools/test_macos_setup.py` | `parents[1] / "setup.sh"` / macOS script at root | none |
| `tools/mksyso`, `tools/mkicon` | `assets/icon.ico`, `assets/app.manifest`, `resource_windows_amd64.syso` | none under A |
| `bucket/clipboard-translator.json` | release zip URL/hash, `ClipboardTranslator.exe`, `setup.ps1`, `STOP`, `models`, `runtime`, `config.json` in the *installed* folder | none: it describes the zip, which does not change in either option |
| `install.ps1` | `ClipboardTranslator.exe`, `Setup.cmd`, `Start.cmd`, `Stop.cmd`, `setup.ps1`, `config.json`, `README.txt`, `LICENSE`, its own raw URL on `main` | none (stays in root) |
| `setup.ps1`, `setup.sh`, `setup-macos.sh` | mention `ClipboardTranslator.exe`, `Setup.cmd`; they do not name `models.go` (the pin comments say "as in setup.ps1") | none. The comment in `models.go` and the `modeldl_test.go` messages that say the values equal `setup.ps1` stay true and move with the file |
| `Setup.cmd`, `Start.cmd`, `Stop.cmd` | `setup.ps1`, `ClipboardTranslator.exe`, `STOP` | none (stay in root; the exe name is set by `-o`) |
| `README.md`, `README.ja.md` | build commands (`go build -ldflags … -o ClipboardTranslator.exe .` — lines ~437/440 and ~406/409), `go run ./tools/mksyso`, `bash tools/package-*.sh dev`, `main.version`, "`setup.ps1` is embedded in the exe", `install.ps1` URL | S2: if the platform tests are mentioned; B: build lines; Q2: `main.version` sentence. Both languages together (`AGENTS.md` style rule) |
| `AGENTS.md` | the Commands block (root build, `-tags x11test … .`, `-tags mactest … .`), the whole **Code map** table (every row names root files), "Things that bite" (`settings_web`, `resource_windows_amd64.syso`, `main.version`), "Tests" (seams named per file: `helper_test.go`, `TestMain`) | **every step**: the Code map rows for the files it moved; S2 the two tagged-test commands |
| tests that read repo files | `setup_script_test.go` (`setup.ps1`, `setup-macos.sh`), `modeldl_test.go` (`setup.ps1`), `models_test.go` (`tools/print_model_pin.py`), `install_script_test.go` (`install.ps1`), `chat_template_test.go` (`testdata/…`), `app_test.go` | a test that moves down one directory level needs `../../` (S1 for `modeldl_test.go`/`models_test.go`, S5 for the rest); consider a helper `repoRoot(t)` |
| `.gitignore`, `.gitattributes` | `*.exe`, `models/`, `runtime/`, `dist/` | none |
| `docs/DESIGN_PHILOSOPHY.md` | no code paths | none |

---

## 5. Staged execution plan

One PR per package, each a mechanical move (`git mv`, `package` line, renames
of now-exported identifiers, import lines, the unavoidable accessor additions
listed). Each PR is verified with the same checklist (**V**):

> **V**: `gofmt -l .` prints nothing; `go vet ./...`; `go test ./...` (Windows);
> on Linux `go test ./...`, `go test -race -count=1 ./...`,
> `CGO_ENABLED=0 go test ./...`, the Xvfb `x11test` run and
> `bash tools/package-linux.sh dev`; on macOS (arm64 and Intel) `go test ./...`,
> the `mactest` smoke run and `bash tools/package-macos.sh dev`; the
> `llama-smoke` workflow when its filtered files move; and a PR-description
> diff stat showing that moved files are renames with ≥ 95 % similarity
> (`git diff -M --stat main`).

Because the host that wrote this document is Windows, Linux/macOS-specific moves
(S2) cannot be proven locally; those PRs depend on the CI Linux and macOS jobs
as the actual verification, and per `AGENTS.md` real-desktop behaviour (live
hotkey, tray, paste) is not covered by `go test` and must be stated as
unverified unless checked on a maintainer machine.

### S0 — shared test support (precondition)

- Files: new `internal/testsupport/fakeserver.go` (fake server, `LoopbackOnly`),
  `helper_test.go` updated to call it. No other file moves.
- API: `MaybeRunFakeServer()`, `FakeTranslation`, `LoopbackOnly()`.
- Risk: the re-exec trick breaks if `TestMain` order changes
  (`proc_linux_test.go`'s `init` runs before `TestMain`).
- Verify: **V**. Test count must stay the same as Appendix A's (464).

### S1 — prove the method: `languages`, `hotkeyspec`, `config`, `models`

Three or four PRs, in this order (each is independent of the later ones):

1. **`internal/languages`** — file list: `languages.go`, `languages_test.go`
   (split). Move `translationDirection` out of `main.go`. Additions: `All()`
   (or `Each`). Risk: `languageOptions` ordering must be identical (existing
   test in `settings_page_test.go` covers the page). Verify: **V**.
2. **`internal/models`** — `models.go`, `modeldl.go`, `models_test.go`,
   `modeldl_test.go`. Additions: `Events()`, exported fields/methods listed
   in 2.2. Updates in this PR: `tools/print_model_pin.py` path (and its
   test fixtures), `llama-smoke.yml` `models.go` filter, `AGENTS.md` Code map
   (rows for `models.go`/`modeldl.go`/`print_model_pin.py`), relative paths in the
   two tests.
   Risk: `modeldl_test.go` reads `setup.ps1` and must find it two levels up.
   Verify: **V** plus `python3 tools/test_print_model_pin.py`.
3. **`internal/hotkeyspec`** — only if Q4 = yes. Extract and re-import from
   `config.go`; platform files still in root call it. Verify: **V**.
4. **`internal/config`** — **deferred to S4** (Q5). File list: `config.go`,
   `settings.go`, `config_test.go` (split), `settings_test.go` (split),
   `main_test.go`; `Profile` methods and `skipReason` out of `main.go`;
   `pathWithin`; `appPaths`/`resolvePaths`/`checkFile` out of `setup_flow.go`.
   Risk: `serverBinaryName` is still in the
   root at this point; `config` cannot import `main`. Resolve by doing S2 first,
   or by temporarily passing the binary name as a parameter in `Default()`
   (a non-mechanical change); **recommended order: S2 before config**, so S1
   proves the method on `languages` and `models` (both true leaves) and `config`
   follows `platform`. See Q5.

### S2 — `internal/platform`

- Files: the full list in 2.2, P5. Moves cgo files with their Go files.
- Edits beyond renames: single `Hotkey` interface; export the 25-odd names
  listed; `captureTask` method export; root `desktop_linux.go` embed of
  `assets/icon.png` → `assets/assets.go`.
- Updates in the same PR: `ci.yml` and `release.yml` tagged-test commands
  (`.` → `./internal/platform`), `llama-smoke.yml` (`proc_*.go`, `platform.go`
  entries), `AGENTS.md` Commands and Code map. `TestAppIconIsEmbedded` and
  `TestProcessIsPerMonitorDPIAware` stay in the root package next to the
  `.syso` (Option A), calling a tiny exported hook (`platform.LoadAppIcon`) or
  being re-implemented against `user32` in the test.
- Risks (highest in the series): (1) **cgo**: a `.c`/`.m` file left behind
  fails the Linux/macOS build only; (2) **per-OS build tags** are only compiled
  on their own CI job; (3) `leftover.go` and `psscript.go` change package, so
  `proc_darwin.go` and `proc_windows.go` must be compiled on both; (4) the
  `x11test` build tag now belongs to the platform package directory;
  (5) symbol-level renames across ~50 call sites in `app*.go`.
- Verify: **V** on all three CI jobs (this PR cannot be checked from the
  Windows host alone); additionally build with `GOOS=linux CGO_ENABLED=0 go vet
  ./...` and `GOOS=darwin CGO_ENABLED=0 go vet ./...` from the Windows host to
  catch stub-only breakage early (`CGO_ENABLED=0` compiles the portable stubs).

### S3 — `internal/llamaserver`

- Files: `server.go`, `server_test.go`, `server_key_test.go`,
  `llama_smoke_test.go`; `waitReady` and `translate` from `main.go`.
  (`leftover.go` is already in `platform` after S2.)
- Additions: exported fields/options on `Supervisor`, `APIKey()` accessor.
- Updates: `llama-smoke.yml` (`server.go`, `main.go`, `llama_smoke_test.go`
  entries, trailing `.`), `AGENTS.md`.
- Risks: the `ui` callback and `onStarted` run on the UI thread — keep the
  documented contract (`serve` must not block); `apiKey` must not be logged;
  `restartLimiter` is used by value inside `app` (copy semantics).
- Verify: **V**; the `llama-smoke` workflow must pass (it is the only test
  against a real `llama-server`).

### S4 — `internal/config` (if deferred from S1) and `internal/update`

- `update`: `update.go`, `update_test.go`; decide `Version` (Q2). Verify **V**.
- `config`: as in S1.4 once `platform` exists.

### S5 — `internal/settings`

- Files: `settings_server.go`, `settings_web/` (or its replacement location),
  `quickstartGuide` from `guide.go`; split test files.
- Additions: exported request/state/form types and `ToConfig`.
- Risks: widest exported surface; the loopback/token/Host/Origin/CSP
  checks in `guard` must not change; the page's JSON field names are tagged and
  must be unchanged (`settings_page_test.go` covers them).
- Verify: **V**.

### S6 — `internal/app` and `main`

- Files: all remaining root `.go` files except `main.go`/`embed.go`; relocate
  `helper_test.go` and the app tests; `main()` becomes a call into `app.Run`.
- Updates: `AGENTS.md` (rewrite Code map around the new layout), README
  build sections (only if Q1 = B), release/CI build lines (only if Q1 = B).
- Risks: `go vet ./...` on all tags; test count unchanged (464 or whatever S0
  left); the root must keep `package main`.
- Verify: **V** and diff of the release zip contents (file list identical
  to the previous release).

(An optional **S7**: `cmd/clipboard-translator`, Q1 = B.)

---

## 6. Open questions

Each needs the maintainer's decision before code moves.

- **Q1. `main` at the root (A) or `cmd/clipboard-translator` (B)?** Options in
  section 3. *Recommendation (proposal): A for steps 2–6, revisit B as a
  follow-up.* A touches no build command, installer, release asset or
  `.syso`; B can be done afterwards without touching the internal packages.
- **Q2. Where does the version string live, and what does `-X` target?**
  (a) keep `var version` in `package main` and pass it to `app.Run`/
  `update`: zero change to `release.yml`, `package-linux.sh`,
  `package-macos.sh`, README; (b) move it to `internal/update` and change the
  three build lines. *Recommendation: (a).*
- **Q3. One `internal/platform` package or one per OS?** One package
  keeps files byte-identical and avoids a registration layer. Per-OS packages
  would add an interface and a registry (a behaviour-neutral but non-mechanical
  design change). *Recommendation: one package.*
- **Q4. Extract `internal/hotkeyspec`, or keep the grammar in `config` and let
  `platform` import `config`?** The second option avoids an extra package but
  makes the low-level platform code depend on the config schema, and config
  already needs `platform.ServerBinaryName` (a cycle). Alternatively move
  `ServerBinaryName` into `config` (it is a one-line OS switch). *Recommendation:
  extract `hotkeyspec`.*
- **Q5. Order of S1 vs S2.** S1 as the Issue describes it ("prove the
  method on `languages`/`config`/`models`") includes `config`, but `config`
  needs `platform.ServerBinaryName` and `checkFile`/`resolvePaths`.
  Options: (a) prove on `languages` + `models` first (true leaves) and put
  `config` after `platform`; (b) keep `config` first and pass the binary name as a
  parameter (a small non-mechanical change). *Recommendation: (a).*
- **Q6. Which tests count as "Go tests" for the moved packages?**
  `install_script_test.go`, `setup_ps1_windows_test.go`,
  `setup_script_test.go` exercise PowerShell/bash scripts at the root; they
  need the root as working directory. Leave them in the root package (then the
  root keeps a `_test.go` set even under Option A) or move them to a
  `scripts_test`-style package with a `repoRoot(t)` helper. *Recommendation:
  leave in the root package.*
- **Q7. Mutable test hooks across packages** (`vcRuntimeAvailable`,
  `downloadTransport`): exported package variables (mechanical, but public mutable
  state inside `internal/`) or injected fields/options (a few lines of new code,
  no mutable globals). *Recommendation: exported variables for the series;
  convert later if it bothers anyone.*
- **Q8. `Profile.check` and the skip messages in `config`?** They are UI-facing
  strings (`skipMessage`, `skipStatus`) attached to the config type only because
  Go needs methods beside their type. Options: leave them in `config`
  (mechanical) or move the strings to `app` and keep only the decision in
  `config` (a refactor). *Recommendation: leave them in `config`.*
- **Q9. The two syso-dependent tests** (`TestAppIconIsEmbedded`,
  `TestProcessIsPerMonitorDPIAware`): keep them in the main package next to the
  `.syso` calling an exported platform hook, or delete the unexported helper
  dependency by re-implementing the 5-line `user32` calls in the test.
  *Recommendation: exported hook.*
- **Q10. Is a Windows-only host acceptable for reviewing S2?** The Linux and
  macOS builds are only proven by CI. If the maintainer wants a local check
  before merging S2, a Linux VM reached over SSH (as `AGENTS.md` allows) is
  needed. *Recommendation: rely on CI plus one manual smoke on each OS before
  the release that contains S2.*
- **Q11. Settings web assets location** (`settings_web/` at the root or
  `internal/settings/web/`). Moving the directory makes `internal/settings`
  self-contained; leaving it avoids touching docs and requires an embed in a
  root package. *Recommendation: move it with S5.*
- **Q12. `docs/DESIGN_PHILOSOPHY.md` is a proposal.** Nothing here changes a
  user-visible workflow, so it is not needed to assess this design; recorded
  only to show it was read.

---

## Appendix A — Baseline ("before" state)

Taken on the branch `issue-104-design` (based on `main` at `cfcb1b4`), Windows
11 x64, Go 1.27.3 (`go version`: `go1.27.3 windows/amd64`), non-interactive
shell from Claude Code. No source files had been changed.

| Command | Outcome |
| --- | --- |
| `go build ./...` | exit status 0 |
| `go test . -count=1` | `ok  github.com/ypsilonmeister/clipboard-translator  242.465s` — 464 tests passed, 0 failed (single package) |
| `git status --short` after the run | only the untracked `BRIEF.md` and `launch-104.cmd`; `resource_windows_amd64.syso` unchanged (no restore needed) |

Notes:

- The brief warns that `TestProcessIsPerMonitorDPIAware` fails in
  SSH/non-interactive sessions, including on unmodified `main`, as an
  environment artifact. It **passed** in this run, so no environment-artifact
  failure needed to be recorded. If it fails elsewhere, compare against this
  baseline before treating it as a regression.
- Linux and macOS code was not built or tested on this host. Their "before"
  state is whatever the Linux/macOS CI jobs report for `main`.
- `go test . -count=1` was run twice. The first run's summary (filtered by the
  local `rtk` proxy) read `464 passed in 1 packages`; the second, unfiltered, run
  printed the `ok … 242.465s` line above. The 464 figure comes from the first run
  only.
- Neither run rewrote the `.syso`.

## Appendix B — Reproducing the inventory

The cross-reference scan was a ~120-line Go program using `go/parser` and
`go/ast`, not committed. Method: parse each root `*.go` file without build
constraints; record every package-level function, type, var and const name and
the file that declares it; for every identifier occurrence in another file whose
group differs, record the pair. Methods are not recorded as definitions (so
receivers such as `(*app).foo` do not appear), which is why the `app` group's
internal structure was read by hand. Counts in section 1 of the form
"N files, M lines" come from `grep` over `*_test.go`.
