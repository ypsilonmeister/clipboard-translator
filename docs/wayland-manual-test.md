# Wayland manual test sheet (#143, W5)

Use this sheet to evaluate a specific build on a specific desktop. Passing CI or a
portal API smoke test does not establish that the packaged application translates
correctly. The initial target is GNOME 48+ with the required portals; only GNOME 50.1
has been measured so far. KDE and wlroots evaluation belongs to W6.

The implementation and approved decisions are in [wayland-implementation.md](wayland-implementation.md).
User instructions are in [README.md](../README.md#wayland-workflow) and
[README.ja.md](../README.ja.md#wayland-での操作).

## Test environment and result record

Copy this record for each run. Use `PASS`, `FAIL`, `BLOCKED` or `NOT RUN` for every
case below; an unchecked checkbox means NOT RUN. Attach only sanitized diagnostics,
never clipboard contents from normal work, a restore token, the `wayland/` directory
or a Settings URL containing its authentication token.

```text
Date / tester:
Build commit / PR / artifact URL / archive SHA-256:
Distribution / architecture:
Desktop version / portal frontend and backend versions:
Session type / keyboard layout:
Model / CPU or GPU runtime:
Fresh or existing app folder / consent already stored?:
Test case IDs and results:
Unexpected dialogs / errors (sanitized):
Cleanup completed / remaining blockers:
```

Record `echo "$XDG_SESSION_TYPE"`, `echo "$XDG_CURRENT_DESKTOP"`, `uname -m` and
`gnome-shell --version` on GNOME. Record portal versions using the distribution's
package manager. Do not infer portal capabilities solely from the GNOME version.

## Preparation

- Use a disposable VM/test account or a desktop explicitly designated for this
  project. Do not automate input or screenshots on somebody's ordinary working desktop.
- Keep an independent SSH/console connection available. Use Ctrl+Shift+F9/F10 for
  test profiles, or record the actual alternatives chosen in the portal dialog.
  Avoid Ctrl+Alt+function-key chords: they can switch Linux virtual terminals.
- Unlock the graphical session. Open a disposable graphical text editor, not a
  terminal in which Ctrl+C might interrupt a process. Use only the known texts below.
  Keep its focus while testing selection capture; Wayland cannot verify that focus.
- Quit other Clipboard Translator instances. Use a dedicated writable package folder,
  retain any existing installation separately, and record which folder is under test.
- Verify the artifact's checksum before extracting. For a source build, use the same
  Linux dependencies as AGENTS.md and `bash tools/package-linux.sh dev`, then extract
  the generated archive into a writable folder. A non-cgo build cannot test desktop behavior.
- Set up an actual model/runtime using the README's explicit setup/download flow if
  needed. A fake server is appropriate for automated app tests, but cannot pass the
  real translation cases in this sheet. Record the model/runtime used.
- Expect the test to replace the clipboard with known fixture text. Do not collect
  or save the pre-test clipboard merely to restore it afterwards.

Known texts:

```text
こんにちは。今日はいい天気です。
Hello. The weather is nice today.
Newer controlled copy — keep this text.
```

## Packaged-application acceptance cases

All cases below are **NOT RUN** in this W5 documentation change. Record actual
observations against the build being evaluated; do not check these based on the
separate W1-W4 evidence ledger.

### Startup, consent and basic translation

- [ ] **S1 — First start:** Launch the extracted binary in the unlocked Wayland session.
  Expected: model startup and the menu remain responsive; shortcut consent precedes
  keyboard/clipboard consent. There is a control window if no compatible tray host exists.
  Do not require first-use dialogs when this app id already has stored consent.
- [ ] **S2 — Actual trigger:** Change the requested shortcut in the portal dialog.
  Expected: Settings' active shortcut and Quick start show the desktop's chosen trigger
  after consent completes; the requested config value is not silently overwritten.
- [ ] **T1 — Japanese to English:** Select the known Japanese sentence in the editor,
  press the actual trigger once and release its modifiers. Wait for translation-ready
  status, then paste manually into an empty area. Expected: an English translation,
  source selection left intact until the manual paste, no automatic Ctrl+V.
- [ ] **T2 — English to Japanese:** Repeat with the known English sentence.
  Expected: a Japanese translation. Judge meaning, not an exact model-dependent string.
- [ ] **T3 — Multiple profiles:** Add a second profile with a distinct actual trigger
  and a different target or bilingual output. Expected: each trigger invokes its own
  profile; Settings and Quick start reflect the active triggers after rebinding.
- [ ] **T4 — Clipboard input:** Set one profile to `input: "clipboard"`; copy known text
  yourself, then focus a different harmless area and press its trigger. Expected: the
  copied text is translated without Ctrl+C or selection capture, and never auto-pasted.
  This profile still needs RemoteDesktop/Clipboard consent.
- [ ] **T5 — History copy:** After a translation, use Recent translations to copy the
  translation, original and bilingual forms separately. Expected: one completion
  notification after each successful copy, correct contents on manual paste, no input injection.

### Clipboard and input limits

- [ ] **C1 — Newer copy during inference:** Start translating the Japanese sentence,
  then copy the known newer controlled text before inference completes. Expected:
  the newer copy is preserved when detected; the translation remains in Recent
  translations. Compare/write is best effort, so also record any observed race.
- [ ] **C2 — New request / cancel:** Start a translation and press another profile's
  trigger, then separately test Cancel translation during a slow operation. Expected:
  older generations are not retried or later reported as new completions. A clipboard
  write already submitted to the compositor cannot be undone by a later cancellation.
- [ ] **C3 — Automatic-paste guard:** In a disposable test configuration, save
  `auto_paste: true`, restart, and translate known selected text. Expected: the selection
  is not replaced automatically; Settings explains Wayland's disabled automatic paste.
  The saved preference may remain true for use in other desktop sessions.
- [ ] **C4 — No selection / empty clipboard:** Test with known clipboard text but no
  selection, then an empty clipboard. Expected: no-selection fallback can translate
  the known clipboard after the copy timeout; empty text is skipped. Record the app's
  response when the editor rejects Ctrl+C rather than assuming every application copies.
- [ ] **C5 — Long-held modifiers:** In the disposable editor only, hold the shortcut
  modifiers after pressing, then release them. Record whether copy succeeds or reports
  held keys. Do not assume Deactivated means every physical modifier is released.
  Check that no virtual Ctrl/key remains held afterwards; document failures as limitations.
- [ ] **C6 — Layouts / application differences:** Repeat T1/T4 on each available JIS,
  AZERTY, Dvorak or non-Latin layout and in a second graphical application, recording
  the actual shortcuts. A result on one layout/application does not validate another.

### Recovery, persistence and lifecycle

- [ ] **R1 — Shortcut refusal:** Refuse shortcut consent on a fresh test account or
  revoke only this app's shortcut permission. Expected: the app stays open, explains
  missing access, and Retry Wayland permissions can bind shortcuts again. Other
  accepted profiles and menu commands remain usable where applicable.
- [ ] **R2 — Keyboard/clipboard refusal:** Refuse the remote-control dialog. Expected:
  the app stays open with actionable status; accepting Retry Wayland permissions
  restores access without unnecessarily replacing the shortcut session.
- [ ] **R3 — Revocation while running:** Revoke only this app's keyboard/clipboard
  access through the desktop's permission/session UI. Expected: pending access stops,
  status changes, and retry requests authorization again. Do not reset other apps' permissions.
- [ ] **R4 — Lock/unlock:** Lock the designated test session and attempt/retry access,
  then unlock it and retry from the control window. Expected: refusal/inhibition is
  recoverable; menu/quit remain usable. Record whether already-running access survives
  the lock rather than assuming a compositor-wide policy.
- [ ] **R5 — Application restart:** Quit normally, then reopen the same package folder.
  Expected: actual bindings return and saved keyboard/clipboard consent is restored
  without a new prompt when allowed by the desktop. Repeat T1. Record any renewed prompt
  and distinguish revocation/restore failure from normal persistence.
- [ ] **R6 — Desktop-session restart:** Log out/in on the test account and reopen the
  same installation. Repeat T1 and record binding/permission persistence separately
  from R5. W3's API reconnection test does not establish this case.
- [ ] **R7 — Portal unavailable / older desktop:** On a separate disposable session
  lacking a required capability, launch the app. Expected: it stays open, identifies
  the required portals and offers recovery. Do not stop a shared desktop portal just
  to create this condition; fake-portal CI already exercises failure handling.
- [ ] **R8 — Startup and moved executable:** Enable Start at login, verify the owned
  application-id launcher, then test a later login. Quit, move the installation,
  launch it and toggle startup off/on. Expected: Exec paths point to the new executable,
  only one owned login launcher exists, and disabling preserves other installations' entries.
- [ ] **R9 — Exit / X11 regression:** Test menu Quit and closing the fallback window.
  Expected: app and child server stop; clipboard retention after exit depends on the
  clipboard manager. In a separate Xorg session, repeat selection/clipboard and optional
  auto-paste checks: X11 still uses PRIMARY, with no Wayland consent dialogs.

## Opt-in native API smoke tests

These are separate from the packaged-app checklist. Run on the designated, unlocked
Wayland test desktop, in the repository working directory, with the graphical session's
D-Bus/runtime environment. An SSH shell may need `DBUS_SESSION_BUS_ADDRESS`,
`XDG_RUNTIME_DIR`, `WAYLAND_DISPLAY`, `XDG_SESSION_TYPE=wayland` and `GDK_BACKEND=wayland`.
Use the real session's values, not another user's bus or a guessed display number.

```bash
CT_TEST_PORTAL_W1=1 go test -tags portaltest -run '^TestPortalNativeSessions$' -v ./internal/platform
CT_TEST_PORTAL_W2=1 CT_TEST_PORTAL_PRESS=1 go test -tags portaltest -run '^TestPortalNativeShortcutBindings$' -v -timeout 5m ./internal/platform
CT_TEST_PORTAL_W3=1 go test -tags portaltest -run '^TestPortalNativeRemoteClipboard$' -v -timeout 6m ./internal/platform
CT_TEST_PORTAL_W4=1 go test -tags portaltest -run '^TestPortalNativeDesktopRuntime$' -v -timeout 6m ./internal/platform
```

W1 does not bind or inject input. W2 asks the tester to accept and press the two
shortcuts. W3 asks for keyboard/clipboard consent and Start in its disposable editor.
W4 asks for both consents, then Start and one Ctrl+Shift+F9 press; it reuses the W3
editor title. W3/W4 explicitly paste only into that focused fixture and stop before
further input if it loses focus. They install known clipboard text before reading,
leave known fixture text afterwards, and close their sessions. Test-only desktop entries
and scoped permissions are removed. W2/W4 need GNOME's `gsettings`, `dconf`, `gdbus`
and Python; W3/W4 also need python3-gi for permission-store cleanup.

CI uses `go test -tags portaltest -run '^TestPortal' ./internal/platform` without these
opt-in environment variables: it compiles the fixtures, runs fake-portal tests and skips
live desktop operations. X11 integration uses its own isolated Xvfb display.

## Existing evidence ledger

These observations were recorded during W1-W4, on `yuya-ubuntu-vm` (Ubuntu 26.04,
GNOME 50.1), on 2026-11-10. They are narrower than the acceptance cases above.

| Stage / evidence | Observed result | Does not establish |
| --- | --- | --- |
| [W1, PR #147](https://github.com/ypsilonmeister/clipboard-translator/pull/147) | Private connection registration, both session creations and cleanup | Consent, shortcut activation, clipboard transfer |
| [W2, PR #148](https://github.com/ypsilonmeister/clipboard-translator/pull/148) | Actual triggers and each maintainer-pressed Ctrl+Shift+F9/F10 activation | Production app startup or synthetic copy/paste |
| [W3, PR #154](https://github.com/ypsilonmeister/clipboard-translator/pull/154) | Known Japanese selection capture, UTF-8 FD transfer, explicit fixture paste and token-based reconnection without a second prompt | Inference, app/session restart, long-held modifiers |
| [W4, PR #156](https://github.com/ypsilonmeister/clipboard-translator/pull/156) | Production NewHotkey/NewClipboard/BeginSelectionCapture API, actual trigger, controlled capture/compare/write, explicit fixture paste and automatic-paste guards | Complete packaged-app workflow, inference, alternative layouts/compositors |

W4's [CI run](https://github.com/ypsilonmeister/clipboard-translator/actions/runs/42770192639)
passed Windows, Linux and both macOS runners, including fake portals, X11/Xvfb and
non-cgo logic. No live Wayland consent or translation quality claim follows from CI.

## Cleanup and release decision

Quit the app/fixtures and verify no test child server remains. Disable only startup
entries created for the test. Remove only the test account's or explicit .W1Test through
.W4Test ids' temporary registrations; do not wipe the permission store or other apps'
shortcut settings. Do not delete an ordinary installation's restore token as routine cleanup.
Remove task-specific temporary source/build folders only after verifying their paths.

Attach the completed record with any failures/blockers. A blank checklist is not an
acceptance report. Leave unmeasured desktops and cases marked NOT RUN, and keep the
experimental support wording until the maintainer reviews the evidence. Merging and
releasing remain the maintainer's decision.
