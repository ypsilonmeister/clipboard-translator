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
- W3: RemoteDesktop/Clipboard, asynchronous capture and disabled Wayland auto-paste.
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
