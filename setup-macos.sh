#!/usr/bin/env bash
# Invoked from the writable app-data folder, never from inside the app bundle.
# The work is done by the binary's setup subcommand (Issue #152); this
# script only validates the arguments and finds the binary.
set -euo pipefail
backend=metal
model=''
explicit_backend=false
usage() { echo 'Usage: bash setup.sh [--backend cpu|metal] [--model 0.8b|2b|8b]' >&2; exit 2; }
while [[ $# -gt 0 ]]; do
    case $1 in
        --backend) [[ $# -ge 2 && $2 =~ ^(cpu|metal)$ ]] || usage; backend=$2; explicit_backend=true; shift 2 ;;
        --model) [[ $# -ge 2 && $2 =~ ^(0\.8b|2b|8b)$ ]] || usage; model=$2; shift 2 ;;
        *) usage ;;
    esac
done
if [[ $(uname -s) != Darwin ]]; then echo 'This script supports macOS only.' >&2; exit 1; fi
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# The pinned llama.cpp release the binary installs. Kept here as the
# record tools/bump_llama_pin.py manages; internal/setup/llama.go holds
# the same values and the tests keep them in agreement.
# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
# LLAMA_TAG = 'b12359'
# LLAMA_ASSETS = {
#     'arm64': {
#         'file': 'llama-b12359-bin-macos-arm64.tar.gz',
#         'sha256': '82b00abc740160e1b9980e6cc79c117345fa34af058c80e6c0a69aa8ddccde75',
#     },
#     'x64': {
#         'file': 'llama-b12359-bin-macos-x64.tar.gz',
#         'sha256': '25320d529077ef5a3d16e7b5da26438f221d88c8c0930a32b0c79457f5eede56',
#     },
# }
# --- end llama.cpp pinned release ---

bin=''
if [[ -n ${CLIPBOARD_TRANSLATOR_BIN:-} && -x ${CLIPBOARD_TRANSLATOR_BIN:-} ]]; then
    bin=$CLIPBOARD_TRANSLATOR_BIN
elif [[ -x $root/clipboard-translator ]]; then
    bin=$root/clipboard-translator
elif [[ -x "$root/Clipboard Translator.app/Contents/MacOS/clipboard-translator" ]]; then
    bin="$root/Clipboard Translator.app/Contents/MacOS/clipboard-translator"
else
    # The app runs this script (the copy embedded in the exe) from the
    # app-data folder; the process that started us is the binary itself.
    parent=$(ps -p "$PPID" -o comm= 2>/dev/null || true)
    if [[ $parent == */clipboard-translator ]]; then
        bin=$parent
    elif [[ -x '/Applications/Clipboard Translator.app/Contents/MacOS/clipboard-translator' ]]; then
        bin='/Applications/Clipboard Translator.app/Contents/MacOS/clipboard-translator'
    elif [[ -x "$HOME/Applications/Clipboard Translator.app/Contents/MacOS/clipboard-translator" ]]; then
        bin="$HOME/Applications/Clipboard Translator.app/Contents/MacOS/clipboard-translator"
    fi
fi
if [[ -z $bin ]]; then
    echo 'Could not find the Clipboard Translator app.' >&2
    echo 'Start the app and accept its offer to download the model, or run this script next to the app.' >&2
    exit 1
fi
args=(setup --root "$root")
if [[ $explicit_backend == true ]]; then args+=(--backend "$backend"); fi
if [[ -n $model ]]; then args+=(--model "$model"); fi
exec "$bin" "${args[@]}"
