#!/usr/bin/env bash
# Linux setup: all downloads are explicit; normal translation remains offline.
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
command -v python3 >/dev/null || { echo 'Install Python 3.12+ to run setup.' >&2; exit 1; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python3 - "$root" "$backend" "$model" "$explicit_backend" <<'PY'
import hashlib
import json
import os
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
arch = {'x86_64': 'x64', 'aarch64': 'arm64'}.get(platform.machine())
if not arch:
    sys.exit('Supported Linux architectures: x86_64 and aarch64.')
if arch != 'x64':
    sys.exit('Pre-built Linux binaries are currently pinned for x86_64.')

# The models setup can install. Each file is pinned to one Hugging Face commit and one SHA-256
# (the model and the chat template), as in setup.ps1; keep the values the same.
# `minimum` only guards files already on disk (they may come from an older revision).
MODELS = {
    '0.8b': dict(repo='plamo-3-translate-0.8b-GGUF', revision='0c4af2b003fdd94b6868aa955414c1c86ee96997',
                 file='plamo-3-translate-0.8b-Q4_K_M.gguf', sha256='73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079',
                 template_sha256='084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e', minimum=400 * 1024**2),
    '2b': dict(repo='plamo-3-translate-2b-GGUF', revision='228a27cc0bb4f111219963848d50a3127c16ca73',
               file='plamo-3-translate-2b-Q4_K_M.gguf', sha256='1ffd9073afcc70e35ccc942428aa2b73d7aa2b9ea779bf504b775369e0276c13',
               template_sha256='ad7eb87b161c6792be070d777a9f9da50c9e57a77e1fa5cca3a24b91b6bdb9e9', minimum=1024**3),
    '8b': dict(repo='plamo-3-translate-8b-GGUF', revision='407c15bc44f6968e6734bc047d92f766c163bbec',
               file='plamo-3-translate-8b-Q4_K_M.gguf', sha256='e021fb83dcda393a4f866291caa53380eebbe2f25be4c7585fb19e8486828bbf',
               template_sha256='084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e', minimum=4 * 1024**3),
}

def fetch(url):
    request = urllib.request.Request(url, headers={'User-Agent': 'ClipboardTranslatorSetup', 'Accept': 'application/vnd.github+json'})
    return urllib.request.urlopen(request, timeout=60)

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

model_dest = root / 'models' / model['file']
if not model_dest.exists():
    print('Downloading ' + model['file'], flush=True)
    download(hf + model['file'], model_dest, model['sha256'])
if model_dest.stat().st_size < model['minimum']:
    raise RuntimeError('Incomplete file; remove it and retry: ' + str(model_dest))

template_dest = root / 'models' / f'chat_template.{model_key}.jinja'
legacy_template = root / 'models' / 'chat_template.jinja'

def file_sha256(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        while chunk := f.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest().lower()

need_template = True
if template_dest.is_file():
    if file_sha256(template_dest) == model['template_sha256'].lower():
        need_template = False
elif legacy_template.is_file():
    if file_sha256(legacy_template) == model['template_sha256'].lower():
        shutil.copyfile(legacy_template, template_dest)
        need_template = False

if need_template:
    print('Downloading chat_template.jinja', flush=True)
    download(hf + 'chat_template.jinja', template_dest, model['template_sha256'])

if template_dest.stat().st_size < 100:
    raise RuntimeError('Incomplete file; remove it and retry: ' + str(template_dest))

# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---
LLAMA_TAG = 'b12032'
LLAMA_ASSETS = {
    'cpu': {
        'file': 'llama-b12032-bin-ubuntu-x64.tar.gz',
        'sha256': '9f10432ca3f66ef003a7a540aaffb9da0acadaefd05404521b9ab7a2436af304',
    },
    'vulkan': {
        'file': 'llama-b12032-bin-ubuntu-vulkan-x64.tar.gz',
        'sha256': 'f75e7107f9450034c5467492c41e9ed91816e2d815fc4b802c6bd58de7d5b126',
    },
}
# --- end llama.cpp pinned release ---

needed = [b for b in dict.fromkeys(['cpu', backend]) if not (root / 'runtime' / b / 'llama-server').is_file()]
if needed:
    print('Using official llama.cpp release ' + LLAMA_TAG, flush=True)
    base_url = 'https://github.com/ggml-org/llama.cpp/releases/download/' + LLAMA_TAG + '/'
    for b in needed:
        info = LLAMA_ASSETS.get(b)
        if not info:
            raise RuntimeError('No pinned asset for backend: ' + b)
        file_name = info['file']
        sha256 = info.get('sha256') or ''
        if not re.fullmatch(r'[0-9a-fA-F]{64}', sha256):
            raise RuntimeError('Missing SHA-256 for ' + file_name)
        url = base_url + file_name
        # Stage all files before making the installation visible. A partial
        # download/extraction must never look like an installed backend.
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
            target = root / 'runtime' / b
            # Preserve the archive's layout and library symlinks next to the server.
            staged = tmp / 'backend'
            shutil.copytree(server.parent, staged, symlinks=True)
            if target.exists():
                raise RuntimeError('Incomplete runtime directory exists; move it aside and retry: ' + str(target))
            staged.rename(target)

def is_custom_template(path):
    if not path or not isinstance(path, str):
        return False
    p = path.replace('\\', '/')
    leaf = Path(p).name.lower()
    parent = Path(p).parent.as_posix()
    if parent not in ('', '.', 'models'):
        return True
    if leaf == 'chat_template.jinja':
        return False
    if re.fullmatch(r'chat_template\.(0\.8b|2b|8b)\.jinja', leaf):
        return False
    return True

if model_arg:
    config['model_file'] = 'models/' + model['file']
    cur_tpl = config.get('template_file')
    if not is_custom_template(cur_tpl):
        config['template_file'] = f'models/chat_template.{model_key}.jinja'
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
# Rewrite in place to retain hard links, consistent with the Windows settings writer.
config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('Setup complete. Start ./clipboard-translator in an X11 session.')
print('Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja')
PY
