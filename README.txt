Clipboard Translator (Windows x64, portable zip)
================================================
Japanese <-> English: select text, press Ctrl+Alt+E, paste with Ctrl+V.
The model runs locally with llama.cpp; no Ollama or Python required.
Built with PLaMo (Preferred Networks) - see LICENSES below.

Project page, newer versions, install script and Scoop:
  https://github.com/ypsilonmeister/clipboard-translator

INSTALL
1. Extract this ZIP to a writable folder.
2. Run Setup.cmd (internet required only for the initial downloads: the PLaMo
   model, over 1 GB, and llama.cpp). If you skip this step, starting
   ClipboardTranslator.exe offers to do it for you.
   If you previously downloaded PLaMo, you may copy the GGUF and
   chat_template.jinja from your OLD models folder into the NEW models folder.
3. Run Start.cmd (or ClipboardTranslator.exe). The app runs in the system tray
   (no console window). Wait for the 'Ready' balloon; hover over the tray icon
   to see the current state. On Windows 11 the icon may be hidden under the ^
   overflow arrow.
4. Highlight Japanese or English text and press Ctrl+Alt+E. Ctrl+V pastes the
   translated text when ready. No text selected = existing clipboard content.
5. Stop.cmd stops the app and llama-server. If you move the folder, toggle
   'Start with Windows' off and on again.

TRAY MENU (right-click the icon)
  Cancel translation          shown while a translation runs
  Recent translations         last 10, kept in memory only; click to copy again
  Paste automatically ...     off by default; types Ctrl+V into the window you
                              selected the text in (only a fresh selection, and
                              only if that window is still in front)
  Hotkey                      pick another key combination
  Translation engine          CPU / CUDA / Vulkan (when more than one is installed)
  Check for updates           asks GitHub only when you click
  Open config.json            other settings; changes there need a restart
  Built with PLaMo (license)  opens the PLaMo Community License
  Start with Windows, Open log, Quit
If llama-server stops unexpectedly the app restarts it; after 3 restarts within
10 minutes it quits with a message.

AUTO BACKENDS
  NVIDIA GPU       -> CUDA 12.4
  AMD/Intel GPU    -> Vulkan
  Other or no GPU  -> CPU
The script downloads the latest RECENT llama.cpp release that contains the
necessary Windows binary ZIPs. It avoids the /releases/latest tag, which may
contain no executables. CPU backup is always included; if GPU load fails,
ClipboardTranslator tries the CPU backend automatically.

For manual choice, run in PowerShell from this folder:
  powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Backend vulkan
  powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Backend cuda
  powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Backend cpu

All model/executable paths are relative. Once set up, copy the whole folder
to a Windows x64 PC. Drivers must support the selected GPU backend; otherwise
the app falls back to the CPU build.

PRIVACY
- Translation runs on this PC; the server listens on 127.0.0.1 only.
- The network is used only when you ask: the downloads above, and 'Check for
  updates' / 'Update'. No telemetry, no background checks.
- The logs in this folder record errors, not the text you translate.

LIMITATIONS
- Prototype; not tested on a wide range of PCs or GPUs.
- Some apps do not allow synthetic Ctrl+C of selected text.
- Reading the selection changes the clipboard. The translated text replaces it
  only if the current clipboard text still matches the captured text.
  If another application updates the clipboard metadata without changing text,
  the translation is still accepted. If the actual text changes, it is preserved.
- Only Japanese <-> English. URLs are not translated.
- The exe is not code-signed; Windows SmartScreen may warn about a zip
  downloaded in a browser.
- The backend may fail because of insufficient VRAM, old GPU drivers, or DLL
  incompatibilities. See server.log and translator.log for details.

LICENSES
- This program: MIT License (see LICENSE).
- Built with PLaMo. The model is used under the PLaMo Community License of
  Preferred Networks, Inc.:
    https://plamo.preferredai.jp/info/plamo-community-license-ja
  Using it for business or profit (including inside an organization) requires
  registering beforehand with the publisher and revenue of at most 1 billion
  yen for the last fiscal year; read the license itself. Model page:
    https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF
- llama.cpp (MIT): https://github.com/ggml-org/llama.cpp/releases
