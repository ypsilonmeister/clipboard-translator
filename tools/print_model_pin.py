#!/usr/bin/env python3
"""Print pinned model information from internal/models/models.go for workflow and scripts.

Usage:
    python3 tools/print_model_pin.py 0.8b
    python3 tools/print_model_pin.py 0.8b --github-output
    python3 tools/print_model_pin.py 0.8b --env
    python3 tools/print_model_pin.py 0.8b --model-sha
"""
import argparse
from pathlib import Path
import re
import sys

HF_HOST = 'https://huggingface.co'
HF_PREFIX = 'https://huggingface.co/pfnet/'
RE_FILE = re.compile(r'^[A-Za-z0-9._-]+$')
RE_SHA256 = re.compile(r'^[0-9a-f]{64}$')
SHELL_META_CHARS = set(' \t\r\n\'"`$')


def validate_url(url, prefix=HF_PREFIX):
    if not isinstance(url, str) or not url.startswith(prefix):
        raise ValueError(f'URL must start with {prefix!r}: {url!r}')
    if any(c in SHELL_META_CHARS for c in url):
        raise ValueError(f'URL contains whitespace or shell metacharacters: {url!r}')


def validate_model_info(info):
    file_name = info.get('file', '')
    if not isinstance(file_name, str) or not RE_FILE.match(file_name):
        raise ValueError(f'Invalid file name {file_name!r} (must match ^[A-Za-z0-9._-]+$)')
    validate_url(info.get('model_url', ''))
    sha256 = info.get('sha256', '')
    if not isinstance(sha256, str) or not RE_SHA256.match(sha256):
        raise ValueError(f'Invalid sha256 {sha256!r} (must match ^[0-9a-f]{{64}}$)')
    validate_url(info.get('template_url', ''))
    template_sha256 = info.get('template_sha256', '')
    if not isinstance(template_sha256, str) or not RE_SHA256.match(template_sha256):
        raise ValueError(f'Invalid template_sha256 {template_sha256!r} (must match ^[0-9a-f]{{64}}$)')
    return info


def parse_models_go(content):
    """Extract model entries from internal/models/models.go text."""
    catalog = {}
    # Find struct literals in Catalog = []Entry{ ... }
    entry_re = re.compile(r'\{Key:\s*"([^"]+)",(?:[^{}]|\n)*?\}', re.MULTILINE)
    field_re = lambda name: re.compile(rf'{name}:\s*"([^"]+)"')

    for match in entry_re.finditer(content):
        block = match.group(0)
        key = match.group(1).lower()
        file_m = field_re('File').search(block)
        repo_m = field_re('Repo').search(block)
        rev_m = field_re('Revision').search(block)
        sha_m = field_re('Sha256').search(block)
        tmpl_sha_m = field_re('TemplateSha256').search(block)

        if not (file_m and repo_m and rev_m and sha_m and tmpl_sha_m):
            continue

        file_name = file_m.group(1)
        repo = repo_m.group(1)
        rev = rev_m.group(1)
        sha = sha_m.group(1).lower()
        tmpl_sha = tmpl_sha_m.group(1).lower()

        base_url = f'{HF_HOST}/pfnet/{repo}/resolve/{rev}/'
        catalog[key] = {
            'key': key,
            'file': file_name,
            'repo': repo,
            'revision': rev,
            'sha256': sha,
            'template_sha256': tmpl_sha,
            'model_url': base_url + file_name,
            'template_url': base_url + 'chat_template.jinja',
        }
    return catalog


def get_model_info(root_dir, key):
    models_path = Path(root_dir) / 'internal' / 'models' / 'models.go'
    if not models_path.is_file():
        raise FileNotFoundError(f'{models_path} not found')
    catalog = parse_models_go(models_path.read_text(encoding='utf-8'))
    norm_key = key.lower().strip()
    if norm_key not in catalog:
        available = ', '.join(sorted(catalog.keys()))
        raise KeyError(f'Model {key!r} not found in internal/models/models.go (available: {available})')
    info = catalog[norm_key]
    validate_model_info(info)
    return info


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('model', help='Model key (e.g. 0.8b, 2b, 8b)')
    parser.add_argument('--github-output', action='store_true', help='Emit KEY=value lines for $GITHUB_OUTPUT')
    parser.add_argument('--env', action='store_true', help='Output bash export statements')
    parser.add_argument('--model-url', action='store_true', help='Print only model download URL')
    parser.add_argument('--model-sha', action='store_true', help='Print only model SHA-256')
    parser.add_argument('--template-url', action='store_true', help='Print only template download URL')
    parser.add_argument('--template-sha', action='store_true', help='Print only template SHA-256')
    parser.add_argument('--file', action='store_true', help='Print only model file name')
    args = parser.parse_args(argv)

    root_dir = Path(__file__).resolve().parents[1]
    try:
        info = get_model_info(root_dir, args.model)
    except Exception as exc:
        sys.exit(f'Error: {exc}')

    if args.github_output:
        print(f'file={info["file"]}')
        print(f'url={info["model_url"]}')
        print(f'sha256={info["sha256"]}')
        print(f'template_url={info["template_url"]}')
        print(f'template_sha256={info["template_sha256"]}')
        return 0

    if args.env:
        print(f'MODEL_FILE="{info["file"]}"')
        print(f'MODEL_URL="{info["model_url"]}"')
        print(f'MODEL_SHA256="{info["sha256"]}"')
        print(f'TEMPLATE_URL="{info["template_url"]}"')
        print(f'TEMPLATE_SHA256="{info["template_sha256"]}"')
        return 0
    if args.model_url:
        print(info['model_url'])
        return 0
    if args.model_sha:
        print(info['sha256'])
        return 0
    if args.template_url:
        print(info['template_url'])
        return 0
    if args.template_sha:
        print(info['template_sha256'])
        return 0
    if args.file:
        print(info['file'])
        return 0

    print(f'{info["file"]} {info["model_url"]} {info["sha256"]} {info["template_url"]} {info["template_sha256"]}')
    return 0


if __name__ == '__main__':
    sys.exit(main())
