package platform

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacLaunchAgent(t *testing.T) {
	entry, err := macLaunchAgent(`/Applications/Quote " & Name.app/Contents/MacOS/clipboard-translator`)
	if err != nil || !strings.Contains(entry, "&amp;") {
		t.Fatalf("%s %v", entry, err)
	}
	var parsed any
	if err := xml.Unmarshal([]byte(entry), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, err := macLaunchAgent("relative/path"); err == nil {
		t.Fatal("accepted a relative executable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if macAutostartEnabled() {
		t.Fatal("unexpected launch agent")
	}
	if err := toggleMacAutostart(); err != nil {
		t.Fatal(err)
	}
	if !macAutostartEnabled() {
		t.Fatal("launch agent does not point at this executable")
	}
	path := filepath.Join(home, "Library", "LaunchAgents", macAgentLabel+".plist")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := toggleMacAutostart(); err != nil {
		t.Fatal(err)
	}
	if macAutostartEnabled() {
		t.Fatal("launch agent not removed")
	}
}
func TestMacWritableDataFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := WritableAppDir("/Applications/Clipboard Translator.app/Contents/MacOS")
	want := filepath.Join(home, "Library", "Application Support", "ClipboardTranslator")
	if err != nil || dir != want {
		t.Fatalf("%q %v", dir, err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("data folder: %v", err)
	}
	if PasteShortcut() != "Cmd+V" {
		t.Fatal("macOS paste shortcut must be Cmd+V")
	}
}
