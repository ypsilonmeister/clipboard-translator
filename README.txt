Portable Clipboard Translator v0.3.2 (Windows x64)
==============================================
Japanese <-> English: select text, press Ctrl+Alt+E, paste with Ctrl+V.
The model runs locally with llama.cpp; no Ollama or Python required.

INSTALL
1. Extract this ZIP to a writable folder.
2. If you previously downloaded PLaMo, you may copy the GGUF and
   chat_template.jinja from your OLD models folder into the NEW models folder.
3. Run Setup.cmd (internet required only for initial downloads).
4. Run Start.cmd. Wait for a 'ready' message in the program console.
5. Highlight Japanese or English text and press Ctrl+Alt+E. Ctrl+V pastes the
   translated text when ready. No text selected = existing clipboard content.
6. Run Stop.cmd to stop the app and llama-server.

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

LIMITATIONS
- Prototype; has not been tested on an actual Windows PC or with a live PLaMo
  inference. Network/API installation and GPU compatibility remain unverified.
- Some apps do not allow synthetic Ctrl+C of selected text.
- Reading selection changes the clipboard. The translated text replaces it
  only if the current clipboard text still matches the captured text.
  If another application updates the clipboard metadata without changing text,
  the translation is still accepted. If the actual text changes, it is preserved.
- This is not an automatic paste; you press Ctrl+V after translation.
- The console and logs in this folder show errors without recording text.
- The backend may fail because of insufficient VRAM, old GPU drivers, or DLL
  incompatibilities. See server.log and translator.log for details.
- Verify PLaMo 3 Translate license terms before use.
  https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF

Official llama.cpp: https://github.com/ggml-org/llama.cpp/releases
