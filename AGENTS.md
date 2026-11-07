# AGENTS.md

Orientation for AI coding agents (and new contributors). User-facing docs are
in [README.md](README.md) / [README.ja.md](README.ja.md); this file is about
working on the code.

## What this is

A Windows tray app. The user selects text, presses a hotkey (default
Ctrl+Alt+E), and the app puts the Japanese<->English translation on the
clipboard. Translation is fully local: the app starts a loopback-only
`llama-server` (llama.cpp) child process with the PLaMo 3 Translate 2B GGUF
model and talks to it over HTTP. Go, standard library only (no third-party
modules), `go 1.21` in `go.mod`, MIT licensed. Windows x64 only; other
platforms compile stubs so CI on Linux can run the logic tests.

## Commands

```bash
gofmt -l .          # must print nothing (CI fails otherwise)
go vet ./...
go test ./...       # Windows runs the Win32 tests too
go build -trimpath -buildvcs=false -ldflags "-H=windowsgui -s -w" -o ClipboardTranslator.exe .
```

On a Windows machine the Linux logic tests can be built with
`GOOS=linux go test -c -o <outside the repo>/app.test .` and run in WSL **with
the repo as the working directory** (some tests read repo files). CI runs them
on `ubuntu-latest` anyway.

The build is a GUI-subsystem exe (`-H=windowsgui`): there is no stdout, so use
`log` (written to `translator.log`), tray notifications, or `showError`.

## Code map

| File | Role |
| --- | --- |
| `main.go` | `main()`, language detection (`detectDirection`: Japanese script wins, URLs skipped), the HTTP `translate` call, `fatal`, `pathWithin` |
| `app.go` | `app` struct and the main loop `serve`: hotkey, tray commands, update results, translation results. `startTranslation` / `handleResult` / `abortTranslation` |
| `app_menu.go` | Builds the tray menu and handles its commands |
| `server.go` | `supervisor` for `llama-server` (start, stop, exit detection), `restartLimiter` (max 3 per 10 min) and backoff |
| `setup_flow.go` | Resolves config paths, checks the model/runtime exist, offers to run `setup.ps1` (embedded in the exe with `//go:embed`) |
| `config.go` / `settings.go` | `Config` + validation; order-preserving in-place `config.json` edits with a `.bak` |
| `update.go`, `update_windows.go` | "Check for updates" (GitHub API, only on click), version compare, install-kind detection (installer / Scoop / portable) |
| `history.go` | Last 10 translations (memory only) and `decidePaste` for auto-paste |
| `tray.go` (+ `_windows`/`_other`) | `Tray` interface; Windows impl is a hidden window + `Shell_NotifyIconW` |
| `hotkey_windows.go`, `clipboard_windows.go` | `RegisterHotKey`, simulated Ctrl+C (`captureSelectedText`), clipboard access, Ctrl+V |
| `setup.ps1`, `install.ps1` | Downloads model + llama.cpp; the one-line installer. Must parse on Windows PowerShell 5.1 *and* PowerShell 7 |
| `bucket/clipboard-translator.json` | Scoop manifest (this repo is also the bucket) |
| `tools/mksyso`, `tools/mkicon` | Generate `resource_windows_amd64.syso` (icon, version info, DPI manifest) and `assets/icon.*` |

## Things that bite

- **One thread owns the UI.** `main` calls `runtime.LockOSThread()`; the tray
  window and `RegisterHotKey` belong to that thread, and `serve` pumps its
  messages (poll tick is capped at 100 ms for that reason). Goroutines talk to
  the app only through channels (`results`, `updates`). Do not touch the tray or
  hotkey from another goroutine, and do not block `serve`.
- **Generation counter.** Every new translation bumps `app.generation`; a
  result with an older generation is dropped. Keep that when changing the flow.
- **Clipboard safety.** The result replaces the clipboard only if the text is
  still the original (`ReplaceIfUnchanged`, one `OpenClipboard` lock). Auto-paste
  is off by default and only fires for a fresh selection while the same window
  is in front.
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
- **No network on its own.** The app contacts GitHub only when the user clicks
  "Check for updates". Keep it that way and keep the README's privacy section
  true if you add anything.
- **Don't commit large or third-party files**: `models/`, `runtime/`, `*.exe`,
  logs (all in `.gitignore`).

## Tests

- The test binary doubles as a fake `llama-server` (`TestMain` in
  `helper_test.go`, env `CT_TEST_FAKE_SERVER=1`), so crash recovery and engine
  switching are tested without the real model. Seams like `newHotkey`,
  `showErr`, `foreground` and `paste` on `app` are replaceable in tests;
  `fakeTray` records tray calls.
- Behaviour that needs a real desktop (live hotkey, real selection, Ctrl+V into
  another app, the look of the tray menu) is not covered by `go test`. Say so
  when you could not check it; never inject keystrokes into the user's desktop.
- Windows-only code lives in `*_windows.go`; give any new one a
  `*_other.go` / `*_nonwindows.go` stub so the Linux CI job still builds.

## Release flow

1. Merge to `main` through a PR (CI = `.github/workflows/ci.yml`: gofmt, vet,
   test, build, PowerShell parse check, version-info check; plus Linux build/test).
2. Tag `vX.Y.Z` (or `gh release create vX.Y.Z --target main`). `release.yml`
   builds `Portable-Clipboard-Translator-Windows-x64-vX.Y.Z.zip` (flat: exe,
   Setup.cmd, Start.cmd, Stop.cmd, setup.ps1, config.json, README.txt, LICENSE)
   and attaches it. If a release already exists, only the zip is uploaded.
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
bundle the model or llama.cpp in the repo or in release zips; `setup.ps1`
downloads them from their official sources. This is not legal advice; don't
state license conclusions beyond what the README already says.

## Style

- `gofmt`; short comments that explain *why*; error messages the user can act
  on (they appear in message boxes and the log).
- Keep user-facing text in English in the UI; the README exists in English and
  Japanese, so update both when you change behaviour the README describes.
- Don't add dependencies without a strong reason: the exe and `go.mod` are
  deliberately dependency-free (`tools/mksyso` writes the Windows resources with
  the standard library for the same reason).
