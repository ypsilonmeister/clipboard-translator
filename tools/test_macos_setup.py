"""macOS setup fixtures; safe to run on Linux without a model or network."""
import contextlib
import io
import json
from pathlib import Path
from unittest import mock
import unittest
import test_linux_setup as linux

SOURCE = (Path(__file__).resolve().parents[1] / 'setup-macos.sh').read_text()
PROGRAM = compile(SOURCE.split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0], 'setup-macos.sh:python', 'exec')


class MacSetupTests(linux.SetupTests):
    def setUp(self):
        super().setUp()
        self.machine = 'arm64'

    def response(self, request, timeout):
        url = request.full_url
        self.calls.append(url)
        if url.endswith('releases?per_page=30'):
            assets = [{'name': f'llama-test-bin-macos-{arch}.tar.gz', 'digest': self.digest, 'browser_download_url': 'https://github.com/ggml-org/llama.cpp/releases/download/test/' + arch} for arch in ['arm64', 'x64']]
            return io.BytesIO(json.dumps([{'tag_name': 'test', 'assets': assets}]).encode())
        if url.startswith('https://github.com/ggml-org/llama.cpp/releases/download/test/'):
            return io.BytesIO(self.archive)
        if url.startswith('https://huggingface.co/pfnet/') and self.model_bytes is not None:
            return io.BytesIO(self.model_bytes)
        raise AssertionError('Unexpected request: ' + url)

    def run_setup(self, backend='metal', *model):
        with mock.patch('sys.argv', ['setup', str(self.root), backend, *model]), mock.patch('platform.machine', return_value=self.machine), mock.patch('urllib.request.urlopen', side_effect=self.response), contextlib.redirect_stdout(io.StringIO()):
            exec(PROGRAM, {'__name__': '__main__'})

    def test_cpu_vulkan_config_and_symlinks(self):
        self.run_setup()
        self.assertTrue((self.root / 'runtime/cpu/llama-server').stat().st_mode & 0o111)
        self.assertTrue((self.root / 'runtime/metal').is_symlink())
        self.assertTrue((self.root / 'runtime/metal/llama-server').is_file())
        config = json.loads((self.root / 'config.json').read_text())
        self.assertEqual(config['server_file'], 'runtime/metal/llama-server')
        self.assertEqual(config['hotkey'], 'Ctrl+Alt+T')
        self.assertEqual(config['future_key'], {'keep': True})
        self.assertTrue(any(url.endswith('/arm64') for url in self.calls))
        self.calls.clear()
        self.run_setup('cpu')
        self.assertEqual(self.calls, [])
        self.assertEqual(json.loads((self.root / 'config.json').read_text())['gpu_layers'], 0)

    def test_intel_architecture_selection(self):
        self.machine = 'x86_64'
        self.run_setup()
        self.assertTrue(any(url.endswith('/x64') for url in self.calls))


if __name__ == '__main__':
    unittest.main()
