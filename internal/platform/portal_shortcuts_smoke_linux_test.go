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

// Requires the maintainer to accept a shortcut consent dialog on a designated VM.
// It never reads/writes clipboard text or sends keyboard input.
func TestPortalNativeShortcutBindings(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_W2") != "1" {
		t.Skip("requires a designated, unlocked desktop and user consent")
	}
	for _, tool := range []string{"python3", "gsettings", "dconf", "gdbus"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("native GNOME test cleanup requires " + tool)
		}
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
	dir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	id := waylandAppID + ".W2Test"
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
	w, err := openGIOPortal()
	if err != nil {
		t.Fatal(err)
	}
	p := startPortalClient(w, id, time.Now)
	defer func() {
		p.Close()
		for i := 0; i < 20; i++ {
			pumpPortalCallbacks()
			time.Sleep(5 * time.Millisecond)
		}
		cleanupPortalShortcutTest(t, id)
	}()
	r := newPortalHotkeys(p, w)
	a, err := r.NewHotkey("Ctrl+Shift+F9")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.NewHotkey("Ctrl+Shift+F10")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Accept the shortcut dialog for Clipboard Translator W2Test on the designated VM.")
	deadline := time.Now().Add(200 * time.Second)
	for time.Now().Before(deadline) && (a.actual == "" || b.actual == "") {
		r.Step()
		if p.err != nil {
			t.Fatal(p.err)
		}
		if r.err != nil {
			t.Fatal(r.err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.actual == "" || b.actual == "" {
		t.Fatal("both shortcuts must be accepted")
	}
	t.Logf("Registered actual shortcuts: %s ; %s", a.actual, b.actual)
	if os.Getenv("CT_TEST_PORTAL_PRESS") == "1" {
		t.Log("Press each registered shortcut once on the VM.")
		seenA, seenB := false, false
		deadline = time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) && (!seenA || !seenB) {
			x, err := a.Pressed()
			if err != nil {
				t.Fatal(err)
			}
			seenA = seenA || x
			x, err = b.Pressed()
			if err != nil {
				t.Fatal(err)
			}
			seenB = seenB || x
			time.Sleep(10 * time.Millisecond)
		}
		if !seenA || !seenB {
			t.Fatalf("activation missing: profile1=%v profile2=%v", seenA, seenB)
		}
	}
}

// GNOME remembers a test app's bindings after the D-Bus session closes.
// Remove only our test id, preserving all other apps even if the list changed.
func cleanupPortalShortcutTest(t *testing.T, id string) {
	t.Helper()
	script := `import ast,subprocess,sys
app=sys.argv[1]
assert app=='io.github.ypsilonmeister.ClipboardTranslator.W2Test'
schema='org.gnome.settings-daemon.global-shortcuts'
raw=subprocess.check_output(['gsettings','get',schema,'applications'],text=True).strip()
if raw.startswith('@as '): raw=raw[4:]
apps=ast.literal_eval(raw)
assert isinstance(apps,list) and all(isinstance(x,str) for x in apps)
if app in apps:
 subprocess.run(['gsettings','set',schema,'applications',repr([x for x in apps if x!=app])],check=True)
subprocess.run(['dconf','reset','-f','/org/gnome/settings-daemon/global-shortcuts/'+app+'/'],check=True)
store=['gdbus','call','--session','--dest','org.freedesktop.impl.portal.PermissionStore','--object-path','/org/freedesktop/impl/portal/PermissionStore','--method','org.freedesktop.impl.portal.PermissionStore.']
listed=subprocess.check_output(store[:-1]+[store[-1]+'List','global-shortcuts'],text=True)
if repr(app) in listed:
 subprocess.run(store[:-1]+[store[-1]+'Delete','global-shortcuts',app],check=True,stdout=subprocess.DEVNULL)
`
	if out, err := exec.Command("python3", "-c", script, id).CombinedOutput(); err != nil {
		t.Errorf("could not remove test shortcut state: %v: %s", err, out)
	}
}
