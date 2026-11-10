#!/usr/bin/env python3
"""Manage the pinned llama.cpp release tag and asset hashes.

The pin lives in the setup scripts (for the checker tools that read them)
and in the binary's own copy in internal/setup/llama.go, which is what
setup actually installs since Issue #152. This tool rewrites and checks
all of them.

Usage:
    python3 tools/bump_llama_pin.py <tag>
    python3 tools/bump_llama_pin.py --check [<tag>]
"""
import argparse
import json
import os
from pathlib import Path
import re
import sys
import urllib.request

START_MARKER = '# --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---'
END_MARKER = '# --- end llama.cpp pinned release ---'
# internal/setup/llama.go carries the same block with Go comment markers.
GO_START_MARKER = '// --- llama.cpp pinned release (managed by tools/bump_llama_pin.py) ---'
GO_END_MARKER = '// --- end llama.cpp pinned release ---'

RE_LLAMA_TAG = re.compile(r'^b[0-9]+$')
RE_SHA256 = re.compile(r'^[0-9a-f]{64}$')
SHELL_META_CHARS = set(' \t\r\n\'"`$')
LLAMA_URL_PREFIX = 'https://github.com/ggml-org/llama.cpp/releases/download/'


def validate_tag(tag):
    if not isinstance(tag, str) or not RE_LLAMA_TAG.match(tag):
        raise ValueError(f'Invalid llama.cpp tag {tag!r} (must match ^b[0-9]+$)')


def validate_url(url, prefix=LLAMA_URL_PREFIX):
    if not isinstance(url, str) or not url.startswith(prefix):
        raise ValueError(f'URL must start with {prefix!r}: {url!r}')
    if any(c in SHELL_META_CHARS for c in url):
        raise ValueError(f'URL contains whitespace or shell metacharacters: {url!r}')


def validate_sha256(digest):
    if not isinstance(digest, str) or not RE_SHA256.match(digest):
        raise ValueError(f'Invalid sha256 {digest!r} (must match ^[0-9a-f]{{64}}$)')


def validate_pin_output(tag, url=None, sha256=None):
    validate_tag(tag)
    if url is not None:
        validate_url(url)
    if sha256 is not None:
        validate_sha256(sha256)


ASSET_PATTERNS = {
    'win_cpu': re.compile(r'^llama-.*-bin-win-cpu-x64\.zip$'),
    'win_cuda': re.compile(r'^llama-.*-bin-win-cuda-12\.4-x64\.zip$'),
    'win_cudart': re.compile(r'^cudart-llama.*-bin-win-cuda-12\.4-x64\.zip$'),
    'win_vulkan': re.compile(r'^llama-.*-bin-win-vulkan-x64\.zip$'),
    'linux_cpu': re.compile(r'^llama-.*-bin-ubuntu-x64\.tar\.gz$'),
    'linux_vulkan': re.compile(r'^llama-.*-bin-ubuntu-vulkan-x64\.tar\.gz$'),
    'macos_arm64': re.compile(r'^llama-.*-bin-macos-arm64\.tar\.gz$'),
    'macos_x64': re.compile(r'^llama-.*-bin-macos-x64\.tar\.gz$'),
}


def fetch_release(tag, token=None):
    url = f'https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/{tag}'
    headers = {
        'User-Agent': 'ClipboardTranslatorBumpPin',
        'Accept': 'application/vnd.github+json',
    }
    token = token or os.environ.get('GITHUB_TOKEN')
    if token:
        headers['Authorization'] = f'Bearer {token}'
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode('utf-8'))


def resolve_assets(release):
    tag = release.get('tag_name')
    if not tag:
        raise ValueError('Release JSON missing tag_name')
    raw_assets = release.get('assets', [])
    assets = {}
    for key, pattern in ASSET_PATTERNS.items():
        matches = [a for a in raw_assets if pattern.match(a.get('name', ''))]
        if not matches:
            raise ValueError(f'Missing required asset for {key} matching {pattern.pattern} in release {tag}')
        asset = matches[0]
        digest = asset.get('digest') or ''
        m = re.match(r'^sha256:([0-9a-fA-F]{64})$', digest)
        if not m:
            raise ValueError(f'Asset {asset.get("name")} in release {tag} lacks valid sha256 digest (got {digest!r})')
        assets[key] = {
            'file': asset['name'],
            'sha256': m.group(1).lower(),
        }
    return tag, assets


def generate_ps1_block(tag, assets, newline):
    lines = [
        START_MARKER,
        'function Get-LlamaRelease {',
        '    return [ordered]@{',
        f'        Tag = \'{tag}\'',
        '        Assets = [ordered]@{',
        '            \'cpu\' = [ordered]@{',
        f'                File   = \'{assets["win_cpu"]["file"]}\'',
        f'                Sha256 = \'{assets["win_cpu"]["sha256"]}\'',
        '            }',
        '            \'cuda\' = [ordered]@{',
        f'                File   = \'{assets["win_cuda"]["file"]}\'',
        f'                Sha256 = \'{assets["win_cuda"]["sha256"]}\'',
        '            }',
        '            \'cudart\' = [ordered]@{',
        f'                File   = \'{assets["win_cudart"]["file"]}\'',
        f'                Sha256 = \'{assets["win_cudart"]["sha256"]}\'',
        '            }',
        '            \'vulkan\' = [ordered]@{',
        f'                File   = \'{assets["win_vulkan"]["file"]}\'',
        f'                Sha256 = \'{assets["win_vulkan"]["sha256"]}\'',
        '            }',
        '        }',
        '    }',
        '}',
        END_MARKER,
    ]
    return newline.join(lines)


def generate_sh_block(tag, assets, newline):
    # Since Issue #152 the shell scripts only carry the pin as a comment
    # record (the binary does the work), so every line is commented out.
    lines = [
        f'# LLAMA_TAG = \'{tag}\'',
        '# LLAMA_ASSETS = {',
        '#     \'cpu\': {',
        f'#         \'file\': \'{assets["linux_cpu"]["file"]}\',',
        f'#         \'sha256\': \'{assets["linux_cpu"]["sha256"]}\',',
        '#     },',
        '#     \'vulkan\': {',
        f'#         \'file\': \'{assets["linux_vulkan"]["file"]}\',',
        f'#         \'sha256\': \'{assets["linux_vulkan"]["sha256"]}\',',
        '#     },',
        '# }',
    ]
    return newline.join([START_MARKER] + lines + [END_MARKER])


def generate_macos_block(tag, assets, newline):
    # Comment record only, like generate_sh_block.
    lines = [
        f'# LLAMA_TAG = \'{tag}\'',
        '# LLAMA_ASSETS = {',
        '#     \'arm64\': {',
        f'#         \'file\': \'{assets["macos_arm64"]["file"]}\',',
        f'#         \'sha256\': \'{assets["macos_arm64"]["sha256"]}\',',
        '#     },',
        '#     \'x64\': {',
        f'#         \'file\': \'{assets["macos_x64"]["file"]}\',',
        f'#         \'sha256\': \'{assets["macos_x64"]["sha256"]}\',',
        '#     },',
        '# }',
    ]
    return newline.join([START_MARKER] + lines + [END_MARKER])


# Order of the Go map literal in internal/setup/llama.go (gofmt-aligned).
GO_ASSET_KEYS = [
    ('win/cpu', 'win_cpu'),
    ('win/cuda', 'win_cuda'),
    ('win/cudart', 'win_cudart'),
    ('win/vulkan', 'win_vulkan'),
    ('linux/cpu', 'linux_cpu'),
    ('linux/vulkan', 'linux_vulkan'),
    ('darwin/arm64', 'macos_arm64'),
    ('darwin/x64', 'macos_x64'),
]


def generate_go_block(tag, assets, newline):
    lines = [
        GO_START_MARKER,
        f'const llamaTag = "{tag}"',
        '',
        'var llamaAssets = map[string]llamaAsset{',
    ]
    width = max(len(f'"{go_key}":') for go_key, _ in GO_ASSET_KEYS)
    for go_key, asset_key in GO_ASSET_KEYS:
        entry = assets[asset_key]
        prefix = '\t' + f'"{go_key}":'.ljust(width + 1)
        lines.append(f'{prefix}{{File: "{entry["file"]}", Sha256: "{entry["sha256"]}"}},')
    lines.append('}')
    lines.append('')  # gofmt separates the closing brace from the marker comment
    lines.append(GO_END_MARKER)
    return newline.join(lines)


def detect_newline(content):
    return '\r\n' if '\r\n' in content else '\n'


def replace_pin_block(content, new_block, start_marker=START_MARKER, end_marker=END_MARKER):
    start = content.find(start_marker)
    if start < 0:
        raise ValueError(f'Start marker not found: {start_marker}')
    end = content.find(end_marker, start)
    if end < 0:
        raise ValueError(f'End marker not found: {end_marker}')
    end += len(end_marker)
    return content[:start] + new_block + content[end:]


def read_file_raw(path):
    with open(path, 'r', encoding='utf-8', newline='') as f:
        return f.read()


def write_file_raw(path, content):
    with open(path, 'w', encoding='utf-8', newline='') as f:
        f.write(content)


def go_llama_path(root):
    return root / 'internal' / 'setup' / 'llama.go'


def rewrite_scripts(root_dir, tag, assets):
    root = Path(root_dir)
    targets = [
        (root / 'setup.ps1', generate_ps1_block, START_MARKER, END_MARKER),
        (root / 'setup.sh', generate_sh_block, START_MARKER, END_MARKER),
        (root / 'setup-macos.sh', generate_macos_block, START_MARKER, END_MARKER),
    ]
    # The Go pin block is rewritten when the file is present (test fixtures
    # with only the three scripts keep working without it).
    go_path = go_llama_path(root)
    if go_path.is_file():
        targets.append((go_path, generate_go_block, GO_START_MARKER, GO_END_MARKER))
    for path, gen, start_marker, end_marker in targets:
        text = read_file_raw(path)
        nl = detect_newline(text)
        block = gen(tag, assets, nl)
        updated = replace_pin_block(text, block, start_marker, end_marker)
        write_file_raw(path, updated)


def extract_script_pin(content, start_marker=START_MARKER, end_marker=END_MARKER):
    start = content.find(start_marker)
    if start < 0:
        return None
    end = content.find(end_marker, start)
    if end < 0:
        return None
    block = content[start:end + len(end_marker)]

    tag_m = re.search(r"(?:Tag = |LLAMA_TAG = )'([^']+)'", block)
    if not tag_m:
        tag_m = re.search(r'llamaTag = "([^"]+)"', block)
    tag = tag_m.group(1) if tag_m else None

    entries = {}
    # PowerShell syntax: 'name' = [ordered]@{ File = '...'; Sha256 = '...' }
    ps_pattern = re.compile(
        r"'(\w+)'\s*=\s*\[ordered\]@\{[^}]*?File\s*=\s*'([^']+)'[^}]*?Sha256\s*=\s*'([0-9a-fA-F]{64})'",
        re.DOTALL,
    )
    for m in ps_pattern.finditer(block):
        entries[m.group(1)] = {'file': m.group(2), 'sha256': m.group(3).lower()}

    # Python syntax: 'name': { 'file': '...', 'sha256': '...' }
    py_pattern = re.compile(
        r"'(\w+)'\s*:\s*\{[^}]*?'file'\s*:\s*'([^']+)'[^}]*?'sha256'\s*:\s*'([0-9a-fA-F]{64})'",
        re.DOTALL,
    )
    for m in py_pattern.finditer(block):
        entries[m.group(1)] = {'file': m.group(2), 'sha256': m.group(3).lower()}

    # Go syntax: "name": {File: "...", Sha256: "..."},
    go_pattern = re.compile(
        r'"([\w/]+)":\s*\{File: "([^"]+)", Sha256: "([0-9a-fA-F]{64})"\}',
    )
    for m in go_pattern.finditer(block):
        entries[m.group(1)] = {'file': m.group(2), 'sha256': m.group(3).lower()}

    return {'tag': tag, 'entries': entries, 'block': block}


def check_scripts(root_dir, expected_tag, expected_assets):
    root = Path(root_dir)
    errors = []

    # Map expected asset keys to file-specific keys. Each item is
    # (path, label, expectations, start marker, end marker, required).
    script_expectations = [
        (root / 'setup.ps1', 'setup.ps1', {
            'cpu': expected_assets['win_cpu'],
            'cuda': expected_assets['win_cuda'],
            'cudart': expected_assets['win_cudart'],
            'vulkan': expected_assets['win_vulkan'],
        }, START_MARKER, END_MARKER, True),
        (root / 'setup.sh', 'setup.sh', {
            'cpu': expected_assets['linux_cpu'],
            'vulkan': expected_assets['linux_vulkan'],
        }, START_MARKER, END_MARKER, True),
        (root / 'setup-macos.sh', 'setup-macos.sh', {
            'arm64': expected_assets['macos_arm64'],
            'x64': expected_assets['macos_x64'],
        }, START_MARKER, END_MARKER, True),
        (go_llama_path(root), 'internal/setup/llama.go', {
            go_key: expected_assets[asset_key] for go_key, asset_key in GO_ASSET_KEYS
        }, GO_START_MARKER, GO_END_MARKER, False),
    ]

    for path, label, exp, start_marker, end_marker, required in script_expectations:
        if not path.is_file():
            if required:
                errors.append(f'{label}: file not found')
            continue
        text = path.read_text(encoding='utf-8')
        pin = extract_script_pin(text, start_marker, end_marker)
        if not pin or not pin['tag']:
            errors.append(f'{label}: pinned block or tag missing')
            continue
        if pin['tag'] != expected_tag:
            errors.append(f'{label}: tag is {pin["tag"]}, expected {expected_tag}')
        for k, v in exp.items():
            entry = pin['entries'].get(k)
            if not entry:
                errors.append(f'{label}: missing entry for {k}')
                continue
            if entry['file'] != v['file']:
                errors.append(f'{label} {k}: file is {entry["file"]}, expected {v["file"]}')
            if entry['sha256'] != v['sha256']:
                errors.append(f'{label} {k}: sha256 is {entry["sha256"]}, expected {v["sha256"]}')

    return errors


def get_pin_details(root_dir, target):
    root = Path(root_dir)
    sh_path = root / 'setup.sh'
    if not sh_path.is_file():
        raise FileNotFoundError(f'{sh_path} not found')
    pin = extract_script_pin(sh_path.read_text(encoding='utf-8'))
    if not pin or not pin['tag']:
        raise ValueError('Could not extract pin from setup.sh')
    tag = pin['tag']

    norm = target.lower().replace('_', '-')
    key = None
    if norm in ('linux-cpu', 'cpu'):
        key = 'cpu'
    elif norm in ('linux-vulkan', 'vulkan'):
        key = 'vulkan'
    else:
        raise ValueError(f'Unknown target: {target}')

    entry = pin['entries'].get(key)
    if not entry:
        raise ValueError(f'Asset {key} not found in setup.sh pin block')

    url = f'{LLAMA_URL_PREFIX}{tag}/{entry["file"]}'
    sha256 = entry['sha256']
    validate_pin_output(tag, url, sha256)
    return {'tag': tag, 'url': url, 'sha256': sha256, 'file': entry['file']}


def show_pin(root_dir, target):
    norm = target.lower().replace('_', '-')
    if norm == 'tag':
        root = Path(root_dir)
        sh_path = root / 'setup.sh'
        if not sh_path.is_file():
            raise FileNotFoundError(f'{sh_path} not found')
        pin = extract_script_pin(sh_path.read_text(encoding='utf-8'))
        if not pin or not pin['tag']:
            raise ValueError('Could not extract pin from setup.sh')
        tag = pin['tag']
        validate_tag(tag)
        return tag

    info = get_pin_details(root_dir, target)
    return f'{info["url"]} {info["sha256"]}'


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Verify scripts match the release digests without rewriting')
    parser.add_argument('--show', metavar='ASSET', help='Print pinned URL and SHA-256 (or tag) from setup.sh (e.g. linux-cpu, tag)')
    parser.add_argument('--github-output', nargs='?', const='linux-cpu', metavar='ASSET',
                        help='Emit KEY=value lines for $GITHUB_OUTPUT (default: linux-cpu)')
    parser.add_argument('tag', nargs='?', help='llama.cpp release tag (e.g. b12032)')
    args = parser.parse_args(argv)

    root_dir = Path(__file__).resolve().parents[1]

    if args.github_output:
        try:
            info = get_pin_details(root_dir, args.github_output)
            print(f'tag={info["tag"]}')
            print(f'url={info["url"]}')
            print(f'sha256={info["sha256"]}')
            return 0
        except Exception as exc:
            sys.exit(f'Error getting pin info: {exc}')

    if args.show:
        try:
            print(show_pin(root_dir, args.show))
            return 0
        except Exception as exc:
            sys.exit(f'Error showing pin: {exc}')

    tag = args.tag
    if not tag and args.check:
        ps1_text = (root_dir / 'setup.ps1').read_text(encoding='utf-8')
        pin = extract_script_pin(ps1_text)
        if not pin or not pin['tag']:
            sys.exit('Error: Could not extract existing tag from setup.ps1 and no tag provided.')
        tag = pin['tag']
    elif not tag:
        parser.error('tag is required unless --check is used with existing scripts')

    print(f'Fetching release info for tag {tag}...')
    try:
        release = fetch_release(tag)
        expected_tag, expected_assets = resolve_assets(release)
    except Exception as exc:
        sys.exit(f'Error fetching or parsing release {tag}: {exc}')

    if args.check:
        errors = check_scripts(root_dir, expected_tag, expected_assets)
        if errors:
            print('Check failed:', file=sys.stderr)
            for err in errors:
                print(f'  - {err}', file=sys.stderr)
            sys.exit(1)
        print(f'OK: setup scripts match published digests for llama.cpp release {tag}')
        return 0

    print(f'Rewriting pin blocks in setup scripts for {expected_tag}...')
    rewrite_scripts(root_dir, expected_tag, expected_assets)
    print(f'Successfully updated setup.ps1, setup.sh, setup-macos.sh and internal/setup/llama.go to {expected_tag}.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
