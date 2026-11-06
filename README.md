# clipboard-translator

Japanese <-> English clipboard translator for Windows. Select text, press
**Ctrl+Alt+E**, then paste the translation with **Ctrl+V**.

Translation runs fully locally with [llama.cpp](https://github.com/ggml-org/llama.cpp)
and [PLaMo 3 Translate 2B (GGUF)](https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF).
No Ollama or Python is required.

> **Status: prototype.** It has not been tested on a wide range of PCs or GPU
> drivers. Use at your own risk.

## Quick start (Windows 10/11 x64)

1. Build or download `ClipboardTranslator.exe` and put it in a writable folder
   together with the other files from this repository (see [Build](#build)).
2. Run `Setup.cmd`. It downloads the PLaMo model and a llama.cpp build
   (CUDA / Vulkan / CPU is chosen automatically; CPU is always installed as a
   fallback). An internet connection is only needed for this step.
3. Run `Start.cmd`. The app lives in the **system tray** (notification area);
   a balloon says *Ready* once the model has loaded (this can take a while on
   the first start).
4. Select Japanese or English text and press `Ctrl+Alt+E`. When the translation
   is ready it replaces the clipboard content; press `Ctrl+V` to paste.
   With no selection, the current clipboard text is translated.
5. Right-click the tray icon for **Open log** and **Quit**. `Stop.cmd` also
   stops the app and its `llama-server` child process.

The tooltip of the tray icon shows the current state (loading, ready,
translating, translation ready). Startup errors are shown in a message box and
written to `translator.log`.

Manual backend choice:

```powershell
powershell -ExecutionPolicy Bypass -File .\setup.ps1 -Backend vulkan   # or cuda / cpu
```

## Build

Requires [Go](https://go.dev/) 1.21 or later.

```powershell
go test ./...

# Release build: no console window, tray icon only
go build -ldflags "-H=windowsgui -s -w" -o ClipboardTranslator.exe .

# Debug build: keeps the console and prints the log to it
go build -o ClipboardTranslator.exe .
```

## Configuration

`config.json` (all paths are relative to the app folder):

| Key | Meaning |
| --- | --- |
| `hotkey` | Modifier(s) + one letter A-Z, e.g. `Ctrl+Alt+E` |
| `port` | Local llama-server port (the app also uses `port + 1` as an instance lock) |
| `max_chars` | Longer text is skipped |
| `gpu_layers` | Layers offloaded to GPU (`0` = CPU only) |
| `server_file` / `fallback_server_file` | llama-server binaries (primary / CPU fallback) |

> `Setup.cmd` rewrites `server_file`, `fallback_server_file` and `gpu_layers`
> in `config.json` to match the detected backend, so the file shows up as
> modified in git after setup.

## Limitations

- Some apps block the synthetic Ctrl+C used to read the selection.
- Reading the selection changes the clipboard. The translation only replaces it
  if the clipboard text is still the text that was captured.
- Only the Japanese <-> English pair is supported. URLs are not translated.
- A GPU backend can fail (too little VRAM, old driver, missing DLLs); the app
  then falls back to the CPU build. See `server.log` and `translator.log`.
- Logs record errors but not the translated text.
- The tray icon is the default application icon for now.

## Licenses

This repository is licensed under the [MIT License](LICENSE).

The model and llama.cpp are **not** included; `Setup.cmd` downloads them from
their official sources and they have their own licenses. Please check the
PLaMo 3 Translate license terms before use:
<https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF>
