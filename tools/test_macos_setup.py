"""macOS setup fixtures; safe to run on Linux without a model or network."""
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import re
import tarfile
from unittest import mock
import unittest
import test_linux_setup as linux

SOURCE = (Path(__file__).resolve().parents[1] / 'setup-macos.sh').read_text()
PROGRAM = linux.fixture_program(SOURCE, 'setup-macos.sh:python')


class MacSetupTests(linux.SetupTests):
    def setUp(self):
        super().setUp()
        self.machine = 'arm64'

    def response(self, request, timeout):
        url = request.full_url
        self.calls.append(url)
        if url.startswith('https://github.com/ggml-org/llama.cpp/releases/download/'):
            return io.BytesIO(self.archive)
        if url.startswith('https://huggingface.co/pfnet/') and self.model_bytes is not None:
            return io.BytesIO(self.model_bytes)
        raise AssertionError('Unexpected request: ' + url)

    default_backend = 'metal'
    alternate_backend = 'cpu'

    def run_setup(self, backend=None, *model, prog=None):
        program = prog or PROGRAM
        with mock.patch('sys.argv', ['setup', str(self.root), backend or self.default_backend, model[0] if model else '', 'true' if backend else 'false']), mock.patch('platform.machine', return_value=self.machine), mock.patch('urllib.request.urlopen', side_effect=self.response), contextlib.redirect_stdout(io.StringIO()):
            exec(program, {'__name__': '__main__'})

    def test_cpu_vulkan_config_and_symlinks(self):
        self.run_setup()
        if os.name != 'nt':
            self.assertTrue((self.root / 'runtime/cpu/llama-server').stat().st_mode & 0o111)
        self.assertTrue((self.root / 'runtime/metal').is_symlink())
        self.assertTrue((self.root / 'runtime/metal/llama-server').is_file())
        config = json.loads((self.root / 'config.json').read_text())
        self.assertEqual(config['server_file'], 'runtime/metal/llama-server')
        self.assertEqual(config['hotkey'], 'Ctrl+Alt+T')
        self.assertEqual(config['future_key'], {'keep': True})
        self.assertTrue(any('macos-arm64' in url for url in self.calls))
        self.calls.clear()
        self.run_setup('cpu')
        self.assertEqual(self.calls, [])
        self.assertEqual(json.loads((self.root / 'config.json').read_text())['gpu_layers'], 0)

    def test_intel_architecture_selection(self):
        self.machine = 'x86_64'
        self.run_setup()
        self.assertTrue(any('macos-x64' in url for url in self.calls))

    def test_missing_digest_is_rejected(self):
        prog = linux.fixture_program(re.sub(r"'sha256': '[0-9a-fA-F]{64}'", "'sha256': ''", SOURCE), 'setup-macos.sh:python', archive_sha256=None)
        with self.assertRaisesRegex(RuntimeError, 'Missing SHA-256'):
            self.run_setup(prog=prog)
        self.assertFalse((self.root / 'runtime/cpu').exists())

    def test_unsafe_archive_is_rejected(self):
        unsafe = linux.runtime_archive(unsafe=True)
        self.archive = unsafe
        prog = linux.fixture_program(SOURCE, 'setup-macos.sh:python', archive_sha256=hashlib.sha256(unsafe).hexdigest())
        with self.assertRaises(tarfile.FilterError):
            self.run_setup(prog=prog)
        self.assertFalse((self.root / 'runtime/cpu').exists())
        self.assertEqual(list(self.root.rglob('escaped')), [])


if __name__ == '__main__':
    unittest.main()
