#!/usr/bin/env bash
# Invoked from the writable app-data folder, never from inside the app bundle.
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
command -v python3 >/dev/null || { echo 'Install Python 3.12+ to run setup (for example, brew install python).' >&2; exit 1; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python3 - "$root" "$backend" "$model" "$explicit_backend" <<'PY'
import hashlib
import json
from pathlib import Path, PureWindowsPath
import platform
import re
import shutil
import sys
import tarfile
import tempfile
import urllib.request

root, backend = Path(sys.argv[1]), sys.argv[2]
model_arg = sys.argv[3] if len(sys.argv) > 3 else ''
explicit_backend = len(sys.argv) > 4 and sys.argv[4] == 'true'
if not hasattr(tarfile, 'data_filter'):
    sys.exit('Setup needs Python with tarfile.data_filter (Python 3.12+, or a security-patched Python 3.10/3.11).')
arch = {'x86_64': 'x64', 'arm64': 'arm64', 'aarch64': 'arm64'}.get(platform.machine())
if not arch:
    sys.exit('Supported macOS architectures: Apple Silicon and Intel x86_64.')

# The models setup can install. Each file is pinned to one Hugging Face commit and one SHA-256
# (the model and the chat template), as in setup.ps1; keep the values the same.
# `minimum` only guards files already on disk (they may come from an older revision).
MODELS = {
    '0.8b': dict(repo='plamo-3-translate-0.8b-GGUF', revision='0c4af2b003fdd94b6868aa955414c1c86ee96997',
                 file='plamo-3-translate-0.8b-Q4_K_M.gguf', sha256='73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079',
                 template_sha256='084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e', minimum=400 * 1024**2),
    '2b': dict(repo='plamo-3-translate-2b-GGUF', revision='232fbd7916519edecddfdae0beae7e76fe70453f',
               file='plamo-3-translate-2b-Q4_K_M.gguf', sha256='f36d0813dd95573a852ab5d76e374c543632d9e292929e922bcdbf7e53b494e0',
               template_sha256='18dcc169548d794897084a04cd150a5a633717f1eb6e62de4a0e0973b377e02a', minimum=1024**3),
    '8b': dict(repo='plamo-3-translate-8b-GGUF', revision='407c15bc44f6968e6734bc047d92f766c163bbec',
               file='plamo-3-translate-8b-Q4_K_M.gguf', sha256='e021fb83dcda393a4f866291caa53380eebbe2f25be4c7585fb19e8486828bbf',
               template_sha256='084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e', minimum=4 * 1024**3),
}

def fetch(url):
    return urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': 'ClipboardTranslatorSetup', 'Accept': 'application/vnd.github+json'}), timeout=60)

def download(url, dest, expected=None):
    partial = dest.with_name(dest.name + '.partial')
    try:
        digest = hashlib.sha256()
        with fetch(url) as response, partial.open('wb') as out:
            while chunk := response.read(1024 * 1024):
                out.write(chunk)
                digest.update(chunk)
        if not partial.stat().st_size:
            raise RuntimeError('Empty download: ' + url)
        if expected and digest.hexdigest().lower() != expected.lower():
            raise RuntimeError('SHA-256 mismatch: ' + dest.name)
        partial.replace(dest)
    finally:
        partial.unlink(missing_ok=True)

(root / 'models').mkdir(exist_ok=True)
(root / 'runtime').mkdir(exist_ok=True)
config_path = root / 'config.json'
# Read config.json before the big download: a damaged file should fail now.
config = json.loads(config_path.read_text(encoding='utf-8')) if config_path.exists() else {}
def configured_server(key):
    value = config.get(key)
    if not isinstance(value, str) or not value.strip():
        return False
    value = value.replace(chr(92), '/')
    path = Path(value)
    if path.is_absolute() or PureWindowsPath(value).drive or '..' in path.parts:
        return False
    return (root / path).is_file()

# Capture this before installing: a fresh install must replace the shipped defaults.
server_present_at_start = configured_server('server_file')
backend_complete_at_start = (root / 'runtime' / backend / 'llama-server').is_file()

# Without --model, keep the model config.json names (so an update never swaps a chosen model); else 2b.
configured = Path(str(config.get('model_file') or '').replace(chr(92), '/')).name.lower()
model_key = model_arg or next((k for k, m in MODELS.items() if m['file'].lower() == configured), '2b')
model = MODELS[model_key]
print(f'Model: PLaMo 3 Translate {model_key.upper()}', flush=True)
hf = 'https://huggingface.co/pfnet/' + model['repo'] + '/resolve/' + model['revision'] + '/'
for name, minimum, sha256 in [(model['file'], model['minimum'], model['sha256']), ('chat_template.jinja', 100, model['template_sha256'])]:
    dest = root / 'models' / name
    if not dest.exists():
        print('Downloading ' + name, flush=True)
        download(hf + name, dest, sha256)
    if dest.stat().st_size < minimum:
        raise RuntimeError('Incomplete file; remove it and retry: ' + str(dest))

# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
LLAMA_TAG = 'b12032'
LLAMA_ASSETS = {
    'arm64': {
        'file': 'llama-b12032-bin-macos-arm64.tar.gz',
        'sha256': '00468fe833eb000b78a748122f5921b6c7b07dc82331a8d0c68196c69cb26382',
    },
    'x64': {
        'file': 'llama-b12032-bin-macos-x64.tar.gz',
        'sha256': '291d99f3b6f45f67f73afe5381540f29928840a1a14d9c19d6595bd649a035fc',
    },
}
# --- end llama.cpp pinned release ---

target = root / 'runtime/cpu'
if not (target / 'llama-server').is_file():
    info = LLAMA_ASSETS.get(arch)
    if not info:
        raise RuntimeError('No matching official llama.cpp macOS runtime found.')
    file_name = info['file']
    sha256 = info.get('sha256') or ''
    if not re.fullmatch(r'[0-9a-fA-F]{64}', sha256):
        raise RuntimeError('Missing SHA-256 for ' + file_name)
    url = 'https://github.com/ggml-org/llama.cpp/releases/download/' + LLAMA_TAG + '/' + file_name
    with tempfile.TemporaryDirectory(prefix='.setup-', dir=root / 'runtime') as tmp:
        tmp = Path(tmp)
        archive = tmp / 'llama.tar.gz'
        download(url, archive, sha256)
        unpacked = tmp / 'unpacked'
        unpacked.mkdir()
        with tarfile.open(archive, 'r:gz') as tar:
            tar.extractall(unpacked, filter='data')
        servers = list(unpacked.rglob('llama-server'))
        if len(servers) != 1 or not servers[0].is_file():
            raise RuntimeError('Expected one llama-server executable in ' + file_name)
        server = servers[0]
        server.chmod(server.stat().st_mode | 0o111)
        staged = tmp / 'backend'
        shutil.copytree(server.parent, staged, symlinks=True)
        if target.exists():
            raise RuntimeError('Incomplete runtime directory exists; move it aside and retry: ' + str(target))
        staged.rename(target)

# The official macOS binary supports both CPU (-ngl 0) and Metal. Share the
# verified installation instead of downloading two copies of the same archive.
metal = root / 'runtime/metal'
if not metal.exists() and not metal.is_symlink():
    metal.symlink_to('cpu', target_is_directory=True)
if not (metal / 'llama-server').is_file():
    raise RuntimeError('Metal runtime alias is incomplete; move runtime/metal aside and retry.')
if model_arg:
    config['model_file'] = 'models/' + model['file']
# Match Update-BackendConfig in setup.ps1: preserve a working user engine on re-runs.
if explicit_backend or not server_present_at_start or not configured_server('server_file') or not backend_complete_at_start:
    config.update(server_file=f'runtime/{backend}/llama-server', fallback_server_file='runtime/cpu/llama-server', gpu_layers=0 if backend == 'cpu' else 99)
else:
    if not configured_server('fallback_server_file'):
        config['fallback_server_file'] = 'runtime/cpu/llama-server'
    if config.get('gpu_layers') is None:
        is_cpu = re.match(r'^runtime[\\/]cpu[\\/]', config['server_file'], re.IGNORECASE)
        config['gpu_layers'] = 0 if is_cpu else 99
if config_path.exists():
    shutil.copyfile(config_path, root / 'config.json.bak')
config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('Setup complete. Restart Clipboard Translator.')
print('Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja')
PY
