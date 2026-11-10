//go:build linux && cgo && portaltest

package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// No BindShortcuts, SelectDevices or Start: no consent dialogs, keyboard input or clipboard access.
func TestPortalNativeSessions(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_W1") != "1" {
		t.Skip("requires an explicitly designated graphical test session")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	id := waylandAppID + ".W1Test"
	dir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, id+".desktop")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("test desktop entry already exists or is not writable")
	}
	defer os.Remove(file)
	exe, err := os.Executable()
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	entry, err := linuxDesktopEntry(exe)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	_, err = f.WriteString(entry + "NoDisplay=true\n")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("could not write test desktop entry")
	}
	for i := 0; i < 2; i++ {
		w, err := openGIOPortal()
		if err != nil {
			t.Fatal(err)
		}
		p := startPortalClient(w, id, time.Now)
		deadline := time.Now().Add(20 * time.Second)
		for p.phase != "ready" && p.phase != "failed" && time.Now().Before(deadline) {
			p.Step()
			time.Sleep(5 * time.Millisecond)
		}
		phase, clientErr := p.phase, p.err
		p.Close()
		// Deliver canceled callbacks before the next connection / test exits.
		for j := 0; j < 20; j++ {
			pumpPortalCallbacks()
			time.Sleep(5 * time.Millisecond)
		}
		if phase != "ready" {
			t.Fatalf("session lifecycle: phase=%s err=%v", phase, clientErr)
		}
	}
}

func TestPortalNativeCancelPendingRegister(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_W1") != "1" {
		t.Skip("requires designated session bus")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := openGIOPortal()
	if err != nil {
		t.Fatal(err)
	}
	p := startPortalClient(w, waylandAppID+".W1Test", time.Now)
	p.Close()
	p.Close()
	for i := 0; i < 20; i++ {
		pumpPortalCallbacks()
		time.Sleep(5 * time.Millisecond)
	}
	if p.phase != "closed" || w.native != nil {
		t.Fatal("pending connection was not closed")
	}
}
