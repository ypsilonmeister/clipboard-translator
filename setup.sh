#!/usr/bin/env bash
# Linux setup: all downloads are explicit; normal translation remains offline.
set -euo pipefail
backend=cpu
if [[ $# -gt 0 ]]; then
    if [[ $# != 2 || $1 != --backend || ! $2 =~ ^(cpu|vulkan)$ ]]; then
        echo 'Usage: bash setup.sh [--backend cpu|vulkan]' >&2
        exit 2
    fi
    backend=$2
fi
if [[ $(uname -s) != Linux ]]; then echo 'This setup script supports Linux only.' >&2; exit 1; fi
command -v python3 >/dev/null || { echo 'Install Python 3.12+ to run setup.' >&2; exit 1; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python3 - "$root" "$backend" <<'PY'
import hashlib
import json
import os
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
arch = {'x86_64': 'x64', 'aarch64': 'arm64'}.get(platform.machine())
if not arch:
    sys.exit('Supported Linux architectures: x86_64 and aarch64.')

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
hf = 'https://huggingface.co/pfnet/plamo-3-translate-2b-GGUF/resolve/main/'
for name, minimum in [('plamo-3-translate-2b-Q4_K_M.gguf', 1024**3), ('chat_template.jinja', 100)]:
    dest = root / 'models' / name
    if not dest.exists():
        print('Downloading ' + name, flush=True)
        download(hf + name, dest)
    if dest.stat().st_size < minimum:
        raise RuntimeError('Incomplete file; remove it and retry: ' + str(dest))

needed = [b for b in dict.fromkeys(['cpu', backend]) if not (root / 'runtime' / b / 'llama-server').is_file()]
if needed:
    with fetch('https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=30') as response:
        releases = json.load(response)
    chosen = None
    for release in releases:
        if release.get('draft') or release.get('prerelease'):
            continue
        assets = {}
        for b in needed:
            suffix = ('' if b == 'cpu' else '-vulkan') + '-' + arch
            pattern = r'^llama-.*-bin-ubuntu' + re.escape(suffix) + r'\.tar\.gz$'
            matches = [a for a in release.get('assets', []) if re.fullmatch(pattern, a['name'])]
            if matches:
                assets[b] = matches[0]
        if len(assets) == len(needed):
            chosen = (release['tag_name'], assets)
            break
    if not chosen:
        raise RuntimeError('No official llama.cpp release with the requested Linux CPU/Vulkan assets found.')
    tag, assets = chosen
    print('Using official llama.cpp release ' + tag, flush=True)
    for b, asset in assets.items():
        digest = asset.get('digest') or ''
        if not re.fullmatch(r'sha256:[0-9a-fA-F]{64}', digest):
            raise RuntimeError('Missing SHA-256 for ' + asset['name'])
        url = asset['browser_download_url']
        if not url.startswith('https://github.com/ggml-org/llama.cpp/releases/download/'):
            raise RuntimeError('Unexpected runtime download URL')
        # Stage all files before making the installation visible. A partial
        # download/extraction must never look like an installed backend.
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
            target = root / 'runtime' / b
            # Preserve the archive's layout and library symlinks next to the server.
            staged = tmp / 'backend'
            shutil.copytree(server.parent, staged, symlinks=True)
            if target.exists():
                raise RuntimeError('Incomplete runtime directory exists; move it aside and retry: ' + str(target))
            staged.rename(target)

config_path = root / 'config.json'
config = json.loads(config_path.read_text(encoding='utf-8')) if config_path.exists() else {}
config.update(server_file=f'runtime/{backend}/llama-server', fallback_server_file='runtime/cpu/llama-server', gpu_layers=0 if backend == 'cpu' else 99)
if config_path.exists():
    shutil.copyfile(config_path, root / 'config.json.bak')
# Rewrite in place to retain hard links, consistent with the Windows settings writer.
config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('Setup complete. Start ./clipboard-translator in an X11 session.')
print('Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja')
PY
