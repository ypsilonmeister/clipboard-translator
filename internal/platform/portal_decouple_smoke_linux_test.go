//go:build linux && cgo && portaltest

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// #133 P1-1 on a designated GNOME VM whose screen shield is up: GNOME refuses
// RemoteDesktop.CreateSession (Response 2) while the shield is up. The shortcut
// feature shares the connection but must keep its own session and its
// BindShortcuts request. The test runs only while the shield is active, so the
// RemoteDesktop path can never reach SelectDevices or show a dialog. The
// shortcut consent dialog is never accepted here: a not-yet-granted app id
// leaves BindShortcuts pending, and the test then closes it and removes the
// test id's shortcut and permission state.
func TestPortalNativeRemoteInhibitKeepsShortcuts(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_P11") != "1" {
		t.Skip("requires a designated GNOME VM with the screen shield up")
	}
	for _, tool := range []string{"python3", "gsettings", "dconf", "gdbus"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("native GNOME test cleanup requires " + tool)
		}
	}
	if !portalScreenShieldActive(t) {
		t.Skip("the GNOME screen shield is not active; this check needs RemoteDesktop to be inhibited")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	id := waylandAppID + ".P11Test"
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, id+".desktop")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
		t.Fatal("test desktop entry write failed")
	}
	tokens, err := newPortalTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := openGIOPortal()
	if err != nil {
		t.Fatal(err)
	}
	p := startPortalClient(w, id, time.Now)
	p.tokens = tokens
	hotkeys := newPortalHotkeys(p, w)
	remote := newPortalRemote(p, w)
	defer func() {
		if hotkeys.pending != "" {
			w.CloseRequest(hotkeys.pending)
		}
		remote.Close()
		p.Close()
		for i := 0; i < 40; i++ {
			pumpPortalCallbacks()
			time.Sleep(5 * time.Millisecond)
		}
		cleanupPortalShortcutTest(t, id)
		cleanupPortalRemoteTest(t, id)
	}()
	h, err := hotkeys.NewHotkey("Ctrl+Shift+F9")
	if err != nil {
		t.Fatal(err)
	}
	authorized := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		hotkeys.Step()
		// Start both features together, as the app did before P1-1, so the
		// inhibited RemoteDesktop reply races the shortcut session.
		if p.phase == "ready" && !authorized {
			remote.Authorize()
			authorized = true
		}
		remote.Step()
		if remote.operation == "select" || remote.operation == "clipboard" || remote.operation == "start" {
			t.Fatal("RemoteDesktop was not inhibited; stopping before any consent dialog")
		}
		if p.err != nil {
			t.Fatalf("portal connection failed: %v", p.err)
		}
		// Since #133 P1-2 an inhibited create under a known-active shield
		// parks the remote feature in "waiting" instead of failing it.
		if remote.phase == "waiting" && hotkeys.session != "" && (hotkeys.operation == "bind" || h.active || hotkeys.err != nil) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("remote: phase=%s err=%v", remote.phase, remote.err)
	t.Logf("shortcuts: session=%q operation=%q err=%v active=%v trigger=%q", hotkeys.session, hotkeys.operation, hotkeys.err, h.active, h.actual)
	if remote.phase != "waiting" || remote.err != nil || remote.shield != shieldActive {
		t.Fatal("RemoteDesktop.CreateSession was expected to be inhibited (waiting) while the shield is up")
	}
	if p.phase != "ready" || p.err != nil {
		t.Fatalf("RemoteDesktop refusal reached the shared connection: phase=%s err=%v", p.phase, p.err)
	}
	if hotkeys.session == "" || hotkeys.err != nil {
		t.Fatalf("RemoteDesktop refusal took down the shortcut session: session=%q err=%v", hotkeys.session, hotkeys.err)
	}
	if hotkeys.operation != "bind" && !h.active {
		t.Fatalf("BindShortcuts was not sent on the shortcut session: operation=%q", hotkeys.operation)
	}
	// Keep pumping: the shortcut session must survive the refusal, not just
	// outlive it by one tick. A granted app id would complete the bind here.
	settle := time.Now().Add(5 * time.Second)
	for time.Now().Before(settle) && !h.active {
		hotkeys.Step()
		remote.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if hotkeys.session == "" || hotkeys.err != nil || p.err != nil {
		t.Fatalf("shortcut session lost after the refusal: session=%q err=%v client=%v", hotkeys.session, hotkeys.err, p.err)
	}
	if h.active {
		t.Logf("BindShortcuts completed: %s", h.actual)
	} else {
		t.Log("BindShortcuts is pending on the live shortcut session (consent dialog not accepted; no prior grant for this test id)")
	}
}

func portalScreenShieldActive(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("gdbus", "call", "--session", "--dest", "org.gnome.ScreenSaver", "--object-path", "/org/gnome/ScreenSaver", "--method", "org.gnome.ScreenSaver.GetActive").CombinedOutput()
	if err != nil {
		t.Skipf("org.gnome.ScreenSaver unavailable: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out)) == "(true,)"
}
