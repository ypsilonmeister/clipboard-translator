#!/usr/bin/env bash
# Linux setup: all downloads are explicit; normal translation remains offline.
# The work is done by the binary's setup subcommand (Issue #152); this
# script only validates the arguments and finds the binary, so the
# release tarball keeps a runnable entry point and the app can still run
# the copy it carries inside the exe.
set -euo pipefail
backend=cpu
model=''
explicit_backend=false
usage() { echo 'Usage: bash setup.sh [--backend cpu|vulkan] [--model 0.8b|2b|8b]' >&2; exit 2; }
while [[ $# -gt 0 ]]; do
    case $1 in
        --backend) [[ $# -ge 2 && $2 =~ ^(cpu|vulkan)$ ]] || usage; backend=$2; explicit_backend=true; shift 2 ;;
        --model) [[ $# -ge 2 && $2 =~ ^(0\.8b|2b|8b)$ ]] || usage; model=$2; shift 2 ;;
        *) usage ;;
    esac
done
if [[ $(uname -s) != Linux ]]; then echo 'This setup script supports Linux only.' >&2; exit 1; fi
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# The pinned llama.cpp release the binary installs. Kept here as the
# record tools/bump_llama_pin.py manages; internal/setup/llama.go holds
# the same values and the tests keep them in agreement.
# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
# LLAMA_TAG = 'b12359'
# LLAMA_ASSETS = {
#     'cpu': {
#         'file': 'llama-b12359-bin-ubuntu-x64.tar.gz',
#         'sha256': '0bc1f091e8916102b6d8f2cf944233a620e90a0f856c72d0ce41150c65c2e72e',
#     },
#     'vulkan': {
#         'file': 'llama-b12359-bin-ubuntu-vulkan-x64.tar.gz',
#         'sha256': '19b60f00395a3f4954e46a5037695bc1d843404fc9d3124f82a612ecd8fbe9f0',
#     },
# }
# --- end llama.cpp pinned release ---

bin=''
if [[ -x $root/clipboard-translator ]]; then
    bin=$root/clipboard-translator
elif [[ -r /proc/$PPID/exe ]] && [[ $(basename -- "$(readlink -- /proc/$PPID/exe)") == 'clipboard-translator' ]]; then
    # The app runs this script (the copy embedded in the exe) from the app
    # folder; the process that started us is the binary itself.
    bin=$(readlink -- /proc/$PPID/exe)
else
    echo 'Could not find the clipboard-translator binary next to this script.' >&2
    echo 'Run setup.sh from the folder the release tarball was extracted to.' >&2
    exit 1
fi
args=(setup --root "$root")
if [[ $explicit_backend == true ]]; then args+=(--backend "$backend"); fi
if [[ -n $model ]]; then args+=(--model "$model"); fi
exec "$bin" "${args[@]}"
