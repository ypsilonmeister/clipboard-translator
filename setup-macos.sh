#!/usr/bin/env bash
# Invoked from the writable app-data folder, never from inside the app bundle.
set -euo pipefail
backend=metal
if [[ $# -gt 0 ]]; then
    if [[ $# != 2 || $1 != --backend || ! $2 =~ ^(cpu|metal)$ ]]; then
        echo 'Usage: bash setup.sh [--backend cpu|metal]' >&2; exit 2
    fi
    backend=$2
fi
if [[ $(uname -s) != Darwin ]]; then echo 'This script supports macOS only.' >&2; exit 1; fi
command -v python3 >/dev/null || { echo 'Install Python 3.12+ to run setup (for example, brew install python).' >&2; exit 1; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python3 - "$root" "$backend" <<'PY'
import hashlib
import json
from pathlib import Path
import platform
import re
import shutil
import sys
import tarfile
import tempfile
import urllib.request

root, backend = Path(sys.argv[1]), sys.argv[2]
if not hasattr(tarfile, 'data_filter'):
    sys.exit('Setup needs Python with tarfile.data_filter (Python 3.12+, or a security-patched Python 3.10/3.11).')
arch = {'x86_64': 'x64', 'arm64': 'arm64', 'aarch64': 'arm64'}.get(platform.machine())
if not arch:
    sys.exit('Supported macOS architectures: Apple Silicon and Intel x86_64.')

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
hf = 'https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/resolve/main/'
for name, minimum in [('plamo-3-translate-2b-Q4_K_M.gguf', 1024**3), ('chat_template.jinja', 100)]:
    dest = root / 'models' / name
    if not dest.exists():
        print('Downloading ' + name, flush=True)
        download(hf + name, dest)
    if dest.stat().st_size < minimum:
        raise RuntimeError('Incomplete file; remove it and retry: ' + str(dest))

target = root / 'runtime/cpu'
if not (target / 'llama-server').is_file():
    with fetch('https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=30') as response:
        releases = json.load(response)
    asset = next((a for r in releases if not r.get('draft') and not r.get('prerelease') for a in r.get('assets', []) if re.fullmatch(r'llama-.*-bin-macos-' + arch + r'\.tar\.gz', a['name'])), None)
    if not asset:
        raise RuntimeError('No matching official llama.cpp macOS runtime found.')
    digest = asset.get('digest') or ''
    if not re.fullmatch(r'sha256:[0-9a-fA-F]{64}', digest):
        raise RuntimeError('Missing SHA-256 for ' + asset['name'])
    url = asset['browser_download_url']
    if not url.startswith('https://github.com/ggml-org/llama.cpp/releases/download/'):
        raise RuntimeError('Unexpected runtime download URL')
    with tempfile.TemporaryDirectory(prefix='.setup-', dir=root / 'runtime') as tmp:
        tmp = Path(tmp)
        archive = tmp / 'llama.tar.gz'
        download(url, archive, digest.split(':', 1)[1])
        unpacked = tmp / 'unpacked'
        unpacked.mkdir()
        with tarfile.open(archive, 'r:gz') as tar:
            tar.extractall(unpacked, filter='data')
        servers = list(unpacked.rglob('llama-server'))
        if len(servers) != 1 or not servers[0].is_file():
            raise RuntimeError('Expected one llama-server executable in ' + asset['name'])
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
config_path = root / 'config.json'
config = json.loads(config_path.read_text(encoding='utf-8')) if config_path.exists() else {}
config.update(server_file=f'runtime/{backend}/llama-server', fallback_server_file='runtime/cpu/llama-server', gpu_layers=0 if backend == 'cpu' else 99)
if config_path.exists():
    shutil.copyfile(config_path, root / 'config.json.bak')
config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('Setup complete. Restart Clipboard Translator.')
print('Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja')
PY
