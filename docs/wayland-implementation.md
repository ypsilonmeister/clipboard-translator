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
