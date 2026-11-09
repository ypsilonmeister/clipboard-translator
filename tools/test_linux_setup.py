"""Exercise setup without network, a real model, or user configuration."""
import contextlib
import hashlib
import io
import json
import os
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


FIXTURE_ARCHIVE = runtime_archive()
FIXTURE_SHA256 = hashlib.sha256(FIXTURE_ARCHIVE).hexdigest()
FIXTURE_TEMPLATE_SHA256 = hashlib.sha256(b"x" * 101).hexdigest()


# Scale only the fixture catalog's minimum sizes; NTFS truncate is not sparse.
def fixture_program(source, filename, archive_sha256=FIXTURE_SHA256):
    source = source.replace('minimum=400 * 1024**2', 'minimum=400')
    source = source.replace('minimum=1024**3', 'minimum=1024')
    source = source.replace('minimum=4 * 1024**3', 'minimum=4096')
    source = re.sub(r"template_sha256='[0-9a-fA-F]{64}'", f"template_sha256='{FIXTURE_TEMPLATE_SHA256}'", source)
    if archive_sha256:
        source = re.sub(r"'sha256': '[0-9a-fA-F]{64}'", f"'sha256': '{archive_sha256}'", source)
    return compile(source.split("<<'PY'\n", 1)[1].rsplit("\nPY", 1)[0], filename, "exec")


PROGRAM = fixture_program(SOURCE, "setup.sh:python")


class SetupTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / "models").mkdir()
        # Small fixture satisfies the patched model-size check without a download.
        with (self.root / "models/plamo-3-translate-2b-Q4_K_M.gguf").open("wb") as f:
            f.truncate(1024)
        (self.root / "models/chat_template.jinja").write_text("x" * 101)
        (self.root / "models/chat_template.2b.jinja").write_text("x" * 101)
        (self.root / "config.json").write_text(json.dumps({"hotkey": "Ctrl+Alt+T", "future_key": {"keep": True}}))
        self.archive = FIXTURE_ARCHIVE
        self.calls = []

    def response(self, request, timeout):
        url = request.full_url
        self.calls.append(url)
        if url.startswith("https://github.com/ggml-org/llama.cpp/releases/download/"):
            return io.BytesIO(self.archive)
        if url.startswith("https://huggingface.co/pfnet/") and self.model_bytes is not None:
            return io.BytesIO(self.model_bytes)
        raise AssertionError("Unexpected network request: " + url)

    model_bytes = None  # what a Hugging Face download returns, when a test allows one

    default_backend = "cpu"
    alternate_backend = "vulkan"

    def run_setup(self, backend=None, *model, prog=None):
        program = prog or PROGRAM
        with mock.patch("sys.argv", ["setup", str(self.root), backend or self.default_backend, model[0] if model else "", "true" if backend else "false"]), mock.patch("platform.machine", return_value="x86_64"), mock.patch("urllib.request.urlopen", side_effect=self.response), contextlib.redirect_stdout(io.StringIO()):
            exec(program, {"__name__": "__main__"})

    def test_cpu_vulkan_config_and_symlinks(self):
        self.run_setup("vulkan")
        for backend in ["cpu", "vulkan"]:
            runtime = self.root / "runtime" / backend
            if os.name != "nt":
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

    def read_config(self):
        return json.loads((self.root / "config.json").read_text())

    def write_config(self, config):
        (self.root / "config.json").write_text(json.dumps(config))

    def installed_engines(self):
        self.run_setup(self.alternate_backend)
        self.run_setup(self.default_backend)
        self.calls.clear()

    def test_plain_rerun_preserves_configured_engine(self):
        self.installed_engines()
        config = self.read_config()
        config.update(server_file=f"runtime/{self.alternate_backend}/llama-server",
                      fallback_server_file="runtime/cpu/llama-server", gpu_layers=17)
        self.write_config(config)
        self.run_setup()
        self.assertEqual(self.read_config(), config)
        self.assertEqual(self.calls, [])
        self.put_model("plamo-3-translate-0.8b-Q4_K_M.gguf", 400)
        self.run_setup(None, "0.8b")
        config["model_file"] = "models/plamo-3-translate-0.8b-Q4_K_M.gguf"
        config["template_file"] = "models/chat_template.0.8b.jinja"
        self.assertEqual(self.read_config(), config)

    def test_explicit_backend_replaces_configured_engine(self):
        self.installed_engines()
        config = self.read_config()
        config.update(server_file=f"runtime/{self.alternate_backend}/llama-server", gpu_layers=17)
        self.write_config(config)
        self.run_setup(self.default_backend)
        self.assertEqual(self.read_config()["server_file"], f"runtime/{self.default_backend}/llama-server")
        self.assertEqual(self.read_config()["gpu_layers"], 0 if self.default_backend == "cpu" else 99)
        self.assertEqual(self.read_config()["fallback_server_file"], "runtime/cpu/llama-server")

    def test_fresh_install_writes_engine(self):
        (self.root / "config.json").unlink()
        self.run_setup()
        self.assertEqual(self.read_config(), {
            "server_file": f"runtime/{self.default_backend}/llama-server",
            "fallback_server_file": "runtime/cpu/llama-server",
            "gpu_layers": 0 if self.default_backend == "cpu" else 99,
        })

    def test_missing_or_unusable_configured_server_is_replaced(self):
        self.installed_engines()
        for server in ["runtime/missing/llama-server", "runtime/cpu", "../runtime/cpu/llama-server",
                       "/runtime/cpu/llama-server", "C:\\runtime\\cpu\\llama-server", "runtime/cpu/../cpu/llama-server", None, 42]:
            with self.subTest(server=server):
                self.write_config({"server_file": server, "gpu_layers": 17})
                self.run_setup()
                self.assertEqual(self.read_config()["server_file"], f"runtime/{self.default_backend}/llama-server")
                self.assertEqual(self.read_config()["gpu_layers"], 0 if self.default_backend == "cpu" else 99)

    def test_missing_defaults_are_filled_without_switching_engine(self):
        self.installed_engines()
        for backend in [self.default_backend, self.alternate_backend]:
            with self.subTest(backend=backend):
                server = f"runtime/{backend}/llama-server".replace("/", chr(92))
                self.write_config({"server_file": server, "gpu_layers": None, "fallback_server_file": "missing"})
                self.run_setup()
                self.assertEqual(self.read_config(), {
                    "server_file": server, "gpu_layers": 0 if backend == "cpu" else 99,
                    "fallback_server_file": "runtime/cpu/llama-server",
                })

    def test_newly_installed_default_backend_updates_engine(self):
        custom = self.root / "custom/llama-server"
        custom.parent.mkdir()
        custom.write_text("fixture")
        self.write_config({"server_file": "custom/llama-server", "gpu_layers": 17})
        self.run_setup()
        self.assertEqual(self.read_config()["server_file"], f"runtime/{self.default_backend}/llama-server")

    def test_shipped_server_missing_at_start_updates_engine(self):
        self.write_config({"server_file": "runtime/cpu/llama-server", "gpu_layers": 17})
        self.run_setup()
        self.assertEqual(self.read_config()["server_file"], f"runtime/{self.default_backend}/llama-server")
        self.assertEqual(self.read_config()["gpu_layers"], 0 if self.default_backend == "cpu" else 99)

    def test_model_choice_is_written_and_kept(self):
        self.put_model("plamo-3-translate-0.8b-Q4_K_M.gguf", 400)
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
        self.put_model("plamo-3-translate-8b-Q4_K_M.gguf", 1024)
        with self.assertRaisesRegex(RuntimeError, "Incomplete file"):
            self.run_setup("cpu", "8b")

    def test_bad_arguments_stop_with_usage_before_doing_anything(self):
        # These exit while parsing the arguments, so nothing is downloaded or written.
        bash = shutil.which("bash")
        if not bash:
            self.skipTest("bash is not available")
        if os.name == "nt" and Path(bash).parent.name.lower() in {"system32", "sysnative"}:
            self.skipTest("WSL bash launcher cannot use native Windows paths")
        base = Path(__file__).resolve().parents[1]
        cases = [
            ("setup.sh", ["--model", "3b"]), ("setup.sh", ["--model"]), ("setup.sh", ["--backend"]),
            ("setup.sh", ["--bogus"]), ("setup.sh", ["--backend", "metal"]),
            ("setup-macos.sh", ["--model", "31b"]), ("setup-macos.sh", ["--backend", "vulkan"]),
        ]
        for script, args in cases:
            result = subprocess.run([bash, (base / script).as_posix(), *args], capture_output=True, text=True, timeout=30)
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
        prog = fixture_program(re.sub(r"'sha256': '[0-9a-fA-F]{64}'", "'sha256': ''", SOURCE), "setup.sh:python", archive_sha256=None)
        with self.assertRaisesRegex(RuntimeError, "Missing SHA-256"):
            self.run_setup(prog=prog)
        self.assertFalse((self.root / "runtime/cpu").exists())

    def test_checksum_mismatch_is_rejected(self):
        self.archive = b"corrupted bytes"
        with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
            self.run_setup()
        self.assertFalse((self.root / "runtime/cpu").exists())

    def test_unsafe_archive_is_rejected(self):
        unsafe = runtime_archive(unsafe=True)
        self.archive = unsafe
        prog = fixture_program(SOURCE, "setup.sh:python", archive_sha256=hashlib.sha256(unsafe).hexdigest())
        with self.assertRaises(tarfile.FilterError):
            self.run_setup(prog=prog)
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
