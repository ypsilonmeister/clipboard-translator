package setup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/config"
)

func TestReadConfigAcceptsUTF8BOM(t *testing.T) {
	root := t.TempDir()
	data := append([]byte{0xef, 0xbb, 0xbf}, []byte(`{"hotkey":"Ctrl+Alt+E"}`)...)
	if err := os.WriteFile(filepath.Join(root, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(root); err != nil {
		t.Fatal("main application rejected fixture:", err)
	}
	cfg, exists, err := readConfig(root)
	if err != nil || !exists || cfg["hotkey"] != "Ctrl+Alt+E" {
		t.Fatalf("setup could not read supported config: exists=%v err=%v", exists, err)
	}
}

func tarFixtureHeaders(t *testing.T, headers []*tar.Header) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		if h.Typeflag == tar.TypeReg {
			h.Size = 7
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.tgz")
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTarRejectsSymlinkWritesAndResolvedEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar runtime extraction is used on Unix; Windows uses zip")
	}
	for _, kind := range []byte{tar.TypeReg, tar.TypeDir, tar.TypeSymlink, tar.TypeLink} {
		t.Run(string([]byte{kind}), func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "out")
			outside := filepath.Join(root, "outside")
			if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			headers := []*tar.Header{
				{Name: "a", Typeflag: tar.TypeDir, Mode: 0755},
				{Name: "real", Typeflag: tar.TypeReg, Mode: 0600},
				{Name: "a/b", Typeflag: tar.TypeSymlink, Linkname: "..", Mode: 0777},
				{Name: "a/b/escape", Typeflag: kind, Linkname: "real", Mode: 0755},
			}
			if err := extractTarGz(tarFixtureHeaders(t, headers), dest); err == nil {
				t.Fatal("accepted writes through a symlink parent")
			}
			if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
				t.Fatal("changed file outside extraction root")
			}
		})
	}
	root := t.TempDir()
	dest := filepath.Join(root, "out")
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	headers := []*tar.Header{
		{Name: "a", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "a/b", Typeflag: tar.TypeSymlink, Linkname: "..", Mode: 0777},
		{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "a/b/../outside", Mode: 0777},
	}
	if err := extractTarGz(tarFixtureHeaders(t, headers), dest); err == nil {
		t.Fatal("accepted a link whose resolved target escapes")
	}
}

func TestTarDoesNotOverwriteSymlinkLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix tar links")
	}
	headers := []*tar.Header{
		{Name: "real", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "alias", Typeflag: tar.TypeSymlink, Linkname: "real", Mode: 0777},
		{Name: "alias", Typeflag: tar.TypeReg, Mode: 0600},
	}
	if err := extractTarGz(tarFixtureHeaders(t, headers), filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("regular member wrote through a symlink")
	}
}

func TestTarPreservesInternalLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix tar links")
	}
	headers := []*tar.Header{
		{Name: "pkg/alias", Typeflag: tar.TypeSymlink, Linkname: "chain", Mode: 0777},
		{Name: "pkg/chain", Typeflag: tar.TypeSymlink, Linkname: "real", Mode: 0777},
		{Name: "pkg/real", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "pkg/hard", Typeflag: tar.TypeLink, Linkname: "pkg/real", Mode: 0600},
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(tarFixtureHeaders(t, headers), dest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"real", "alias", "chain", "hard"} {
		if b, err := os.ReadFile(filepath.Join(dest, "pkg", name)); err != nil || string(b) != "payload" {
			t.Fatalf("internal link %s failed: %v", name, err)
		}
	}
}
