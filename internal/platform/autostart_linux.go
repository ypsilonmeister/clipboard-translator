package platform

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
	name := "clipboard-translator.desktop"
	if backend, _ := selectLinuxBackend(os.Getenv("DISPLAY"), os.Getenv("XDG_SESSION_TYPE"), os.Getenv("WAYLAND_DISPLAY")); backend == linuxBackendWayland {
		name = waylandAppID + ".desktop"
	}
	return filepath.Join(dir, "autostart", name), nil
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
	for _, candidate := range linuxAutostartCandidates(path) {
		got, err := os.ReadFile(candidate)
		if err == nil && string(got) == want {
			return true
		}
	}
	return false
}

// Recognize our older X11 entry across desktop-session changes, and avoid
// creating two login launchers. Never remove an entry for another executable.
func linuxAutostartCandidates(path string) []string {
	dir := filepath.Dir(path)
	return []string{filepath.Join(dir, "clipboard-translator.desktop"), filepath.Join(dir, waylandAppID+".desktop")}
}

func toggleLinuxAutostart() error {
	path, err := linuxAutostartPath()
	if err != nil {
		return err
	}
	if linuxAutostartEnabled() {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		want, err := linuxDesktopEntry(exe)
		if err != nil {
			return err
		}
		for _, candidate := range linuxAutostartCandidates(path) {
			got, err := os.ReadFile(candidate)
			if err == nil && string(got) == want {
				if err := os.Remove(candidate); err != nil {
					return err
				}
			}
		}
		return nil
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
