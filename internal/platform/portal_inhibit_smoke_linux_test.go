//go:build linux && cgo && portaltest

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// #133 P1-2 on a designated GNOME VM whose screen shield is up (design memo
// §0.2/§6): RemoteDesktop.CreateSession is inhibited, so the remote feature
// must report "waiting" (not "failed"), learn the shield state from
// org.gnome.ScreenSaver, and reconnect as soon as ActiveChanged(false) arrives,
// well before its backoff would. The shield is toggled with
// org.gnome.ScreenSaver.SetActive and restored to "active" afterwards.
//
// No consent dialog can appear: the test stops as soon as CreateSession
// succeeds, before Start (the only step that shows the RemoteDesktop dialog),
// and closes the session.
func TestPortalNativeRemoteWaitsForShield(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_P12") != "1" {
		t.Skip("requires a designated GNOME VM with the screen shield up")
	}
	for _, tool := range []string{"python3", "gdbus"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("native GNOME test requires " + tool)
		}
	}
	if !portalScreenShieldActive(t) {
		t.Skip("the GNOME screen shield is not active; this check needs RemoteDesktop to be inhibited")
	}
	// Whatever happens, leave the shield as it was found.
	defer func() {
		setPortalScreenShield(t, true)
		// SetActive(true) returns before the shield finishes coming up.
		for i := 0; i < 50 && !portalScreenShieldActive(t); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if !portalScreenShieldActive(t) {
			t.Error("could not restore the screen shield to active")
		}
	}()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	id := waylandAppID + ".P12Test"
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if err := installPortalDesktopEntryForID(dataHome, mustExecutable(t), id); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(dataHome, "applications", id+".desktop"))
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
	remote := newPortalRemote(p, w)
	defer func() {
		remote.Close()
		p.Close()
		for i := 0; i < 40; i++ {
			pumpPortalCallbacks()
			time.Sleep(5 * time.Millisecond)
		}
		cleanupPortalRemoteTest(t, id)
	}()
	step := func() {
		remote.Step()
		if remote.operation == "clipboard" || remote.operation == "start" {
			t.Fatal("reached a consent step; stopping before Start")
		}
		if p.err != nil {
			t.Fatalf("portal connection failed: %v", p.err)
		}
	}
	// 1. Shield up: inhibited, waiting, retried on the backoff (1 s, then 5 s).
	authorized := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !(remote.phase == "waiting" && remote.attempt >= 2) {
		if p.phase == "ready" && !authorized {
			remote.Authorize()
			authorized = true
		}
		step()
		if remote.operation == "select" {
			t.Fatal("RemoteDesktop.CreateSession was not inhibited under the shield")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("shield up: phase=%s shield=%d attempt=%d err=%v status=%q", remote.phase, remote.shield, remote.attempt, remote.err, remote.WaitMessage())
	if remote.phase != "waiting" || remote.err != nil || remote.shield != shieldActive || remote.attempt < 2 {
		t.Fatal("an inhibited CreateSession under the shield must wait, not fail")
	}
	// 2. Shield lifts: the reconnect must follow ActiveChanged(false), not the
	// 5 s backoff step that is now pending.
	backoffDue := remote.retryAt
	lifted := time.Now()
	setPortalScreenShield(t, false)
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && remote.session == "" {
		step()
		time.Sleep(10 * time.Millisecond)
	}
	took := time.Since(lifted)
	t.Logf("shield down: phase=%s operation=%q shield=%d session=%v after %v (backoff was due in %v)", remote.phase, remote.operation, remote.shield, remote.session != "", took.Round(time.Millisecond), backoffDue.Sub(lifted).Round(time.Millisecond))
	if remote.shield != shieldInactive {
		t.Fatal("ActiveChanged(false) was not received")
	}
	if remote.session == "" || remote.operation != "select" {
		t.Fatal("the remote feature did not reconnect after the shield lifted")
	}
	if !lifted.Add(took).Before(backoffDue) {
		t.Fatal("the reconnect waited for the backoff instead of following ActiveChanged(false)")
	}
	// The deferred Close drops the SelectDevices request and the session before Start.
}

func setPortalScreenShield(t *testing.T, active bool) {
	t.Helper()
	value := "false"
	if active {
		value = "true"
	}
	out, err := exec.Command("gdbus", "call", "--session", "--dest", "org.gnome.ScreenSaver", "--object-path", "/org/gnome/ScreenSaver", "--method", "org.gnome.ScreenSaver.SetActive", value).CombinedOutput()
	if err != nil {
		t.Errorf("org.gnome.ScreenSaver.SetActive %s failed: %v: %s", value, err, out)
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}
