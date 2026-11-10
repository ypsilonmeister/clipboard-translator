package setup

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testRunner(tr http.RoundTripper) *runner {
	return &runner{out: io.Discard, client: &http.Client{Transport: tr}}
}

func TestDownloadVerifiesAndRenames(t *testing.T) {
	body := []byte("payload")
	tr := &stubTransport{handler: func(url string) ([]byte, int) { return body, 200 }}
	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := testRunner(tr).download("https://example.invalid/file.bin", dest, sha256Hex(body)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dest); err != nil || !bytes.Equal(b, body) {
		t.Errorf("dest = %q, %v", b, err)
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Error("the partial file was left behind")
	}
}

func TestDownloadHashMismatchLeavesNothing(t *testing.T) {
	tr := &stubTransport{handler: func(url string) ([]byte, int) { return []byte("wrong"), 200 }}
	dest := filepath.Join(t.TempDir(), "file.bin")
	err := testRunner(tr).download("https://example.invalid/file.bin", dest, sha256Hex([]byte("right")))
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a file with a bad hash got its final name")
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Error("the partial file was left behind")
	}
}

func TestDownloadEmptyAndHTTPError(t *testing.T) {
	dir := t.TempDir()
	empty := &stubTransport{handler: func(url string) ([]byte, int) { return nil, 200 }}
	if err := testRunner(empty).download("https://example.invalid/x", filepath.Join(dir, "x"), ""); err == nil {
		t.Error("an empty download was accepted")
	}
	failing := &stubTransport{handler: func(url string) ([]byte, int) { return nil, 500 }}
	if err := testRunner(failing).download("https://example.invalid/x", filepath.Join(dir, "x"), ""); err == nil {
		t.Error("an HTTP 500 was accepted")
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := extractZip(archive, dest); err == nil {
		t.Fatal("a zip with a ../ member was extracted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); !os.IsNotExist(err) {
		t.Error("the zip member escaped the destination")
	}
}

func TestExtractTarRejectsLinkEscape(t *testing.T) {
	archive := buildTarWithLink(t)
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(path, dest); err == nil {
		t.Fatal("a tar with a link pointing outside was extracted")
	}
}

func buildTarWithLink(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("x")
	if err := tw.WriteHeader(&tar.Header{Name: "llama-fixture/llama-server", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "llama-fixture/evil.so", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../../outside"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLlamaPinsComplete mirrors the old cross-script pin test: every
// platform/backend the binary can install has a pinned file whose name
// matches the llama.cpp release layout, with a full SHA-256.
func TestLlamaPinsComplete(t *testing.T) {
	if !regexp.MustCompile(`^b[0-9]+$`).MatchString(llamaTag) {
		t.Errorf("llamaTag %q is not a llama.cpp release tag", llamaTag)
	}
	want := map[string]*regexp.Regexp{
		"win/cpu":      regexp.MustCompile(`^llama-` + llamaTag + `-bin-win-cpu-x64\.zip$`),
		"win/cuda":     regexp.MustCompile(`^llama-` + llamaTag + `-bin-win-cuda-12\.4-x64\.zip$`),
		"win/cudart":   regexp.MustCompile(`^cudart-llama.*-bin-win-cuda-12\.4-x64\.zip$`),
		"win/vulkan":   regexp.MustCompile(`^llama-` + llamaTag + `-bin-win-vulkan-x64\.zip$`),
		"linux/cpu":    regexp.MustCompile(`^llama-` + llamaTag + `-bin-ubuntu-x64\.tar\.gz$`),
		"linux/vulkan": regexp.MustCompile(`^llama-` + llamaTag + `-bin-ubuntu-vulkan-x64\.tar\.gz$`),
		"darwin/arm64": regexp.MustCompile(`^llama-` + llamaTag + `-bin-macos-arm64\.tar\.gz$`),
		"darwin/x64":   regexp.MustCompile(`^llama-` + llamaTag + `-bin-macos-x64\.tar\.gz$`),
	}
	if len(llamaAssets) != len(want) {
		t.Errorf("llamaAssets has %d entries, want %d", len(llamaAssets), len(want))
	}
	for key, pattern := range want {
		a, err := llamaAssetFor(key)
		if err != nil {
			t.Errorf("no pinned asset for %s: %v", key, err)
			continue
		}
		if !pattern.MatchString(a.File) {
			t.Errorf("%s: file %q does not match %s", key, a.File, pattern)
		}
		if !sha256Pattern.MatchString(a.Sha256) {
			t.Errorf("%s: sha256 %q is not a full digest", key, a.Sha256)
		}
		if got := a.url(); !strings.HasPrefix(got, "https://github.com/ggml-org/llama.cpp/releases/download/"+llamaTag+"/") {
			t.Errorf("%s: url %q", key, got)
		}
	}
}
