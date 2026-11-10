package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxAutostart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if linuxAutostartEnabled() {
		t.Fatal("unexpected existing entry")
	}
	if err := toggleLinuxAutostart(); err != nil {
		t.Fatal(err)
	}
	if !linuxAutostartEnabled() {
		t.Fatal("entry does not point at this executable")
	}
	b, err := os.ReadFile(filepath.Join(dir, "autostart", "clipboard-translator.desktop"))
	if err != nil || !strings.Contains(string(b), "Terminal=false") {
		t.Fatalf("%s %v", b, err)
	}
	if err := toggleLinuxAutostart(); err != nil {
		t.Fatal(err)
	}
	if linuxAutostartEnabled() {
		t.Fatal("entry not removed")
	}
}

func TestLinuxDesktopEntryQuoting(t *testing.T) {
	entry, err := linuxDesktopEntry("/tmp/space and $quote\"percent%/clipboard-translator")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry, `Exec="/tmp/space and \\$quote\\"percent%%/clipboard-translator"`) {
		t.Fatalf("incorrect quoting:\n%s", entry)
	}
	for _, invalid := range []string{"relative/path", "/tmp/line\nbreak", "/tmp/carriage\rreturn"} {
		if _, err := linuxDesktopEntry(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := linuxAutostartPath(); err == nil {
		t.Fatal("accepted relative config directory")
	}
}
