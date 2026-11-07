package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func linuxAutostartPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
	}
	return filepath.Join(dir, "autostart", "clipboard-translator.desktop"), nil
}

func linuxDesktopEntry(exe string) (string, error) {
	if !filepath.IsAbs(exe) || strings.ContainsAny(exe, "\r\n\x00") {
		return "", errors.New("the executable must have an absolute single-line path")
	}
	// Exec has both Desktop Entry string escaping and command argument quoting.
	quoted := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "$", `\\$`, "`", "\\\\`", "%", "%%").Replace(exe)
	dir := strings.ReplaceAll(filepath.Dir(exe), `\`, `\\`)
	return "[Desktop Entry]\nType=Application\nName=Clipboard Translator\nExec=\"" + quoted + "\"\nPath=" + dir + "\nTerminal=false\nCategories=Utility;\n", nil
}

func linuxAutostartEnabled() bool {
	path, err := linuxAutostartPath()
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	want, err := linuxDesktopEntry(exe)
	if err != nil {
		return false
	}
	got, err := os.ReadFile(path)
	return err == nil && string(got) == want
}

func toggleLinuxAutostart() error {
	path, err := linuxAutostartPath()
	if err != nil {
		return err
	}
	if linuxAutostartEnabled() {
		return os.Remove(path)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	entry, err := linuxDesktopEntry(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(entry), 0o600)
}
