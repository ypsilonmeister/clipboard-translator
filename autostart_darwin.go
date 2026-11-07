package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const macAgentLabel = "com.github.ypsilonmeister.clipboard-translator"

func macAutostartPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", macAgentLabel+".plist"), nil
}
func macLaunchAgent(exe string) (string, error) {
	if !filepath.IsAbs(exe) || strings.ContainsRune(exe, 0) {
		return "", errors.New("LaunchAgent executable must be an absolute path")
	}
	var value bytes.Buffer
	if err := xml.EscapeText(&value, []byte(exe)); err != nil {
		return "", err
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + macAgentLabel + `</string>
<key>ProgramArguments</key><array><string>` + value.String() + `</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>
`, nil
}
func macAutostartEnabled() bool {
	path, err := macAutostartPath()
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	want, err := macLaunchAgent(exe)
	if err != nil {
		return false
	}
	got, err := os.ReadFile(path)
	return err == nil && string(got) == want
}
func toggleMacAutostart() error {
	path, err := macAutostartPath()
	if err != nil {
		return err
	}
	if macAutostartEnabled() {
		return os.Remove(path)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	content, err := macLaunchAgent(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
