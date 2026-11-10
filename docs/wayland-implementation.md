# Wayland implementation plan (#143)

## Decisions

The maintainer approved the recommended Q1-Q6 choices in #143 during implementation:

- Use GIO/GDBus through cgo, behind a testable Go interface, without new Go modules.
- Use `io.github.ypsilonmeister.ClipboardTranslator` as the app id.
- Disable auto-paste on Wayland initially because focus identity cannot be checked.
- Register a desktop entry in the user's applications directory and align autostart with that id.
- Target GNOME 48+ first; measure other desktops before claiming support.
- Keep X11 integration CI, add fake-portal tests, and use the maintainer's test VM for consent and real Wayland checks. Revisit headless GNOME CI after W3.

## Stages

Each stage is a separate PR targeting main. This document records implementation status,
not a claim that the application already supports Wayland.

- W0: separate backend-neutral GTK C code from X11 C code and select the Linux session backend.
  Implemented in this change. Wayland continues to report the existing unsupported-session error;
  no portal sessions or consent prompts are introduced.
- W1: portal connection, app desktop registration, session lifecycle and private restore-token storage.
  Implemented as an opt-in foundation in this change; the application entry point is not wired yet.
- W2: GlobalShortcuts sessions, profile bindings and actual trigger display.
  Implemented as an opt-in registry; production backend activation remains W4.
- W3: RemoteDesktop/Clipboard, asynchronous capture and disabled Wayland auto-paste.
  Implemented as an opt-in controller; application integration remains W4.
- W4: capability/consent recovery UX and real shortcut descriptions in Settings and the guide.
- W5: user documentation and a manual Wayland test sheet.
- W6: measure KDE Plasma and a wlroots compositor before extending support.
- W7: optional StatusNotifierItem/dbusmenu tray.

Research evidence remains in #133 and the architecture/acceptance criteria in #143.

## W0 boundaries

`native_linux.c` contains GTK menus, dialogs, tray/control-window UI and pumping,
with no Xlib headers or calls. `native_x11_linux.c` retains X11 initialization,
hotkeys, selection tracking, clipboard access and XTest input. `ct_init` remains
an X11 entry point and calls the shared `ct_gtk_init` first.

`selectLinuxBackend` gives Wayland priority over DISPLAY (Xwayland can expose both).
The platform entry point refuses that backend until the portal stages are ready.
The existing X11 initialization, errors and exported platform API remain intact.

## W1 boundaries

`portal_linux.go` owns the UI-thread lifecycle over a small fakeable wire interface.
Registration completes before creating GlobalShortcuts and RemoteDesktop sessions.
`Step` handles asynchronous replies, denial, timeout, session closure and service loss.
Closing the dedicated D-Bus connection releases pending requests and sessions; retry
uses a fresh client/connection, so Registry registration is never repeated on a peer.

`portal_gio_linux.go` / `portal_native_linux.c` use GIO on a private session-bus
connection. Responses are subscribed before requests, filtered to this peer's object
paths and correlated by random request tokens. The C callbacks queue events; they do
not access Go/app state. GIO errors are converted to generic messages so arbitrary
portal error bodies cannot expose credentials in app logs. The connection opens at
startup, while subsequent registration and session calls are asynchronous.

The application desktop entry is written atomically to XDG_DATA_HOME/applications
(with the usual ~/.local/share fallback), updating Exec after the executable moves.
The existing Desktop Entry quoting helper is reused. Autostart remains on the X11
path for now; its Wayland app-id alignment belongs to W4 when the backend is enabled.

Restore tokens are stored under <app folder>/wayland/restore-token, separately from
config.json. The folder requires mode 0700, the token requires mode 0600, writes use
atomic replacement, and reads refuse symlinks/nonregular files. The token is not yet
sent to a portal: SelectDevices, Start, token rotation and consent belong to W3.

No shortcuts are bound and no remote input, clipboard access, consent dialog or
portal connection is initiated by the application in W1. Wayland remains disabled
until the subsequent integration stages are ready. Non-cgo Linux builds retain a stub.

### Tests

- Fake-wire tests cover registration ordering, reply order, unknown/duplicate replies,
  denial, malformed response, connection loss, early session closure, timeout and cancellation.
- File tests cover desktop Exec updates/quoting and private token permissions/rotation,
  including symlink refusal and preservation of unrelated files.
- Opt-in native smoke test (designated graphical test machine only):
  `CT_TEST_PORTAL_W1=1 go test -tags portaltest -run '^TestPortalNativeSessions$' -v ./internal/platform`.
  It creates a temporary .W1Test desktop entry and two connections in sequence, registers
  and creates sessions, then disconnects. It does not bind shortcuts or start remote control.
  GNOME must be unlocked; screen lock can inhibit RemoteDesktop session creation.

API references:
- https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.host.portal.Registry.html
- https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.GlobalShortcuts.html
- https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html
- https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Session.html

## W2 boundaries

`portal_shortcuts_linux.go` coalesces profile registration into one BindShortcuts call.
The returned objects implement Hotkey and expose TriggerDescription from the compositor,
not from the requested config key. The preferred trigger maps Win/Super to LOGO.
Activated fires its own profile once until Deactivated. ShortcutsChanged updates the
actual descriptions without replacing unrelated entries. Missing bindings remain inactive.

When profiles change, the registry closes/recreates only the shortcuts session and binds
its complete replacement set. The RemoteDesktop session remains alive. Old-session signals
and obsolete dialog replies cannot activate a newly reused profile id. Refusal and timeout
clear active bindings; timeout also closes the pending Request and session.

The app summary/guide can read the optional TriggerDescription interface. Settings exposes
an output-only actual_hotkeys list, shown as "Currently active shortcut" without modifying
requested config values. This prepares real-trigger display, while W4 still supplies
production backend selection, asynchronous readiness/status refresh and consent recovery.

Native GNOME test (explicitly designated, unlocked test desktop only):
`CT_TEST_PORTAL_W2=1 CT_TEST_PORTAL_PRESS=1 go test -tags portaltest -run '^TestPortalNativeShortcutBindings$' -v -timeout 5m ./internal/platform`.
The maintainer accepts the dialog, then presses Ctrl+Shift+F9 and Ctrl+Shift+F10 once.
Do not use Ctrl+Alt+function-key chords: they may switch Linux virtual terminals.
The test does not inject input, translate, or access the clipboard. It closes its sessions,
removes the temporary .W2Test desktop entry and removes only that test id's GNOME shortcut
settings/permission-store entry, preserving other applications. This optional test requires
python3, gsettings, dconf and gdbus for scoped cleanup; CI runs the fake-wire tests instead.

## W3 boundaries

`portal_remote_linux.go` requests keyboard-only RemoteDesktop access and Clipboard
access before Start. Authorization, refusal, timeout, revocation and reconnection
advance on UI ticks. A remote-session failure preserves the GlobalShortcuts session;
losing the private D-Bus connection still closes both. A saved restore token is
consumed before SelectDevices and replaced by the token returned by Start. Normal
shutdown retains it; explicit session revocation removes it. Tokens and clipboard
contents are never included in diagnostic messages.

`portal_clipboard_linux.go` implements nonblocking Clipboard operations. Pending
operations return `ErrClipboardPending`; W4 must retry them from the UI loop, including
result application and history copy, without sleeping or spinning. Reads use cancellable
Unix FD transfers, UTF-8/plain-text MIME negotiation, a 32 MiB limit and a five-second
IO deadline. Owner changes invalidate cached data and cancel obsolete reads. Writes
wait for both the method acknowledgement and ownership notification; a subsequent
copy is preserved when completing a compare/write. This remains best effort: the
portal has no atomic read-and-replace operation. Offered data is served to external
consumers through bounded asynchronous FD writes and SelectionWriteDone.

`portal_capture_linux.go` waits for shortcut deactivation plus a short release grace,
requests Ctrl+C asynchronously, then waits for clipboard ownership to settle. Copy
failure does not fall back to stale text. The controller finishes/releases its virtual
keys even if the caller drops a capture task, and rejects overlapping key sequences.
GlobalShortcuts deactivation is not a physical modifier-state query: users must release
the hotkey modifiers, and physical held-modifier behavior remains a manual-test limit.
Wayland capture always reports `fromSelection=false`, because the focused application
cannot be identified reliably. This prevents automatic paste even if configured.
The paste sequence is available only to the explicit, controlled native test in W3.

The production backend is still disabled. No portal consent prompt or keyboard/clipboard
operation is initiated by the ordinary application until W4 connects these components.
Consequently user-facing README support claims remain unchanged.

### Tests

Fake-wire tests exercise authorization ordering, token consumption/rotation, refusal,
revocation/retry, owner invalidation, cancelled reads, acknowledgement/owner ordering,
compare/write protection, UTF-8/size validation, transfer completion, timeouts, virtual
key cleanup, overlapping-key rejection and capture without automatic paste. Existing
X11 and non-cgo tests continue to run separately.

Opt-in native test, only on an explicitly designated disposable Wayland desktop:
`CT_TEST_PORTAL_W3=1 go test -tags portaltest -run '^TestPortalNativeRemoteClipboard$' -v -timeout 6m ./internal/platform`.
It requires python3-gi for scoped permission cleanup. Accept keyboard/clipboard access,
then click Start in the disposable "Clipboard Translator W3" window and keep it focused.
The test first installs known clipboard contents before reading, copies a known Japanese
selection, compares/replaces it, and explicitly pastes into that same fixture. It stops
before further key requests if the fixture loses focus. It then reconnects using the
saved token without another consent prompt. Sessions, the .W3Test desktop entry and only
that test application's permission-store records are removed afterwards. The fixture
leaves its known test text on the clipboard rather than reading/restoring previous data.

Verified on the maintainer's `yuya-ubuntu-vm` (100.78.89.32), Ubuntu 26.04 / GNOME 50.1:
UTF-8 selection capture, FD transfer to the GTK paste consumer, explicit fixture paste,
and permission-token reconnection all passed. This does not validate production app
integration, translation inference, physical held modifiers, KDE or wlroots.

API references:
- https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html
- https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Clipboard.html
