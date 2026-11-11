<p align="center"><img src="assets/icon.png" alt="Clipboard Translator icon" width="128"></p>

# clipboard-translator

English | [日本語](README.ja.md)

Local clipboard translation for Windows (the macOS and Linux builds are experimental).
Select text in any app, press **Ctrl+Alt+E**, and the translation lands on your clipboard,
ready to paste with **Ctrl+V**. It translates between 53 languages
(Japanese <-> English by default).

Everything runs on your own PC, with [llama.cpp](https://github.com/ggml-org/llama.cpp)
and [PLaMo 3 Translate (GGUF)](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF) by
Preferred Networks. No text leaves your machine: no cloud, no account, no API key and no
character limits — so you can use it for the text you would not paste into an online
translator, like work email, code comments or internal documents.

Choose the model size in Settings (0.8B / 2B / 8B). There is nothing else to set up:
no Ollama, no Python — first-run setup runs in the app itself.

**Built with PLaMo.** The translation model is PLaMo by Preferred Networks and is
used under the [PLaMo Community License](https://plamo.preferredai.jp/info/plamo-community-license-ja);
see [Licenses](#licenses) before using the app for business.

> **Status: prototype.** It has not been tested on a wide range of PCs or GPU
> drivers. Use at your own risk.

## Which download?

Every [release](https://github.com/ypsilonmeister/clipboard-translator/releases) has one file per platform
(the macOS and Linux files have a `.sha256` file next to them; the Windows zip's SHA-256 is in the release notes):

| Your computer | File | Instructions |
| --- | --- | --- |
| Windows 10/11 x64 | install script, Scoop, or `Portable-Clipboard-Translator-Windows-x64-<version>.zip` | [Windows](#install-windows-1011-x64) |
| Mac with Apple silicon (M1 or later) | `clipboard-translator-macos-arm64-<version>.zip` | [macOS](#macos-experimental-macos-13) |
| Mac with an Intel processor | `clipboard-translator-macos-amd64-<version>.zip` | [macOS](#macos-experimental-macos-13) |
| Linux x86_64, X11 session | `clipboard-translator-linux-amd64-<version>.tar.gz` | [Linux](#linux-x11-experimental) |

macOS and Linux are experimental, and no Linux arm64 build is published.

## Install (Windows 10/11 x64)

The llama.cpp builds need the **Visual C++ Redistributable (x64)**, which most PCs
already have. On a PC without it (or with a very old one) `llama-server` exits at
once with code `0xC0000135` and prints nothing; the app then shows a message with
the fix. Install it with `winget install --id Microsoft.VCRedist.2015+.x64 -e`
or from <https://aka.ms/vs/17/release/vc_redist.x64.exe>.

### Install script (recommended)

Open PowerShell (no administrator rights needed) and run:

```powershell
irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1 | iex
```

The script downloads the latest release and verifies its SHA-256, installs it to
`%LOCALAPPDATA%\ClipboardTranslator`, downloads the PLaMo model (over 1 GB) and a
fixed llama.cpp build (verified against pinned SHA-256 hashes; CUDA / Vulkan / CPU
is chosen automatically; CPU is always installed as a fallback), adds a Start menu
shortcut and an *Apps & features* entry, and starts the app. Read [`install.ps1`](install.ps1)
first if you like; it is short.

- **Update:** run the same command again, or use *Check for updates* in the tray
  menu. The tray's *Update* runs the installer script from the release being
  installed. Your `config.json`, the model and llama.cpp are kept.
- **Uninstall:** Settings > Apps > *Clipboard Translator*, or
  `& ([scriptblock]::Create((irm https://raw.githubusercontent.com/ypsilonmeister/clipboard-translator/main/install.ps1))) -Uninstall`
  (this deletes the app's own files, including the model and llama.cpp, and the
  folder itself if nothing else is left in it). The installer only installs into
  an empty or new folder, so other files are never mixed in.
- **Options** (environment variables, because `| iex` cannot take parameters):
  `CT_VERSION=v0.5.0` pins a release, `CT_INSTALL_DIR=D:\Apps\CT` chooses the
  folder, `CT_SKIP_SETUP=1` skips the model download, `CT_NO_LAUNCH=1` does not
  start the app. Example: `$env:CT_VERSION='v0.5.0'; irm ... | iex`. If you saved
  the script, the same options exist as parameters (`-Version`, `-InstallDir`,
  `-SkipSetup`, `-NoLaunch`, `-Force`).

### Scoop

```powershell
scoop bucket add clipboard-translator https://github.com/ypsilonmeister/clipboard-translator
scoop install clipboard-translator
```

`models`, `runtime` and `config.json` are kept across `scoop update`. This route
is new and has had less testing than the install script.

### Portable zip (manual)

Download `Portable-Clipboard-Translator-Windows-x64-<version>.zip` from
[Releases](https://github.com/ypsilonmeister/clipboard-translator/releases) and
extract it to a writable folder. Run `ClipboardTranslator.exe` (or `Start.cmd`);
if the model or llama.cpp is missing it offers to download them. `Setup.cmd` does
the same download up front, and `Stop.cmd` stops the app and its `llama-server`
child process. "Start with Windows" registers the exe path under `HKCU\...\Run`;
if you move the folder, toggle it off and on again.

Manual backend choice:

```powershell
powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Backend vulkan   # or cuda / cpu
```

### Choosing the model size

Setup installs PLaMo 3 Translate **2B** (1.8 GB) by default. To use another size:

```powershell
powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Model 8b   # or 0.8b / 2b
```

| `-Model` | File | Download |
| --- | --- | --- |
| `0.8b` | `plamo-3-translate-0.8b-Q4_K_M.gguf` | 0.6 GB |
| `2b` (default) | `plamo-3-translate-2b-Q4_K_M.gguf` | 1.8 GB |
| `8b` | `plamo-3-translate-8b-Q4_K_M.gguf` | 5.3 GB |

You can also choose and download models on the *Settings page* (below). A
bigger model needs more disk space, memory and time per translation; a
smaller one is faster. The 31B model is not offered. `-Model` downloads the file
(checked against a pinned SHA-256) and sets `model_file` in `config.json`; the
old model file stays in `models\` until you delete it. Setup without `-Model`
(including updates through Scoop) keeps the model `config.json` names. All sizes
use the same [PLaMo Community License](https://plamo.preferredai.jp/info/plamo-community-license-en).
On Linux and macOS the option is `--model 0.8b|2b|8b` (for example
`bash setup.sh --backend cpu --model 8b`).

## macOS (experimental, macOS 13+)

### Install steps

1. **Pick the file for your Mac.** Apple menu > *About This Mac* shows the chip
   (*Apple M...* means `arm64`, *Intel* means `amd64`), or run `uname -m` in Terminal
   (`arm64` or `x86_64`).
2. **Download** `clipboard-translator-macos-<arch>-<version>.zip` and its `.sha256` file from
   Releases, then check the download (in the folder that holds both files):

   ```bash
   shasum -a 256 -c clipboard-translator-macos-<arch>-<version>.zip.sha256
   ```

3. **Unzip** it and move **Clipboard Translator.app** to `/Applications` (or wherever it should stay).
4. **Open it for the first time.** The app is not notarized, so macOS may refuse to open it
   (the exact prompts differ between macOS versions). Either open *System Settings > Privacy & Security*
   and choose **Open Anyway** after the first blocked attempt, or remove the download quarantine flag
   before opening it:

   ```bash
   xattr -dr com.apple.quarantine "/Applications/Clipboard Translator.app"
   ```

5. **Start the app** and accept the download prompt (model over 1 GB). Then grant access in
   *System Settings > Privacy & Security > Accessibility* so it can copy the selection.

The details below explain each part.

CI produces separate Apple Silicon (`arm64`) and Intel (`amd64`) app bundles:
`clipboard-translator-macos-<arch>-<version>.zip`, with SHA-256 files. Development
builds are available from the PR's Actions artifacts; tagged builds are attached to
Releases. Extract the matching zip and move **Clipboard Translator.app** to its final
location (for example Applications) before granting permissions or enabling autostart.
These are **ad-hoc signed development builds**, without Developer ID signing or
notarization. Gatekeeper acceptance and public-distribution signing are not verified.
Accessibility access may need to be granted again after replacing a development build.

First-run setup runs in the app itself (its `setup` subcommand); no Python is needed.
Start the app and accept the explicit
download prompt. A responsive setup window offers Cancel; download details go to `setup.log`.
A fixed official llama.cpp macOS archive is verified against its pinned SHA-256 and supports
both CPU and Metal. Metal is selected by default; CPU is available as a fallback and in Settings (Translation engine card).
As on Windows and Linux, a fresh download of the model and the chat template is checked against pinned SHA-256 values; the size check only guards a file that is already on disk.

The app runs in the **menu bar**, without a Dock icon. The default hotkey is
**Control+Option+E** (`Ctrl+Alt+E` in config); paste with **Command+V**.
Grant the app access in **System Settings > Privacy & Security > Accessibility** to
enable selected-text capture (synthetic Cmd+C) and optional auto-paste (Cmd+V).
The menu's **Open Accessibility Settings...** opens that page; permissions are never
requested automatically. Without access, manually copy text with Cmd+C, press the
hotkey, and paste the translation yourself. Auto-paste is off by default. The app
rechecks the frontmost process/window after modifiers are released, but cannot reliably
detect a changed selection in the same window. Its pasteboard compare/write is best
effort, not atomic. Sandbox restrictions and Secure Input may prevent some operations.

The app keeps writable data outside the bundle, in:

```text
~/Library/Application Support/ClipboardTranslator/
    config.json, setup.sh, setup.log, translator.log, server.log
    models/, runtime/
```

The embedded macOS script is written there as `setup.sh`. To reinstall/select CPU manually,
quit the app, then run `bash "$HOME/Library/Application Support/ClipboardTranslator/setup.sh" --backend cpu`
(or `metal`). **Start at login** writes only
`~/Library/LaunchAgents/com.github.ypsilonmeister.clipboard-translator.plist`; the setting
applies at the next login. Toggle it after moving the app. Quit from the menu before
replacing the app to update it; data in Application Support is retained. Status/error
notifications appear in the menu-bar tooltip and log, without requesting Notification
Center access. Errors requiring intervention also use native dialogs.

To build on a Mac (or a GitHub-hosted macOS runner), install Go and Xcode Command Line Tools,
then run `bash tools/package-macos.sh dev`. This builds the runner's native architecture,
creates `.app` metadata/icons, ad-hoc signs/verifies the bundle, and writes a zip/checksum
under `dist/`. CI uses separate Apple Silicon and Intel runners. Native smoke checks cover
AppKit startup, pasteboard round trips, Carbon hotkey registration/event dispatch, and
menu commands without injecting input or granting Accessibility access. Actual inter-app
selection/paste, non-US keyboard use, Gatekeeper behavior and Metal performance need
manual testing. Cross-compilation with `CGO_ENABLED=0` only verifies portable logic/stubs.

## Linux (X11 / Wayland, experimental)

### Install steps

1. Check your session with `echo "$XDG_SESSION_TYPE"`: use X11 (Xorg), or a Wayland
   desktop with the portals described under *Wayland workflow* below.
2. Install the dependencies (Ubuntu 24.04, below), then download
   `clipboard-translator-linux-amd64-<version>.tar.gz` and its `.sha256` file from Releases
   (x86_64 only). Check it, extract it into a dedicated folder in your home directory and run it:

   ```bash
   sha256sum -c clipboard-translator-linux-amd64-<version>.tar.gz.sha256
   tar -xzf clipboard-translator-linux-amd64-<version>.tar.gz -C ~
   cd ~/clipboard-translator-linux-amd64-<version>
   bash setup.sh            # downloads the model and the official CPU runtime
   ./clipboard-translator
   ```

The details below explain each part.

Linux support is the first phase of [#13](https://github.com/ypsilonmeister/clipboard-translator/issues/13).
Wayland support is experimental, initially targeting GNOME 48+ with current
GlobalShortcuts, RemoteDesktop keyboard and Clipboard portals. Accept the shortcut
and keyboard/clipboard permission dialogs, then release the hotkey modifiers after
pressing. Translations go to the clipboard; **automatic paste is disabled on Wayland**.
The desktop chooses the actual shortcuts, shown in Settings and Quick start. If access
is denied or revoked, use **Retry Wayland permissions** in the control-window menu.
Without a tray host, keep the control window open (closing it quits the app).
Older desktops without these portals still need an Xorg/X11 session. KDE and wlroots
have not been verified. Only the GNOME 50.1 test VM has been measured so far.

On Ubuntu 24.04, install the runtime dependencies:

```bash
sudo apt-get install libgtk-3-0t64 libxtst6 xdg-utils libnotify-bin
```

Extract the Linux `tar.gz` from Releases into a writable, dedicated directory in your home
folder, then run:

```bash
cd clipboard-translator-linux-amd64-<version>
bash setup.sh                  # downloads the model and official CPU runtime
./clipboard-translator
# Optional, explicit Vulkan installation (requires a working Vulkan driver):
bash setup.sh --backend vulkan
```

Setup runs in the app binary (`setup.sh` forwards to its `setup` subcommand); no Python
is needed. Runtime archives are checked against their pinned SHA-256.
CPU is the default; Vulkan falls back to the installed CPU runtime if it cannot start.
A fresh download of the model and the chat template is checked against pinned SHA-256 values, as on Windows; the size check only guards a file that is already on disk.
CPU and Vulkan runtime files retain their upstream layout and shared-library links.
The runtime archive's system requirements also apply; older distributions may need a
locally built compatible llama.cpp runtime at `runtime/cpu/llama-server`.

### X11 workflow

Select text and press the hotkey. X11 reads the **PRIMARY** selection directly,
without simulating Ctrl+C, and stages it on CLIPBOARD before translation. Only a selection
made while the window of the hotkey press had the focus is eligible for auto-paste; if PRIMARY
was last changed in another window, or that cannot be told, the text is still translated but
never auto-pasted. If PRIMARY has no text, the existing CLIPBOARD text is used.
X11 has no atomic clipboard compare-and-set: the app compares text before writing, but unlike
Windows it cannot eliminate races with other clients.

Auto-paste is off by default and uses XTest Ctrl+V; it checks the focused X11 window
again after modifiers are released. Changes to a selection within the same window cannot
be detected reliably, so use manual paste when that matters.

### Wayland workflow

The initial target is GNOME 48+ with GlobalShortcuts, RemoteDesktop keyboard and
Clipboard portals. GNOME 50.1 on the designated Ubuntu 26.04 VM has been measured;
GNOME 48/49, KDE and wlroots remain unverified. A version number alone does not
establish compatibility: the app checks the required portal capabilities at startup.

1. Start the app from the unlocked graphical session. Allow the shortcut dialog and
   choose the keys you want; then allow keyboard/clipboard access. The portal's
   **RemoteDesktop** name refers here to local keyboard simulation and clipboard
   access, not a network connection or screen sharing. The app requests keyboard
   access, not pointer control or screen capture.
2. In Settings or Quick start, check the **actual shortcut** chosen by the desktop.
   `config.json` keeps your requested keys; they may differ from the active binding.
3. Select text, press the actual shortcut and release its modifiers. Stay in the same
   application while the selection is being copied. Wayland uses simulated Ctrl+C;
   it cannot identify the focused window or reliably query physical modifier state.
   Long key holds, focus changes and applications that intercept Ctrl+C can prevent
   capture or copy different text. If that happens, copy the desired text yourself
   and use a profile with `input: "clipboard"`, which sends no Ctrl+C.
4. When the translation is ready, paste it yourself with Ctrl+V. **Automatic paste
   stays disabled on Wayland**, including when `auto_paste` is saved as `true`.
   The saved preference is retained for other desktop sessions. Clipboard-input
   profiles still need the keyboard/clipboard consent because clipboard access is
   provided through the RemoteDesktop session.

The app compares clipboard text before claiming it, to preserve newer copies, but
Wayland compare/write is best effort, as on X11. It is not an atomic operation.
Keep the app running until you paste; clipboard contents may disappear on exit unless
a clipboard manager retains them.

If permission is refused, revoked or interrupted, the app stays open and shows the
reason in its control window and Settings. Use **Open menu > Retry Wayland permissions**
in the control window (or the same item in the tray menu) when enabled. If GNOME is
locked, unlock it before retrying. Missing-portal errors require a desktop with the
listed capabilities or an Xorg session; repeated consent attempts cannot add those
capabilities. A restart can reuse saved consent when the desktop permits it; a new
dialog may still be required after revocation or a failed restore.

The application entry is written to
`~/.local/share/applications/io.github.ypsilonmeister.ClipboardTranslator.desktop`
(under `XDG_DATA_HOME` when set). A private `wayland/restore-token` beside the
Linux app's `config.json` stores reusable input permission, separately from shared
configuration. Its directory is mode 0700 and its file mode 0600. Do not share this
folder with bug reports; the token and clipboard text are not logged. Desktop shortcut
bindings are stored by the compositor. Moving the executable updates its application
entry on the next launch; update Start at login separately as described below.

For acceptance testing and the distinction between verified portal operations and the
remaining packaged-app checks, see the [Wayland manual test sheet](https://github.com/ypsilonmeister/clipboard-translator/blob/main/docs/wayland-manual-test.md).

### Desktop controls, startup and builds

GTK 3 supplies the tray icon/menu. If the desktop has no compatible tray host, a small
control window keeps the menu and Quit available. Optional notifications use `notify-send`.
Keep the fallback control window open; closing it quits the app.
**Start at login** writes `~/.config/autostart/clipboard-translator.desktop` on X11,
or `~/.config/autostart/io.github.ypsilonmeister.ClipboardTranslator.desktop` on Wayland
(under `XDG_CONFIG_HOME` when set). It recognizes either owned entry across session
changes and removes only entries matching this executable when disabled.
Move the app before enabling it,
or toggle the setting after moving it. Quit from the menu or send SIGTERM; a `STOP`
file beside the executable also stops the app. Updating a Linux build currently means
quitting and replacing the executable/setup script; retain `config.json`, `models`, and
`runtime`. The Windows PowerShell installer and Scoop are Windows-only.

To build from source on Ubuntu 24.04 (Go 1.21+ and a C compiler required):

```bash
sudo apt-get install build-essential pkg-config libgtk-3-dev libx11-dev libxtst-dev
bash tools/package-linux.sh dev
# Extract dist/clipboard-translator-linux-amd64-dev.tar.gz to a writable directory.
```

`CGO_ENABLED=0` builds keep the logic testable but cannot run the Linux desktop app.
CI tests native input, PRIMARY/CLIPBOARD, paste, and menu commands in an isolated Xvfb
display. CI also runs fake-portal tests and compiles the opt-in Wayland fixtures;
it does not grant desktop consent or test a live Wayland session. Translation quality,
the full packaged-app Wayland workflow, actual desktop tray integration and GPU drivers
still need manual testing.

## ChromeOS (experimental, Crostini)

Install and set up the Linux build in the ChromeOS Linux environment first. Translation stays in the container; the browser extension supplies a host-side trigger.

1. Open the app's **Settings...**, enable **ChromeOS / local translation endpoint**, and use **Copy token**. The endpoint is off by default. Its per-install token is generated locally and saved as `translate_api_token` in `config.json`; do not share it. The switch (`translate_api_enabled`) takes effect immediately and survives restarts.
2. In ChromeOS **Settings → Developers → Linux development environment → Port forwarding**, add TCP port **18121** and turn forwarding on. Use `http://localhost:18121/translate` in the extension; `http://penguin.linux.test:18121/translate` is another permitted local address.
3. Copy this repository's `extension/` directory into a location Chrome can select (for example, My files). Open `chrome://extensions`, enable **Developer mode**, choose **Load unpacked**, and select that directory. There is no build step.
4. In Linux manual mode (desktop portals missing), the app menu also offers **Chrome extension endpoint** and **Copy connection info**. Paste that JSON into **Paste connection info** in extension **Options** and click **Apply connection info**, then **Test connection**. Manual endpoint/token entry with **Save** remains available. The test translates “Hello” locally without changing the clipboard.
5. Select English or Japanese text in Chrome, right-click, and choose **Translate with Clipboard Translator**. A notification confirms the result with a short preview. Paste with **Ctrl+V**. English translates to Japanese and Japanese to English with the default profile.

The extension uses the first configured profile, including direction, newline cleanup and replace/bilingual output. It never monitors the clipboard or sends text anywhere except the configured local endpoint. Options are stored locally in Chrome, without sync. The endpoint uses the same local model, reload and chunking path as the hotkey; it returns busy while another translation runs. It does not write the Linux clipboard; the extension writes the host clipboard after success.

When required Wayland portals are missing, the Linux app keeps a control window open in **manual mode** and explains that the hotkey workflow is unavailable. Copy text explicitly, focus the app's control window, choose **Open menu → Translate clipboard**, wait for completion, then paste. This action translates the current CLIPBOARD with the first profile, protects newer clipboard copies, and never automatically pastes. Keep the control window focused until completion so Wayland permits clipboard access. Portals that are available continue to use the usual workflow.

**Limitation:** ChromeOS deliberately isolates input between host applications and the Linux container. A Linux hotkey cannot trigger translation while a ChromeOS host app is focused. This extension handles selected text in Chrome; host-native applications require the manual copy → translate → paste workflow. Loading the extension and both language directions on a real Chromebook still need in-Chrome acceptance testing.

The optional endpoint listens on **`0.0.0.0:18121` (IPv4)** so Crostini forwarding can reach it; the inference engine and Settings page remain loopback-only. Enabling it also makes the port reachable on other container IPv4 interfaces, subject to host networking/firewall rules. Every request requires `X-Clipboard-Translator-Token`; keep forwarding limited to your machine. No CORS access is granted to web pages. Disable the switch to close the listener. To rotate a disclosed token, quit the app, remove `translate_api_token` from `config.json`, restart, and update extension options.

For other local clients, POST `/translate` with JSON `{"text":"Hello","profile":"0"}` and the token header. `profile` is optional (defaults to the first profile); its ID is the configured hotkey, such as `Ctrl+Alt+E`, or a zero-based index string. Success returns `{"translation":"…"}`; missing or wrong tokens return HTTP 401. The API exposes no model downloads, settings, or external translation proxy.

See [extension details](extension/README.md) for permissions and browser requirements.

## Using it

The app lives in the **system tray** (notification area; on Windows 11 it may be
under the `^` arrow). The first time the model has loaded, a balloon says *Ready* and
gives a three-step quick start with your actual hotkey (select text, press the hotkey,
paste with `Ctrl+V`, or `Cmd+V` on macOS). It is shown once; later launches stay quiet.
Read it again from **Quick start** in the tray menu or the card at the top of the Settings
page. If the hotkey could not be registered, the balloon sends you to Settings to choose
another one instead. The guide changes no setting (auto-paste stays off). The tooltip shows
the state and, while loading or translating, the elapsed seconds.

Select text in your language pair (Japanese or English unless you changed it, see
`languages` below) and press `Ctrl+Alt+E`. When the translation is
ready it replaces the clipboard content; press `Ctrl+V` to paste. With no
selection, the current clipboard text is translated (on Windows after up to 2 s, the
time the app gives a slow page to finish its copy; a profile with `"input": "clipboard"`
skips that wait).

Right-click the tray icon:

| Item | What it does |
| --- | --- |
| **Settings...** | Opens a settings page in your browser: add, change and remove hotkeys (keys, a language pair or one target language each), auto-paste, completion notifications, the longest text to translate, idle model unloading, the translation model (download a size, switch between installed ones) and the engine. Saved to `config.json`; see *Settings page*. |
| **Quick start** | Shows the select, hotkey, paste steps again with your current hotkey. |
| **Cancel translation** | Shown while a translation runs. |
| **Unload model** | Stops the server and unloads the model when no translation or server switch is in progress. |
| **Translation model** | Shown when two or more models are installed to quickly switch between a fast and an accurate model. |
| **Recent translations** | The last 10, kept in memory only (never written to disk). Click an entry to copy its translation. Under *Copy original or both*, choose an entry and copy its original or original + translation. Completed translations are also kept here when a newer clipboard copy prevents automatic replacement; *Clear history* empties the list. |
| **Check for updates** | Asks GitHub for the latest release when you click. The app never checks by itself. |
| **Open config.json** | For everything else, see [Configuration](#configuration). |
| **Built with PLaMo (license)** | Opens the PLaMo Community License. |
| **Start with Windows** | Toggle. |
| **Open log** / **Quit** | |

### What is in the tray / what is in Settings

| Place | Actions and settings |
| --- | --- |
| Tray | Settings, cancel translation, unload model, translation model (when two or more are installed, to quickly switch between a fast and an accurate model), recent translations, updates, open config.json, start at login, license, quit (plus logs and version information) |
| Settings | Hotkeys, languages, and output format (replace / bilingual) and text source (selection / clipboard) per profile; auto-paste, notifications, model and engine selection, text limits, idle unload |

Auto-paste is off by default. On supported desktops, when enabled, it pastes only for a fresh selection while the same window is still in front; otherwise the translation waits in the clipboard. It is always disabled on Wayland.
If hotkey registration fails, open **Settings...**, the first tray entry, choose another hotkey and click **Save**.
For advanced keys, use **Open config.json** and restart after editing.

If the translation server (`llama-server`) stops unexpectedly the app restarts it
(1 s, 2 s, 4 s ... apart) and says so; after 3 restarts within 10 minutes it gives
up and quits with a message. Startup errors are shown in a message box and
written to `translator.log`.

## Privacy and network use

- Translation happens on your PC. The inference server listens on `127.0.0.1` only, so
  other computers cannot reach it. Programs on your PC, including web pages in
  your browser, can reach `127.0.0.1`, so the app also gives the server a random
  key each time it starts it, and the server refuses translation and other API
  requests without that key (its `/health` status check does not need it). The app also asks
  llama-server to turn off its built-in Web UI (`LLAMA_ARG_UI=0`, read by
  current llama.cpp builds).
- The app contacts the network only when you ask: the model and llama.cpp
  download (`Setup.cmd`, `setup.ps1`, the first-run offer, the install script;
  both model and llama.cpp versions are pinned and verified against hard-coded
  SHA-256 hashes) and *Check for updates* / *Update*. There is no telemetry and no
  background check.
- *Settings...* opens a page served by the app itself on `127.0.0.1` (a free port,
  only while the page is open; it closes a few minutes after the last request or
  with *Close this page*). It is protected by a random token in its address and
  loads nothing from the internet.
- The Settings page can download a model. That is the only thing it fetches, from
  `huggingface.co`, only when you click *Download*, only the pinned revision of
  that model, and the file is used only if its SHA-256 matches the pinned value.
  A download goes on after the page is closed and can be cancelled on the page;
  a cancelled or failed download keeps its partial file and continues next time.
- The optional ChromeOS translation endpoint is off by default and accepts token-authenticated local client requests on IPv4 port 18121 when enabled; see the ChromeOS section for its bind address and forwarding scope. It makes no outbound network calls.
- Logs record errors but not the text you translate. Translation history lives in
  memory only and is gone when the app quits.

## Configuration

Use Settings for everyday configuration. For advanced keys, edit `config.json`
(all paths are relative to the app folder) and restart the app. The previous
file is kept as `config.json.bak`.

| Key | Meaning |
| --- | --- |
| `hotkey` | Modifier(s) + one key: a letter A-Z, a digit 0-9, or a function key F1-F12, e.g. `Ctrl+Alt+E` or `Ctrl+F5` (on macOS `Cmd` / `Command` and `Option` also work) |
| `auto_paste` | `true` types Ctrl+V after translating on supported desktops (default `false`; always disabled on Wayland) |
| `quickstart_shown` | `true` once the first-run quick start has been shown (set by the app; set it to `false` to see the balloon again; default `false`) |
| `notify_on_complete` | `true` shows a tray notification that the translation is ready in the clipboard (without showing text) when not auto-pasted (default `false`) |
| `languages` | Two language codes translated in both directions, e.g. `["ja", "en"]` (default), `["en", "ko"]`, `["ja", "zh-hans"]`. Codes are the model's: `ja en ko zh-hans zh-hant ru th ar de fr es ...` (53 in all, see the [model card](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF)). The direction is read from the script of the text, so the two languages must use different scripts (en ↔ de is refused for now). Japanese ↔ English is the main use; other languages are best effort. |
| `profiles` | Several hotkeys, each with its own languages, e.g. `[{"hotkey": "Ctrl+Alt+E", "languages": ["ja", "en"]}, {"hotkey": "Ctrl+Alt+K", "target": "ko", "output": "bilingual"}]`. A profile has either `languages` (a pair, both ways) or `target` (always translate into that language; the model detects the source, so any language works, including pairs that share a script such as `en` and `de`: use one `target` profile for each), and an optional `output` format: `"replace"` (default; key absent = replace) puts only the translation on the clipboard; `"bilingual"` puts the original text, a newline, and then the translation (`<source>\n<translated>`, with trailing CR/LF characters trimmed from the source before adding the newline). Translation history keeps Source and Translated separately; "Recent translations" lets you copy the original, the translation, or both. An optional `input` says where the text comes from: `"selection"` (default) copies the current selection first (Ctrl+C, waiting up to 2 s on Windows for a slow page); `"clipboard"` translates what is already on the clipboard, sends no Ctrl+C, skips the selection-copy wait and never auto-pastes (Wayland clipboard transfer is still asynchronous), which suits a page that copies slowly (copy yourself, then press the hotkey). Takes the place of `hotkey` and `languages`; Settings edits each profile. Up to 8 hotkeys on Windows and Linux; on macOS only the first is registered for now, and the app says which ones are not active. A hotkey another program owns only disables its own profile. |
| `port` | Local llama-server port, 1024-65534 (the app also uses `port + 1` as an instance lock) |
| `max_chars` | Longer text is skipped |
| `idle_unload_minutes` | Unload the model after this many idle minutes (0..1440; default `0` = never). Also editable on the Settings page. |
| `gpu_layers` | Layers offloaded to GPU (`0` = CPU only) |
| `model_file` | The model, e.g. `models/plamo-3-translate-8b-Q4_K_M.gguf`; `setup.ps1 -Model` sets it (see *Choosing the model size*) |
| `server_file` / `fallback_server_file` | llama-server binaries (primary / CPU fallback) |

> `setup.ps1` sets `server_file`, `fallback_server_file` and `gpu_layers` to match
> the detected backend on a fresh install, when you pass `-Backend cuda|vulkan|cpu`
> explicitly, or when it newly installs a GPU build. A plain re-run or an update
> (`-Backend auto`, as Scoop does) keeps the engine and `gpu_layers` you already
> use. It only downloads a backend that is missing or incomplete, and verifies
> every download against a SHA-256 (the model files are pinned to one revision).

## Settings page

Set **Unload model after idle** (`idle_unload_minutes`) and click *Save* to enable automatic unloading. Manual or automatic unloading frees RAM/VRAM. The next hotkey press reloads the model and translates, taking longer while the model loads.

*Settings...* in the tray menu opens it in your browser (see *Privacy and network
use* for how it is served). It edits:

- the **hotkeys**, each with its keys, either a language pair or one target
  language, the **text source** (selection or clipboard; `input`), and the **output format** ("Replace with the translation" / "Keep the original, then the translation") (as `profiles` in `config.json`), **auto-paste**, **notification on complete** (`notify_on_complete`), and the **longest
  text** (`max_chars`). These are applied with *Save*.
- the **translation model**: *Download* fetches a size you do not have yet (with
  progress and *Cancel*; the 31B model is not offered), *Use this model* switches
  to one that is installed. Switching restarts `llama-server`, which takes a
  moment; if the new model cannot be loaded (for example for lack of memory) the
  app tries to restore the previous one and the page reports the result. If restoring
  it also fails, the app reports that the translation server stopped and is restarting.
  *Delete* removes an installed model
  you are no longer using or an incomplete file (with inline confirmation), and
  *Remove partial download* cleans up an interrupted download to free disk space
  (available for installed and active models too). The active model's file and the
  chat template files are never deleted.
- the **engine** (CPU, CUDA, Vulkan, or Metal on macOS), when more than one is installed.

Everything else stays in `config.json`.

## Limitations

- Some apps block the synthetic Ctrl+C used to read the selection.
- Reading the selection changes the clipboard. The translation only replaces it
  if the clipboard text is still the text that was captured.
- One language pair per hotkey; the two languages must use different scripts (en ↔ de, or Simplified ↔ Traditional Chinese, cannot be told apart from the text; give each its own `target` profile instead). With Japanese and Chinese in one pair, text with only Han characters is read as Chinese; kana makes it Japanese. URLs are not translated. When text cannot be translated (empty selection, URL-only, wrong language pair), the tray tooltip shows why; text over the character limit also shows a notification.
- A GPU backend can fail (too little VRAM, old driver, missing DLLs); the app
  then falls back to the CPU build. See `server.log` and `translator.log`.
- The exe is not code-signed, so a zip downloaded in a browser may trigger a
  SmartScreen warning (files fetched by the install script do not).

## Build (Windows)

Requires [Go](https://go.dev/) 1.21 or later.

```powershell
go test ./...

# Release build: no console window, tray icon only
go build -ldflags "-H=windowsgui -s -w" -o ClipboardTranslator.exe .

# Debug build: keeps the console and prints the log to it
go build -o ClipboardTranslator.exe .
```

The icon, the version info shown in Explorer and the application manifest
(per-monitor DPI awareness, so the tray icon and menu stay sharp on scaled
displays) are linked in from `resource_windows_amd64.syso`, which the Go
toolchain picks up automatically. It is generated with the standard library only:

```powershell
go run ./tools/mkicon                    # assets/icon-source.jpg -> assets/icon.ico, assets/icon.png
go run ./tools/mksyso -version 0.5.1     # icon + assets/app.manifest + version -> resource_windows_amd64.syso
```

The committed `.syso` carries the version of the last release; the release
workflow regenerates it from the tag and also stamps `main.version`, which *Check
for updates* compares with the latest release (a local build says "development
build" and never claims to be outdated). `setup.ps1` is embedded in the exe.

## Continuous integration and releases

- `.github/workflows/ci.yml` runs `gofmt`, `go vet`, `go test` and a release-flag
  build on Windows for every push to `main` and every pull request, and checks
  that the PowerShell scripts parse. Linux builds/tests include native X11 desktop
  checks under Xvfb, setup fixture tests, and tarball packaging. macOS runs native
  tests/smoke checks and app packaging on Apple Silicon and Intel runners.
- `.github/workflows/llama-smoke.yml` runs a weekly and PR/dispatch smoke test
  that verifies real `llama-server` startup and translation against the pinned
  Linux CPU build and the 0.8B model.
- Pushing a tag such as `v0.6.0` runs `.github/workflows/release.yml`: it builds
  the exe with the tag as its version, packages
  `Portable-Clipboard-Translator-Windows-x64-<tag>.zip` and attaches it to the
  GitHub release. It also attaches a Linux amd64 tarball and SHA-256 file, built
  on Ubuntu 24.04, plus ad-hoc signed macOS Apple Silicon/Intel app zips/checksums.
  An existing release receives the build artifacts.
- `bucket/clipboard-translator.json` is the Scoop manifest; bump it with each
  release (`scoop checkver -u` can do it).

Bug reports and feature requests: use the issue forms. They ask for the app
version, how it was installed and the log excerpt.

## Licenses

This repository is licensed under the [MIT License](LICENSE).

The model and llama.cpp are **not** included; `setup.ps1` / `setup.sh` download them from
their official sources and they have their own licenses.

**PLaMo.** The model is used under the
[PLaMo Community License](https://plamo.preferredai.jp/info/plamo-community-license-ja)
of Preferred Networks, Inc. (**Built with PLaMo**). In short, as read for this
README (the publisher can change the text): using the model for business or
profit, which includes use inside an organization, is allowed free of charge if
you register beforehand on the publisher's page and your (or your affiliates')
revenue for the last fiscal year does not exceed 1 billion yen. Whoever provides
the model or its output to third parties must show "Built with PLaMo" and supply
a copy of the license, and text you publish that the model produced must say so.
Read the license itself; this summary is not legal advice. The license says
nothing about donations, so whether accepting any counts as commercial use is a
question for the publisher.

[llama.cpp](https://github.com/ggml-org/llama.cpp) is MIT licensed.
