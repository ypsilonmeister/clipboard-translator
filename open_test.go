package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenConfigFile_OpenerSucceeds(t *testing.T) {
	a, tray, _ := newTestApp(t)
	opened := ""
	a.openURL = func(target string) error {
		opened = target
		return nil
	}
	a.handleCommand(cmdOpenConfig)
	wantPath, _ := filepath.Abs(filepath.Join(a.dir, "config.json"))
	if opened != wantPath {
		t.Errorf("openURL called with %q, want %q", opened, wantPath)
	}
	if tray.lastNote() != "" || tray.lastError() {
		t.Errorf("unexpected notification: %q (error=%v)", tray.lastNote(), tray.lastError())
	}
}

func TestOpenConfigFile_OpenerFails(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.openURL = func(target string) error {
		return errors.New("file association error")
	}
	fallbackTried := false
	a.fallbackEditor = func(path string) error {
		fallbackTried = true
		return errors.New("fallback failed")
	}
	a.handleCommand(cmdOpenConfig)
	if !fallbackTried {
		t.Error("expected fallback editor to be tried")
	}
	if !tray.lastError() {
		t.Error("expected error notification")
	}
	wantPath, _ := filepath.Abs(filepath.Join(a.dir, "config.json"))
	note := tray.lastNote()
	if !strings.Contains(note, wantPath) {
		t.Errorf("notification %q does not contain full path %q", note, wantPath)
	}
	if !strings.Contains(note, "text editor") || !strings.Contains(note, "edit the hotkey") || !strings.Contains(note, "restart the app") {
		t.Errorf("notification %q missing manual recovery instructions", note)
	}
}

func TestOpenConfigFile_FallbackSucceeds(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.openURL = func(target string) error {
		return errors.New("file association error")
	}
	fallbackTried := false
	a.fallbackEditor = func(path string) error {
		fallbackTried = true
		return nil
	}
	a.handleCommand(cmdOpenConfig)
	if !fallbackTried {
		t.Error("expected fallback editor to be tried")
	}
	if tray.lastNote() != "" || tray.lastError() {
		t.Errorf("unexpected notification when fallback succeeds: %q (error=%v)", tray.lastNote(), tray.lastError())
	}
}

func TestOpenLicense_Succeeds(t *testing.T) {
	a, tray, _ := newTestApp(t)
	opened := ""
	a.openURL = func(target string) error {
		opened = target
		return nil
	}
	a.handleCommand(cmdPlamoLicense)
	if opened != plamoLicenseURL {
		t.Errorf("openURL called with %q, want %q", opened, plamoLicenseURL)
	}
	if tray.lastNote() != "" || tray.lastError() {
		t.Errorf("unexpected notification: %q (error=%v)", tray.lastNote(), tray.lastError())
	}
}

func TestOpenLicense_Fails(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.openURL = func(target string) error {
		return errors.New("browser failed")
	}
	a.handleCommand(cmdPlamoLicense)
	if !tray.lastError() {
		t.Error("expected error notification")
	}
	if !strings.Contains(tray.lastNote(), plamoLicenseURL) {
		t.Errorf("notification %q does not contain license URL %q", tray.lastNote(), plamoLicenseURL)
	}
}

func TestOpenReleasesPage_Succeeds(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.latest = releaseInfo{Tag: "v9.9.9"}
	a.askYesNo = func(msg string) bool { return true }
	opened := ""
	a.openURL = func(target string) error {
		opened = target
		return nil
	}
	a.startUpdate()
	if opened != releasesPage {
		t.Errorf("openURL called with %q, want %q", opened, releasesPage)
	}
	if tray.lastNote() != "" || tray.lastError() {
		t.Errorf("unexpected notification: %q (error=%v)", tray.lastNote(), tray.lastError())
	}
}

func TestOpenReleasesPage_Fails(t *testing.T) {
	a, tray, _ := newTestApp(t)
	a.latest = releaseInfo{Tag: "v9.9.9"}
	a.askYesNo = func(msg string) bool { return true }
	a.openURL = func(target string) error {
		return errors.New("browser failed")
	}
	a.startUpdate()
	if !tray.lastError() {
		t.Error("expected error notification")
	}
	if !strings.Contains(tray.lastNote(), releasesPage) {
		t.Errorf("notification %q does not contain releases page URL %q", tray.lastNote(), releasesPage)
	}
}
