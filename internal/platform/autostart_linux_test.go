package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxAutostart(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
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

func TestLinuxWaylandAutostart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	path, err := linuxAutostartPath()
	if err != nil || filepath.Base(path) != waylandAppID+".desktop" {
		t.Fatal("Wayland autostart must share the portal app id")
	}
	if err := toggleLinuxAutostart(); err != nil {
		t.Fatal(err)
	}
	if !linuxAutostartEnabled() {
		t.Fatal("Wayland autostart was not enabled")
	}
	if err := toggleLinuxAutostart(); err != nil {
		t.Fatal(err)
	}
	if linuxAutostartEnabled() {
		t.Fatal("Wayland autostart was not removed")
	}
}

func TestLinuxAutostartAcrossSessionsPreservesOtherInstallations(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	if err := toggleLinuxAutostart(); err != nil {
		t.Fatal(err)
	}
	old, _ := linuxAutostartPath()
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	if !linuxAutostartEnabled() {
		t.Fatal("old launcher must remain recognized")
	}
	current, _ := linuxAutostartPath()
	foreign, _ := linuxDesktopEntry("/tmp/another-installation/translator")
	if err := os.WriteFile(current, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}
	if err := toggleLinuxAutostart(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("owned old launcher remains")
	}
	if got, err := os.ReadFile(current); err != nil || string(got) != foreign {
		t.Fatal("other installation was changed")
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
