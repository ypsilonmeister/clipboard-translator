#!/usr/bin/env python3
"""Offline unit tests for tools/bump_llama_pin.py."""
import copy
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import bump_llama_pin as bump

FIXTURE_TAG = 'b99999'
FIXTURE_RELEASE = {
    'tag_name': FIXTURE_TAG,
    'assets': [
        {'name': f'llama-{FIXTURE_TAG}-bin-win-cpu-x64.zip', 'digest': 'sha256:' + 'a' * 64},
        {'name': f'llama-{FIXTURE_TAG}-bin-win-cuda-12.4-x64.zip', 'digest': 'sha256:' + 'b' * 64},
        {'name': 'cudart-llama-bin-win-cuda-12.4-x64.zip', 'digest': 'sha256:' + 'c' * 64},
        {'name': f'llama-{FIXTURE_TAG}-bin-win-vulkan-x64.zip', 'digest': 'sha256:' + 'd' * 64},
        {'name': f'llama-{FIXTURE_TAG}-bin-ubuntu-x64.tar.gz', 'digest': 'sha256:' + 'e' * 64},
        {'name': f'llama-{FIXTURE_TAG}-bin-ubuntu-vulkan-x64.tar.gz', 'digest': 'sha256:' + 'f' * 64},
        {'name': f'llama-{FIXTURE_TAG}-bin-macos-arm64.tar.gz', 'digest': 'sha256:' + '1' * 64},
        {'name': f'llama-{FIXTURE_TAG}-bin-macos-x64.tar.gz', 'digest': 'sha256:' + '2' * 64},
    ],
}

PS1_TEMPLATE = f'''# header
{bump.START_MARKER}
# old pin
{bump.END_MARKER}
# footer
'''

SH_TEMPLATE = f'''#!/usr/bin/env bash
{bump.START_MARKER}
# old pin
{bump.END_MARKER}
# footer
'''


def write_file_raw(path, content):
    with open(path, 'w', encoding='utf-8', newline='') as f:
        f.write(content)


class BumpLlamaPinTests(unittest.TestCase):
    def test_resolve_assets_success(self):
        tag, assets = bump.resolve_assets(FIXTURE_RELEASE)
        self.assertEqual(tag, FIXTURE_TAG)
        self.assertEqual(len(assets), 8)
        self.assertEqual(assets['win_cpu']['sha256'], 'a' * 64)
        self.assertEqual(assets['win_cuda']['sha256'], 'b' * 64)
        self.assertEqual(assets['win_cudart']['sha256'], 'c' * 64)
        self.assertEqual(assets['win_vulkan']['sha256'], 'd' * 64)
        self.assertEqual(assets['linux_cpu']['sha256'], 'e' * 64)
        self.assertEqual(assets['linux_vulkan']['sha256'], 'f' * 64)
        self.assertEqual(assets['macos_arm64']['sha256'], '1' * 64)
        self.assertEqual(assets['macos_x64']['sha256'], '2' * 64)

    def test_resolve_assets_missing_asset(self):
        rel = copy.deepcopy(FIXTURE_RELEASE)
        rel['assets'] = [a for a in rel['assets'] if 'win-cpu' not in a['name']]
        with self.assertRaisesRegex(ValueError, 'Missing required asset for win_cpu'):
            bump.resolve_assets(rel)

    def test_resolve_assets_missing_or_bad_digest(self):
        for bad_digest in ['', 'md5:123', 'sha256:not64chars', None]:
            with self.subTest(bad_digest=bad_digest):
                rel = copy.deepcopy(FIXTURE_RELEASE)
                rel['assets'][0]['digest'] = bad_digest
                with self.assertRaisesRegex(ValueError, 'lacks valid sha256 digest'):
                    bump.resolve_assets(rel)

    def test_replace_pin_block(self):
        orig = f"before\n{bump.START_MARKER}\nold\n{bump.END_MARKER}\nafter\n"
        new_block = f"{bump.START_MARKER}\nnew\n{bump.END_MARKER}"
        replaced = bump.replace_pin_block(orig, new_block)
        self.assertEqual(replaced, f"before\n{bump.START_MARKER}\nnew\n{bump.END_MARKER}\nafter\n")

    def test_replace_pin_block_missing_markers(self):
        with self.assertRaisesRegex(ValueError, 'Start marker not found'):
            bump.replace_pin_block("no markers", "new")
        with self.assertRaisesRegex(ValueError, 'End marker not found'):
            bump.replace_pin_block(f"start: {bump.START_MARKER} but no end", "new")

    def test_rewrite_and_check_scripts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_file_raw(root / 'setup.ps1', PS1_TEMPLATE.replace('\n', '\r\n'))
            write_file_raw(root / 'setup.sh', SH_TEMPLATE)
            write_file_raw(root / 'setup-macos.sh', SH_TEMPLATE)

            tag, assets = bump.resolve_assets(FIXTURE_RELEASE)
            bump.rewrite_scripts(root, tag, assets)

            # Check line endings preserved
            ps1_bytes = (root / 'setup.ps1').read_bytes()
            self.assertIn(b'\r\n', ps1_bytes)
            self.assertNotIn(b'\r\r\n', ps1_bytes)
            sh_bytes = (root / 'setup.sh').read_bytes()
            self.assertNotIn(b'\r\n', sh_bytes)

            # check_scripts passes
            errors = bump.check_scripts(root, tag, assets)
            self.assertEqual(errors, [])

            # Corrupt one hash in setup.sh
            sh_text = (root / 'setup.sh').read_text(encoding='utf-8')
            corrupted = sh_text.replace('e' * 64, '0' * 64)
            write_file_raw(root / 'setup.sh', corrupted)
            errors = bump.check_scripts(root, tag, assets)
            self.assertEqual(len(errors), 1)
            self.assertIn('setup.sh cpu: sha256 is', errors[0])

    def test_main_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_file_raw(root / 'setup.ps1', PS1_TEMPLATE.replace('\n', '\r\n'))
            write_file_raw(root / 'setup.sh', SH_TEMPLATE)
            write_file_raw(root / 'setup-macos.sh', SH_TEMPLATE)

            with mock.patch('bump_llama_pin.Path.resolve') as mock_resolve:
                # Make tools/.. resolve to root
                mock_resolve.return_value = root / 'tools' / 'bump_llama_pin.py'
                with mock.patch('bump_llama_pin.fetch_release', return_value=FIXTURE_RELEASE):
                    # Rewrite via main
                    ret = bump.main([FIXTURE_TAG])
                    self.assertEqual(ret, 0)

                    # Check via main with tag
                    ret = bump.main(['--check', FIXTURE_TAG])
                    self.assertEqual(ret, 0)

                    # Check without tag (infers from scripts)
                    ret = bump.main(['--check'])
                    self.assertEqual(ret, 0)

                    # Show linux-cpu
                    out = io.StringIO()
                    with mock.patch('sys.stdout', out):
                        ret = bump.main(['--show', 'linux-cpu'])
                    self.assertEqual(ret, 0)
                    want_url = f'https://github.com/ggml-org/llama.cpp/releases/download/{FIXTURE_TAG}/llama-{FIXTURE_TAG}-bin-ubuntu-x64.tar.gz'
                    self.assertEqual(out.getvalue().strip(), f'{want_url} {"e" * 64}')

                    # Show tag
                    out = io.StringIO()
                    with mock.patch('sys.stdout', out):
                        ret = bump.main(['--show', 'tag'])
                    self.assertEqual(ret, 0)
                    self.assertEqual(out.getvalue().strip(), FIXTURE_TAG)

                    # Github output
                    out = io.StringIO()
                    with mock.patch('sys.stdout', out):
                        ret = bump.main(['--github-output', 'linux-cpu'])
                    self.assertEqual(ret, 0)
                    gh_out = out.getvalue()
                    self.assertIn(f'tag={FIXTURE_TAG}\n', gh_out)
                    self.assertIn(f'url={want_url}\n', gh_out)
                    self.assertIn(f'sha256={"e" * 64}\n', gh_out)

    def test_show_pin_direct(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_file_raw(root / 'setup.ps1', PS1_TEMPLATE)
            write_file_raw(root / 'setup.sh', SH_TEMPLATE)
            write_file_raw(root / 'setup-macos.sh', SH_TEMPLATE)
            tag, assets = bump.resolve_assets(FIXTURE_RELEASE)
            bump.rewrite_scripts(root, tag, assets)

            out = bump.show_pin(root, 'linux-cpu')
            self.assertIn(f'llama-{FIXTURE_TAG}-bin-ubuntu-x64.tar.gz', out)
            self.assertIn('e' * 64, out)

            tag_out = bump.show_pin(root, 'tag')
            self.assertEqual(tag_out, FIXTURE_TAG)

            details = bump.get_pin_details(root, 'linux-cpu')
            self.assertEqual(details['tag'], FIXTURE_TAG)
            self.assertEqual(details['sha256'], 'e' * 64)

            with self.assertRaises(ValueError):
                bump.show_pin(root, 'unknown-asset')

    def test_validation_rules(self):
        # Valid tag
        for valid_tag in ['b1', 'b1234', 'b99999']:
            with self.subTest(valid_tag=valid_tag):
                bump.validate_tag(valid_tag)

        # Invalid tag
        for bad_tag in ['v1.2.3', 'b', '1234', 'b12;rm', 'b12 34', '', None, 42]:
            with self.subTest(bad_tag=bad_tag):
                with self.assertRaisesRegex(ValueError, 'Invalid llama.cpp tag'):
                    bump.validate_tag(bad_tag)

        # Valid URL
        valid_url = 'https://github.com/ggml-org/llama.cpp/releases/download/b123/file.tar.gz'
        bump.validate_url(valid_url)

        # Invalid URL prefix
        for bad_url in ['http://github.com/ggml-org/llama.cpp/releases/download/b1/file', 'https://malicious.org/', '', None]:
            with self.subTest(bad_url=bad_url):
                with self.assertRaisesRegex(ValueError, 'URL must start with'):
                    bump.validate_url(bad_url)

        # URL with shell metacharacters
        for bad_char in [' ', '\t', '\n', "'", '"', '$', '`']:
            with self.subTest(bad_char=bad_char):
                with self.assertRaisesRegex(ValueError, 'whitespace or shell metacharacters'):
                    bump.validate_url(f'{valid_url}{bad_char}foo')

        # Valid sha256
        bump.validate_sha256('a' * 64)

        # Invalid sha256
        for bad_sha in ['a' * 63, 'a' * 65, 'A' * 64, 'z' * 64, '', None]:
            with self.subTest(bad_sha=bad_sha):
                with self.assertRaisesRegex(ValueError, 'Invalid sha256'):
                    bump.validate_sha256(bad_sha)

    def test_smoke_workflow_yaml_syntax(self):
        wf = Path(__file__).resolve().parents[1] / '.github' / 'workflows' / 'llama-smoke.yml'
        self.assertTrue(wf.is_file(), f'{wf} not found')
        text = wf.read_text(encoding='utf-8')
        self.assertNotIn('\t', text, 'YAML must not contain tab characters')
        lines = text.splitlines()
        self.assertGreater(len(lines), 30)

        # Basic bracket balance check
        pairs = {'[': ']', '{': '}'}
        brackets = []
        in_block = False
        block_indent = 0
        for lineno, line in enumerate(lines, 1):
            if not line.strip() or line.strip().startswith('#'):
                continue
            indent = len(line) - len(line.lstrip(' '))
            if in_block:
                if indent <= block_indent:
                    in_block = False
                else:
                    continue
            if line.rstrip().endswith(('|', '>-', '|-', '>')):
                in_block = True
                block_indent = indent
                continue
            in_quote = None
            for char in line:
                if in_quote:
                    if char == in_quote:
                        in_quote = None
                elif char in ('"', "'"):
                    in_quote = char
                elif char in pairs:
                    brackets.append((char, lineno))
                elif char in pairs.values():
                    self.assertTrue(brackets, f'Unmatched closing {char} at line {lineno}')
                    top, _ = brackets.pop()
                    self.assertEqual(pairs[top], char, f'Mismatched {top} and {char} at line {lineno}')
        self.assertEqual(brackets, [])


if __name__ == '__main__':
    unittest.main()
