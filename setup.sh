#!/usr/bin/env bash
# Linux setup: all downloads are explicit; normal translation remains offline.
set -euo pipefail
backend=cpu
model=''
usage() { echo 'Usage: bash setup.sh [--backend cpu|vulkan] [--model 0.8b|2b|8b]' >&2; exit 2; }
while [[ $# -gt 0 ]]; do
    case $1 in
        --backend) [[ $# -ge 2 && $2 =~ ^(cpu|vulkan)$ ]] || usage; backend=$2; shift 2 ;;
        --model) [[ $# -ge 2 && $2 =~ ^(0\.8b|2b|8b)$ ]] || usage; model=$2; shift 2 ;;
        *) usage ;;
    esac
done
if [[ $(uname -s) != Linux ]]; then echo 'This setup script supports Linux only.' >&2; exit 1; fi
command -v python3 >/dev/null || { echo 'Install Python 3.12+ to run setup.' >&2; exit 1; }
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
python3 - "$root" "$backend" "$model" <<'PY'
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
model_arg = sys.argv[3] if len(sys.argv) > 3 else ''
if not hasattr(tarfile, 'data_filter'):
    sys.exit('Setup needs Python with tarfile.data_filter (Python 3.12+, or a security-patched Python 3.10/3.11).')
arch = {'x86_64': 'x64', 'aarch64': 'arm64'}.get(platform.machine())
if not arch:
    sys.exit('Supported Linux architectures: x86_64 and aarch64.')

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

if model_arg:
    config['model_file'] = 'models/' + model['file']
config.update(server_file=f'runtime/{backend}/llama-server', fallback_server_file='runtime/cpu/llama-server', gpu_layers=0 if backend == 'cpu' else 99)
if config_path.exists():
    shutil.copyfile(config_path, root / 'config.json.bak')
# Rewrite in place to retain hard links, consistent with the Windows settings writer.
config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print('Setup complete. Start ./clipboard-translator in an X11 session.')
print('Built with PLaMo: https://plamo.preferredai.jp/info/plamo-community-license-ja')
PY
