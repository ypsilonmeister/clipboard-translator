"""Exercise setup without network, a real model, or user configuration."""
import contextlib
import hashlib
import io
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

SOURCE = (Path(__file__).resolve().parents[1] / "setup.sh").read_text()
PROGRAM = compile(SOURCE.split("<<'PY'\n", 1)[1].rsplit("\nPY", 1)[0], "setup.sh:python", "exec")


def runtime_archive(unsafe=False):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w:gz") as tar:
        for name, content in [("../escaped" if unsafe else "llama-test/llama-server", b"#!/bin/sh\nexit 0\n"), ("llama-test/libggml.so.1", b"fixture")]:
            info = tarfile.TarInfo(name)
            info.size, info.mode = len(content), 0o755
            tar.addfile(info, io.BytesIO(content))
        link = tarfile.TarInfo("llama-test/libggml.so")
        link.type, link.linkname = tarfile.SYMTYPE, "libggml.so.1"
        tar.addfile(link)
    return out.getvalue()


class SetupTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / "models").mkdir()
        # Sparse file satisfies the model-size check without downloading a model.
        with (self.root / "models/plamo-3-translate-2b-Q4_K_M.gguf").open("wb") as f:
            f.truncate(1024 ** 3)
        (self.root / "models/chat_template.jinja").write_text("x" * 101)
        (self.root / "config.json").write_text(json.dumps({"hotkey": "Ctrl+Alt+T", "future_key": {"keep": True}}))
        self.archive = runtime_archive()
        self.digest = "sha256:" + hashlib.sha256(self.archive).hexdigest()
        self.calls = []

    def response(self, request, timeout):
        url = request.full_url
        self.calls.append(url)
        if url.endswith("releases?per_page=30"):
            assets = [{"name": f"llama-test-bin-ubuntu{suffix}-x64.tar.gz", "digest": self.digest, "browser_download_url": "https://github.com/ggml-org/llama.cpp/releases/download/test/" + backend} for backend, suffix in [("cpu", ""), ("vulkan", "-vulkan")]]
            return io.BytesIO(json.dumps([{"tag_name": "test", "assets": assets}]).encode())
        if url.startswith("https://github.com/ggml-org/llama.cpp/releases/download/test/"):
            return io.BytesIO(self.archive)
        if url.startswith("https://huggingface.co/pfnet/") and self.model_bytes is not None:
            return io.BytesIO(self.model_bytes)
        raise AssertionError("Unexpected network request: " + url)

    model_bytes = None  # what a Hugging Face download returns, when a test allows one

    def run_setup(self, backend="cpu", *model):
        with mock.patch("sys.argv", ["setup", str(self.root), backend, *model]), mock.patch("platform.machine", return_value="x86_64"), mock.patch("urllib.request.urlopen", side_effect=self.response), contextlib.redirect_stdout(io.StringIO()):
            exec(PROGRAM, {"__name__": "__main__"})

    def test_cpu_vulkan_config_and_symlinks(self):
        self.run_setup("vulkan")
        for backend in ["cpu", "vulkan"]:
            runtime = self.root / "runtime" / backend
            self.assertTrue((runtime / "llama-server").stat().st_mode & 0o111)
            self.assertTrue((runtime / "libggml.so").is_symlink())
            self.assertEqual((runtime / "libggml.so").read_bytes(), b"fixture")
        config = json.loads((self.root / "config.json").read_text())
        self.assertEqual(config["server_file"], "runtime/vulkan/llama-server")
        self.assertEqual(config["hotkey"], "Ctrl+Alt+T")
        self.assertEqual(config["future_key"], {"keep": True})
        self.assertTrue((self.root / "config.json.bak").exists())
        self.calls.clear()
        self.run_setup("vulkan")
        self.assertEqual(self.calls, [], "an installed setup should not contact the network")

    def put_model(self, name, size):
        with (self.root / "models" / name).open("wb") as f:
            f.truncate(size)

    def test_model_choice_is_written_and_kept(self):
        self.put_model("plamo-3-translate-0.8b-Q4_K_M.gguf", 400 * 1024 ** 2)
        self.run_setup("cpu", "0.8b")
        config = json.loads((self.root / "config.json").read_text())
        self.assertEqual(config["model_file"], "models/plamo-3-translate-0.8b-Q4_K_M.gguf")
        self.assertEqual(config["future_key"], {"keep": True})
        # An update without --model keeps it and does not go back to 2b.
        self.run_setup("cpu")
        self.assertEqual(json.loads((self.root / "config.json").read_text())["model_file"], "models/plamo-3-translate-0.8b-Q4_K_M.gguf")

    def test_plain_run_does_not_add_model_file(self):
        self.run_setup("cpu")
        self.assertNotIn("model_file", json.loads((self.root / "config.json").read_text()))

    def test_configured_model_is_fetched_from_its_pinned_revision_and_hash_checked(self):
        # The config names the 8B model (with a Windows-style path) and it is not on disk:
        # a plain run must fetch that one from its pinned commit and refuse wrong bytes.
        (self.root / "config.json").write_text(json.dumps({"model_file": "models\\plamo-3-translate-8b-Q4_K_M.gguf"}))
        self.model_bytes = b"not the model"
        with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
            self.run_setup("cpu")
        self.assertEqual(self.calls, ["https://huggingface.co/pfnet/plamo-3-translate-8b-GGUF/resolve/407c15bc44f6968e6734bc047d92f766c163bbec/plamo-3-translate-8b-Q4_K_M.gguf"])
        self.assertFalse((self.root / "models/plamo-3-translate-8b-Q4_K_M.gguf").exists(), "a bad download must not be left in place")
        self.assertEqual(list((self.root / "models").glob("*.partial")), [])

    def test_too_small_model_is_rejected(self):
        self.put_model("plamo-3-translate-8b-Q4_K_M.gguf", 1024 ** 3)
        with self.assertRaisesRegex(RuntimeError, "Incomplete file"):
            self.run_setup("cpu", "8b")

    def test_bad_arguments_stop_with_usage_before_doing_anything(self):
        # These exit while parsing the arguments, so nothing is downloaded or written.
        bash = shutil.which("bash")
        if not bash:
            self.skipTest("bash is not available")
        base = Path(__file__).resolve().parents[1]
        cases = [
            ("setup.sh", ["--model", "3b"]), ("setup.sh", ["--model"]), ("setup.sh", ["--backend"]),
            ("setup.sh", ["--bogus"]), ("setup.sh", ["--backend", "metal"]),
            ("setup-macos.sh", ["--model", "31b"]), ("setup-macos.sh", ["--backend", "vulkan"]),
        ]
        for script, args in cases:
            result = subprocess.run([bash, str(base / script), *args], capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 2, (script, args, result.stderr))
            self.assertIn("Usage: bash setup.sh", result.stderr)
            self.assertIn("--model 0.8b|2b|8b", result.stderr)

    def test_catalog_matches_windows_setup(self):
        # setup.ps1 and the Unix scripts must pin the same commits and hashes.
        base = Path(__file__).resolve().parents[1]
        ps1 = (base / "setup.ps1").read_text()
        for key in ["0.8b", "2b", "8b"]:
            match = re.search(r"'%s' = @\{.*?Revision = '([0-9a-f]{40})'\s+Sha256 = '([0-9a-f]{64})'\s+TemplateSha256 = '([0-9a-f]{64})'" % re.escape(key), ps1, re.S)
            self.assertTrue(match, key)
            for script in ["setup.sh", "setup-macos.sh"]:
                text = (base / script).read_text()
                for value in match.groups():
                    self.assertIn(value, text, f"{script} lacks {value} ({key})")

    def test_missing_digest_is_rejected(self):
        self.digest = None
        with self.assertRaisesRegex(RuntimeError, "Missing SHA-256"):
            self.run_setup()
        self.assertFalse((self.root / "runtime/cpu").exists())

    def test_checksum_mismatch_is_rejected(self):
        self.digest = "sha256:" + "0" * 64
        with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
            self.run_setup()
        self.assertFalse((self.root / "runtime/cpu").exists())

    def test_unsafe_archive_is_rejected(self):
        self.archive = runtime_archive(unsafe=True)
        self.digest = "sha256:" + hashlib.sha256(self.archive).hexdigest()
        with self.assertRaises(tarfile.FilterError):
            self.run_setup()
        self.assertFalse((self.root / "runtime/cpu").exists())
        self.assertEqual(list(self.root.rglob("escaped")), [])

    def test_partial_install_is_not_overwritten(self):
        partial = self.root / "runtime/cpu"
        partial.mkdir(parents=True)
        sentinel = partial / "keep"
        sentinel.write_text("keep me")
        with self.assertRaisesRegex(RuntimeError, "Incomplete runtime directory"):
            self.run_setup()
        self.assertEqual(sentinel.read_text(), "keep me")


if __name__ == "__main__":
    unittest.main()
