# AGENTS.md

Orientation for AI coding agents (and new contributors). User-facing docs are
in [README.md](README.md) / [README.ja.md](README.ja.md); this file is about
working on the code.

## What this is

A tray app for Windows x64, with experimental Linux X11 and macOS support. The
user selects text, presses a hotkey (default Ctrl+Alt+E), and the app puts the
translation on the clipboard: Japanese<->English by default, or any language
pair or target language of the 53 the model covers, with replace or bilingual
output set per hotkey (`profiles` in `config.json`, also editable on the Settings page). Translation is fully
local: the app starts a loopback-only `llama-server` (llama.cpp) child process
with a PLaMo 3 Translate GGUF model (2B by default; 0.8B and 8B through
`setup.ps1 -Model` or the Settings page) and talks to it over HTTP. Go,
standard library only (no third-party modules), `go 1.21` in `go.mod`, MIT
licensed. Linux uses cgo with system GTK 3, X11 and XTest; macOS uses cgo with
Cocoa/Carbon/ApplicationServices (no extra Go modules).

## Design philosophy (proposed)

Before planning or implementing changes, read
[`docs/DESIGN_PHILOSOPHY.md`](docs/DESIGN_PHILOSOPHY.md). Its central goal is to
avoid interrupting the user's work just to translate. Use its principles to
assess feature proposals, defaults, and UX tradeoffs, especially preserving
the select → hotkey → paste workflow. The document is currently a proposal:
do not treat unapproved principles as settled requirements. Existing explicit
requirements, safety constraints, and maintainer decisions still apply.

## Commands

```bash
gofmt -l .          # must print nothing (CI fails otherwise)
go vet ./...
go test ./...       # Windows runs the Win32 tests too
go build -trimpath -buildvcs=false -ldflags "-H=windowsgui -s -w" -o ClipboardTranslator.exe .
```

Linux: install `libgtk-3-dev libx11-dev libxtst-dev pkg-config`, then run
`go test ./...` and `bash tools/package-linux.sh dev`. `setup.sh` needs Python
with `tarfile.data_filter`. Native integration tests run on a disposable Xvfb
display by default, not on a desktop someone is working at:

```bash
env -u WAYLAND_DISPLAY XDG_SESSION_TYPE=x11 GDK_BACKEND=x11 CT_TEST_X11=1 xvfb-run -a go test -tags x11test -run '^TestX11' -timeout 60s .
python3 tools/test_linux_setup.py
```

`GDK_BACKEND=x11` matters on a machine that also runs a Wayland session (Ubuntu
26.04 has only Wayland): without it GTK may pick the Wayland socket in
`XDG_RUNTIME_DIR` and `TestX11Desktop` fails with "could not initialize GTK 3".

`CGO_ENABLED=0` builds only the portable logic/stubs on Linux. Wayland is
explicitly rejected at runtime. `desktop_linux.go` / `native_linux.c` own the
GTK event loop, XGrabKey, XTest input and X11 clipboard. `autostart_linux.go`
manages an XDG autostart Desktop Entry. GTK supplies a control-window fallback
if no tray host is present. X11 clipboard compare/write is best effort, not
atomic like the Windows implementation.

macOS needs a native macOS runner with Xcode Command Line Tools. Build with
`bash tools/package-macos.sh dev`; `CGO_ENABLED=0` only compiles logic/stubs.
`desktop_darwin.go` / `native_darwin.m` own AppKit/Carbon and must stay on the
process main thread. `CT_TEST_MACOS=1 go test -tags mactest -run '^TestMacNative' -v .`
executes native smoke checks through TestMain on that thread (CI only, no key
injection or Accessibility prompts). `python3 tools/test_macos_setup.py` runs
setup fixtures on any platform. macOS data goes to Application Support, never
inside the `.app`. Packages are ad-hoc signed only; Developer ID signing and
notarization are outside this implementation.

On a Windows machine the Linux logic tests can be built with
`GOOS=linux go test -c -o <outside the repo>/app.test .` and run in WSL **with
the repo as the working directory** (some tests read repo files). CI runs them
on `ubuntu-latest` anyway.

The build is a GUI-subsystem exe (`-H=windowsgui`): there is no stdout, so use
`log` (written to `translator.log`), tray notifications, or `showError`.

## Code map

| File | Role |
| --- | --- |
| `main.go` | `main()`, `Profile.direction` (what one hotkey translates), the HTTP `translate` call, `fatal`, `pathWithin` |
| `internal/languages/` | The 53 model languages, `Validate`, and `DirectionFor`: the script of the text picks the direction of a language pair (URLs are skipped). Standard library only; the first package split out of `main` (Issue #104) |
| `app.go` | `app` struct and the main loop `serve`: hotkeys (one per profile), tray commands, update results, translation results, Settings-page requests, download results. `startTranslation` / `handleResult` / `abortTranslation` |
| `app_menu.go`, `app_idle.go` | Daily tray commands (Settings first, cancel, unload, model switch when multiple are installed, history, updates, config and license); configuration is edited in Settings; UI-thread idle timer and intentional unload/reload state |
| `capture.go` | `captureJob`: the ordering rules of selection capture (modifiers released, focus unchanged, clipboard sequence) over per-OS pieces, so they are tested without a desktop. `serve` steps the job (`stepCapture`) every ~15 ms instead of sleeping, so a slow copy never blocks the UI thread |
| `settings_server.go`, `app_settings.go`, `settings_web/` | The Settings page: `settings_server.go` serves it on 127.0.0.1 while it is open (token, Host/Origin checks, CSP) and never touches app state; `app_settings.go` answers its requests on the UI thread and applies hotkeys, auto-paste, notifications, `max_chars` and `idle_unload_minutes`; `settings_web/` is the embedded HTML/CSS/JS |
| `internal/models/` (`models.go`, `modeldl.go`), `app_models.go` | `models.go` is the model catalog (pinned revision and SHA-256, kept equal to `setup.ps1`, `setup.sh` and `setup-macos.sh`; tests compare them); `modeldl.go` is the download manager (resume, SHA-256 check, cancel; owned by `app`, not by the settings server; the only code that contacts the model host); `internal/models/modeltest` is a fake model host for tests and `internal/testsupport` holds the loopback-only transport; `app_models.go` switches model and engine for the page, answers it while the server loads, and deletes unused models or partials |
| `server.go` | `supervisor` for `llama-server` (start, stop, exit detection), `restartLimiter` (max 3 per 10 min) and backoff |
| `setup_flow.go` | Resolves config paths, checks the model/runtime exist, offers to run `setup.ps1` (embedded in the exe with `//go:embed`) |
| `internal/hotkeyspec/` | The hotkey syntax (`Ctrl+Alt+E`): one parser shared by the config check and the per-OS hotkey code; no OS dependency |
| `config.go` / `settings.go` | `Config` + validation; order-preserving in-place `config.json` edits with a `.bak` |
| `update.go`, `update_windows.go` | "Check for updates" (GitHub API, only on click), version compare, install-kind detection (installer / Scoop / portable) |
| `history.go` | Last 10 translations (memory only) and `decidePaste` for auto-paste |
| `tray.go` (+ `_windows`/`_other`) | `Tray` interface; Windows impl is a hidden window + `Shell_NotifyIconW` |
| `hotkey_windows.go`, `clipboard_windows.go` | `RegisterHotKey` (one id per hotkey), simulated Ctrl+C (`captureSelectedText`), clipboard access, Ctrl+V |
| `desktop_linux.go` + `native_linux.c`, `desktop_darwin.go` + `native_darwin.m`, `hotkey_nonwindows.go`, `clipboard_*.go`, `autostart_*.go` | Tray, hotkey, clipboard and autostart on Linux (GTK/X11/XTest) and macOS (AppKit/Carbon); the non-cgo stubs. Details under Commands |
| `platform.go`, `proc_*.go`, `data_*.go` | Per-OS facts: server binary name, hotkey limit, hiding the console, child-process lifetime, where data lives |
| `psscript.go` | The PowerShell command lines that run `setup.ps1` / `install.ps1` and keep the failure prompt open |
| `setup.ps1`, `install.ps1` | Downloads model + llama.cpp; the one-line installer. Must parse on Windows PowerShell 5.1 *and* PowerShell 7 |
| `llama_smoke_test.go` | Real-binary smoke test (`//go:build llamasmoke`): starts `llama-server` through `supervisor` and tests translation with real model |
| `tools/bump_llama_pin.py` | Bumps, checks, and inspects (`--show`) the pinned llama.cpp release tag and asset SHA-256 hashes across `setup.ps1`, `setup.sh` and `setup-macos.sh` |
| `tools/print_model_pin.py` | Prints pinned model URLs and SHA-256 digests from `internal/models/models.go` for workflow and scripts |
| `bucket/clipboard-translator.json` | Scoop manifest (this repo is also the bucket) |
| `tools/mksyso`, `tools/mkicon` | Generate `resource_windows_amd64.syso` (icon, version info, DPI manifest) and `assets/icon.*` |

## Things that bite

- **One thread owns the UI.** `main` calls `runtime.LockOSThread()`; the tray
  window and `RegisterHotKey` belong to that thread, and `serve` pumps its
  messages (poll tick is capped at 100 ms for that reason). Goroutines talk to
  the app only through channels (`results`, `updates`, `settingsReqs` for the
  Settings page, `dl.events` for model downloads). Do not touch the tray, the
  hotkeys or `app.conf` from another goroutine, and do not block `serve`.
- **Generation counter.** Every new translation bumps `app.generation`; a
  result with an older generation is dropped. Keep that when changing the flow.
- **Clipboard safety.** The result replaces the clipboard only if the text is
  still the original (`ReplaceIfUnchanged`, one `OpenClipboard` lock). Auto-paste
  is off by default and only fires for a fresh selection while the same window
  is in front. Selection capture waits for the sequence to settle (~80 ms) to
  avoid intermediate clipboard writes, and `ReplaceIfUnchanged` tolerates harmless
  line-break (CRLF/CR) and trailing whitespace changes while protecting newer user copies.
  On Windows the capture waits up to 2 s (`windowsCopyTimeout`) for the copy, without
  blocking the UI thread (a new hotkey or an abort drops the pending copy): a slow web
  page can finish its copy after 1 s, and giving up earlier translates the stale clipboard
  text (the cost: with nothing selected the clipboard text is used 2 s after the key press).
  A profile with `input: "clipboard"` skips the capture entirely (no Ctrl+C, no wait,
  never auto-pasted).
- **Paths in `config.json` are relative to the exe folder** (`pathWithin`
  rejects absolute paths and `..`), and may use `/` or `\`. Unknown keys must
  survive `updateConfigFile`.
- **Single instance** is enforced by listening on `port+1`; the server uses
  `port` (default 18119). A leftover `STOP` file is removed at startup;
  `Stop.cmd` creates it to ask the running app to quit.
- **Embedded resources.** `resource_windows_amd64.syso` is committed (it holds
  the last release's version). Regenerate with
  `go run ./tools/mksyso -version X.Y.Z` after changing the icon or
  `assets/app.manifest`. The release workflow regenerates it from the tag.
- **Version string.** `main.version` is `"dev"` locally and is stamped by
  `release.yml` with `-X main.version=<tag without v>`. "Check for updates"
  compares it numerically with the latest release; "dev" never offers an update.
- **No network on its own.** The app contacts the network only when the user
  asks: "Check for updates" / "Update" (GitHub), the setup downloads of the model
  and llama.cpp (Hugging Face, GitHub), and the Download button on the Settings
  page (Hugging Face, the pinned revision and SHA-256 only). The Settings page
  itself is a loopback-only server that exists while it is open. Keep it that
  way and keep the README's privacy section true if you add anything.
- **Pinned llama.cpp release.** `setup.ps1`, `setup.sh` and `setup-macos.sh`
  install a pinned llama.cpp release tag with verified SHA-256 digests for all
  asset families (Windows CPU, CUDA+cudart, Vulkan; Linux CPU, Vulkan; macOS
  ARM64, x64). Update them using `python3 tools/bump_llama_pin.py <tag>`.
- **Don't commit large or third-party files**: `models/`, `runtime/`, `*.exe`,
  logs (all in `.gitignore`).

## Tests

- The test binary doubles as a fake `llama-server` (`TestMain` in
  `helper_test.go` calls `testsupport.MaybeRunFakeServer`, env
  `CT_TEST_FAKE_SERVER=1`), so crash recovery and engine
  switching are tested without the real model. Seams like `newHotkey`,
  `showErr`, `foreground`, `paste`, `capture` and `openURL` on `app` are
  replaceable in tests; `fakeTray` records tray calls. Model downloads in tests
  only reach loopback (`downloadTransport` is set in `TestMain`).
- Behaviour that needs a real desktop (live hotkey, real selection, Ctrl+V into
  another app, the look of the tray menu) is not covered by `go test`. Say so
  when you could not check it. Do not inject keystrokes into, or take screenshots
  of, a desktop someone is working at. A machine or VM the maintainer has set up
  for testing (for example a Linux VM reached over SSH) is fair game for real
  checks, including key injection and screenshots, as is any desktop the
  maintainer explicitly points you at; use it for this project only, leave
  nothing running afterwards, and say which machine you used.
- Windows-only code lives in `*_windows.go`; give any new one a
  `*_other.go` / `*_nonwindows.go` stub so the Linux CI job still builds.

## Bumping llama.cpp

The setup scripts install a fixed official llama.cpp release (release tag and
asset SHA-256 hashes hard-coded in each script). To bump to a newer llama.cpp
release:

1. Run `python3 tools/bump_llama_pin.py <tag>` (queries GitHub API for that
   release tag, verifies all required asset families and SHA-256 digests are
   published, and rewrites the pin blocks across `setup.ps1`, `setup.sh`, and
   `setup-macos.sh`).
2. Run `python3 tools/bump_llama_pin.py --check` (or `go test ./...`) to ensure
   all scripts agree and hashes match.
3. Run the llama.cpp smoke workflow (`.github/workflows/llama-smoke.yml` via
   `workflow_dispatch` on the branch, or let it run on the PR because the pin
   files are in its paths) and verify it passes before merge.
4. Review the diff.

## Release flow

1. Merge to `main` through a PR (CI = `.github/workflows/ci.yml`: a Windows job
   (gofmt, vet, test, build, PowerShell parse check, version-info check), a Linux
   job (X11 tests under Xvfb, setup/package script checks, tarball) and a macOS
   job (native smoke checks, setup fixtures, app bundle) on arm64 and Intel, plus
   `.github/workflows/llama-smoke.yml` for real-binary smoke tests with pinned
   llama-server and the 0.8B model).
   The Linux CI job also runs `go test -race -count=1 ./...` to detect data races.
   The smoke workflow retries asset downloads against transient server errors.
2. Tag `vX.Y.Z` (or `gh release create vX.Y.Z --target main`). `release.yml`
   builds `Portable-Clipboard-Translator-Windows-x64-vX.Y.Z.zip` (flat: exe,
   Setup.cmd, Start.cmd, Stop.cmd, setup.ps1, config.json, README.txt, LICENSE),
   the Linux tarball and the macOS arm64 / amd64 zips with their `.sha256`
   files, and attaches them. If a release already exists, the files are uploaded
   to it.
3. Afterwards bump `bucket/clipboard-translator.json` (`version`, `url`, `hash`
   = SHA-256 of that zip) in a follow-up PR.
4. Not done yet: winget manifests (submitting to `microsoft/winget-pkgs` is a PR
   to someone else's repo, so ask the maintainer first).

Releases, publishing, and merging PRs are the maintainer's call: ask before
doing them. PRs are merged with merge commits by the maintainer; point stacked
PRs at `main` (deleting a base branch auto-closes the PRs stacked on it).

## Licensing notes

The app is MIT. The model is not: PLaMo is under the PLaMo Community License
(business use, including internal use, needs prior registration with PFN). Keep
the "Built with PLaMo" item in the tray menu and the notice in the README. Don't
bundle the model or llama.cpp in the repo or in release zips; the setup
scripts and the Settings page download them from their official sources. This is not legal advice; don't
state license conclusions beyond what the README already says.

## Style

- `gofmt`; short comments that explain *why*; error messages the user can act
  on (they appear in message boxes and the log).
- Keep user-facing text in English in the UI; the README exists in English and
  Japanese, so update both when you change behaviour the README describes.
- Don't add dependencies without a strong reason: the exe and `go.mod` are
  deliberately dependency-free (`tools/mksyso` writes the Windows resources with
  the standard library for the same reason).
