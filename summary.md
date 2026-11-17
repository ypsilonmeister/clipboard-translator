# Issue #196

Implemented in the existing `fix/issue-196-minor-batch` worktree, based on
`origin/main` at `7f61678352ef00e879afe17bb10d40ff9b423b56`.

## 1. Cancel pending clipboard transfers

The Cancel command now uses the same condition as the menu: translating,
capturing, pending clipboard read, or pending result. Existing abort logic
clears the transfer and advances the generation to reject late results.

Evidence: `TestCancelFromMenuPendingResult` failed before the fix because the
translation was written after Cancel. It now passes: after enabling clipboard
writes, ticking retries, and delivering a late result, the source remains and
there are zero clipboard writes, pastes, or history entries.
`TestDesktopClipboardReadCancellation` also passes through `cmdCancel`.

## 2. Rotate translator.log

Startup rotates logs strictly larger than 1 MiB to `translator.log.1`, replacing
the previous rotation, and opens a new log. Smaller logs keep appending.
Uninstall removes the rotated log. Both READMEs describe the rotation.

Evidence: `TestOpenTranslatorLogRotation` failed for the oversized case before
the fix and now passes for missing, small, exactly-at-limit, and oversized logs.
It checks retained file contents, replacement of an older rotation, and a new
entry in the active log. `TestUninstallPreservesUnrelatedFiles` failed with the
new rotated-log fixture before the fix and now passes.

## 3. Restrict config.json permissions

New configs use `0600`. On non-Windows systems the existing file is chmodded
before writing, including empty existing files and unchanged saves. Updates
still preserve hard links. Windows skips chmod and retains its existing access
control behavior.

Evidence: `TestConfigFilePrivatePermissions` uses `os.Stat` to check `0600` for
new configs, new tokens, empty existing files, tokens added to existing `0644`
files, preserved tokens, and unchanged token saves. It compiles on Windows and
is explicitly skipped there. The complete config test binary cross-compiles
successfully with `CGO_ENABLED=0` for Linux amd64 and macOS amd64.

**The Linux run is left to the reviewer**, as requested. macOS execution is also
unverified. On either platform run:

```sh
go test ./internal/config -run '^TestConfigFilePrivatePermissions$' -count=1 -v
```

## 4. Keep the ready tooltip within Windows' limit

When the detailed ready tooltip exceeds 127 UTF-16 units, it shows the number
of working hotkeys. Failed registrations and portal bindings without an actual
trigger are excluded. Short tooltips keep their details.

Evidence: the eight-profile regression initially produced 301 UTF-16 units.
`TestReadyTipSummarizesLongHotkeyList` now passes with the complete text
`Clipboard Translator - ready (8 hotkeys)` (40 UTF-16 units), and verifies
counts with unavailable bindings. `TestReadyTipUTF16Limit` passes for short
text, exactly 127 units, and an over-limit label containing surrogate pairs.
Existing tests for partial hotkey registration failures also pass.

## Verification

Executed on Windows amd64 with Go 1.27.3:

- PASS: targeted Cancel, clipboard read, tooltip, log rotation, and uninstall
  regression tests, all with `-count=1`.
- PASS: `go test ./internal/config -count=1 -v`; the Unix permission test is
  skipped on Windows.
- PASS: `go test ./... -skip '^TestProcessIsPerMonitorDPIAware$' -count=1`.
  A final repeat against the completed code with `-json` also passed; all 13
  packages with tests passed, with zero failures (app package: 238.756 seconds).
- PASS: `go vet ./...`.
- PASS: `gofmt -l .` (no output) and `git diff --check`.
- PASS: Windows GUI build with `-trimpath -buildvcs=false` and
  `-ldflags '-H=windowsgui -s -w'`, output outside the repository.
- PASS: `install.ps1` and `setup.ps1` parse in Windows PowerShell 5.1 and
  PowerShell 7.
- PASS: `GOOS=linux CGO_ENABLED=0 go test -c ./internal/config` and
  `GOOS=darwin CGO_ENABLED=0 go test -c ./internal/config`, with output paths
  outside the repository.

The DPI-awareness test is excluded because it is environmental over SSH, as
requested. No live desktop selection, paste, or visual tray checks were run;
clipboard and tooltip evidence comes from the app's test fakes.
