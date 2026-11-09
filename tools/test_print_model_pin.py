#!/usr/bin/env python3
"""Offline unit tests for tools/print_model_pin.py."""
import io
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import print_model_pin as pmp

FIXTURE_MODELS_GO = '''package main

var modelCatalog = []modelEntry{
	{Key: "0.8b", Name: "PLaMo 3 Translate 0.8B", Note: "Smallest and fastest.",
		File: "plamo-3-translate-0.8b-Q4_K_M.gguf", Repo: "plamo-3-translate-0.8b-GGUF",
		Revision:       "0c4af2b003fdd94b6868aa955414c1c86ee96997",
		Sha256:         "73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079",
		TemplateSha256: "084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e",
		SizeLabel:      "0.6 GB", MinBytes: 400 << 20, Bytes: 573125952},
	{Key: "2b", Name: "PLaMo 3 Translate 2B", Note: "The default.",
		File: "plamo-3-translate-2b-Q4_K_M.gguf", Repo: "plamo-3-translate-2b-GGUF",
		Revision:       "232fbd7916519edecddfdae0beae7e76fe70453f",
		Sha256:         "f36d0813dd95573a852ab5d76e374c543632d9e292929e922bcdbf7e53b494e0",
		TemplateSha256: "18dcc169548d794897084a04cd150a5a633717f1eb6e62de4a0e0973b377e02a",
		SizeLabel:      "1.8 GB", MinBytes: 1 << 30, Bytes: 1807199392},
}
'''


class PrintModelPinTests(unittest.TestCase):
    def test_parse_fixture(self):
        catalog = pmp.parse_models_go(FIXTURE_MODELS_GO)
        self.assertIn('0.8b', catalog)
        self.assertIn('2b', catalog)

        m08 = catalog['0.8b']
        self.assertEqual(m08['file'], 'plamo-3-translate-0.8b-Q4_K_M.gguf')
        self.assertEqual(m08['sha256'], '73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079')
        self.assertEqual(m08['template_sha256'], '084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e')
        self.assertEqual(
            m08['model_url'],
            'https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF/resolve/0c4af2b003fdd94b6868aa955414c1c86ee96997/plamo-3-translate-0.8b-Q4_K_M.gguf',
        )
        self.assertEqual(
            m08['template_url'],
            'https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF/resolve/0c4af2b003fdd94b6868aa955414c1c86ee96997/chat_template.jinja',
        )

    def test_get_model_info_from_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'models.go').write_text(FIXTURE_MODELS_GO, encoding='utf-8')

            info = pmp.get_model_info(root, '0.8B')
            self.assertEqual(info['key'], '0.8b')

            with self.assertRaises(KeyError):
                pmp.get_model_info(root, 'nonexistent')

    def test_main_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'models.go').write_text(FIXTURE_MODELS_GO, encoding='utf-8')

            with mock.patch('print_model_pin.Path.resolve') as mock_resolve:
                mock_resolve.return_value = root / 'tools' / 'print_model_pin.py'

                # Test --github-output
                out = io.StringIO()
                with mock.patch('sys.stdout', out):
                    ret = pmp.main(['0.8b', '--github-output'])
                self.assertEqual(ret, 0)
                gh_text = out.getvalue()
                self.assertIn('file=plamo-3-translate-0.8b-Q4_K_M.gguf\n', gh_text)
                self.assertIn('url=https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF/resolve/0c4af2b003fdd94b6868aa955414c1c86ee96997/plamo-3-translate-0.8b-Q4_K_M.gguf\n', gh_text)
                self.assertIn('sha256=73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079\n', gh_text)
                self.assertIn('template_url=https://huggingface.co/pfnet/plamo-3-translate-0.8b-GGUF/resolve/0c4af2b003fdd94b6868aa955414c1c86ee96997/chat_template.jinja\n', gh_text)
                self.assertIn('template_sha256=084af862c7844bdca74e8dca5516fde1f446919e00650ee4d8fae5b193fd064e\n', gh_text)

                # Test --env
                out = io.StringIO()
                with mock.patch('sys.stdout', out):
                    ret = pmp.main(['0.8b', '--env'])
                self.assertEqual(ret, 0)
                env_text = out.getvalue()
                self.assertIn('MODEL_FILE="plamo-3-translate-0.8b-Q4_K_M.gguf"', env_text)
                self.assertIn('MODEL_SHA256="73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079"', env_text)

                # Test --model-sha
                out = io.StringIO()
                with mock.patch('sys.stdout', out):
                    ret = pmp.main(['0.8b', '--model-sha'])
                self.assertEqual(ret, 0)
                self.assertEqual(out.getvalue().strip(), '73a1d942d8ab1e654d83898d997b8b4808c32ba669b378df612230989d18f079')

    def test_validation_rules(self):
        valid = {
            'file': 'valid-file_name.1.gguf',
            'model_url': 'https://huggingface.co/pfnet/repo/resolve/main/model.gguf',
            'sha256': 'a' * 64,
            'template_url': 'https://huggingface.co/pfnet/repo/resolve/main/chat_template.jinja',
            'template_sha256': 'b' * 64,
        }
        self.assertEqual(pmp.validate_model_info(valid), valid)

        # Invalid file name
        for bad_file in ['bad file.gguf', 'bad$file', 'bad;file', '../file', 'bad/file', '']:
            with self.subTest(bad_file=bad_file):
                info = dict(valid, file=bad_file)
                with self.assertRaisesRegex(ValueError, 'Invalid file name'):
                    pmp.validate_model_info(info)

        # Invalid URL prefix
        for bad_url in ['http://huggingface.co/pfnet/model', 'https://example.com/pfnet/model', '']:
            with self.subTest(bad_url=bad_url):
                info = dict(valid, model_url=bad_url)
                with self.assertRaisesRegex(ValueError, 'URL must start with'):
                    pmp.validate_model_info(info)

        # URL with spaces, quotes, $, backticks
        for bad_char in [' ', '\t', '\n', "'", '"', '$', '`']:
            with self.subTest(bad_char=bad_char):
                info = dict(valid, model_url=f'https://huggingface.co/pfnet/model{bad_char}test')
                with self.assertRaisesRegex(ValueError, 'whitespace or shell metacharacters'):
                    pmp.validate_model_info(info)

        # Invalid sha256
        for bad_sha in ['a' * 63, 'a' * 65, 'A' * 64, 'z' * 64, '']:
            with self.subTest(bad_sha=bad_sha):
                info = dict(valid, sha256=bad_sha)
                with self.assertRaisesRegex(ValueError, 'Invalid sha256'):
                    pmp.validate_model_info(info)

        # Invalid template_sha256
        for bad_sha in ['a' * 63, 'a' * 65, 'A' * 64, 'z' * 64, '']:
            with self.subTest(bad_sha=bad_sha):
                info = dict(valid, template_sha256=bad_sha)
                with self.assertRaisesRegex(ValueError, 'Invalid template_sha256'):
                    pmp.validate_model_info(info)


if __name__ == '__main__':
    unittest.main()
