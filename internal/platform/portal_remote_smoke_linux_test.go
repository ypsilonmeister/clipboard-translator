//go:build linux && cgo && portaltest

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Explicitly opt in only on the maintainer's disposable Wayland test desktop.
func TestPortalNativeRemoteClipboard(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_W3") != "1" {
		t.Skip("requires consent and a disposable Wayland desktop")
	}
	if err := exec.Command("python3", "-c", "from gi.repository import Gio, GLib").Run(); err != nil {
		t.Skip("GNOME permission cleanup needs python3-gi")
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
	id := waylandAppID + ".W3Test"
	dir := filepath.Join(dataHome, "applications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".desktop")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("test desktop entry already exists or is not writable")
	}
	defer os.Remove(path)
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
	defer cleanupPortalRemoteTest(t, id)
	store, err := newPortalTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	connect := func() (*portalClient, *portalRemote, *gioPortalWire) {
		w, err := openGIOPortal()
		if err != nil {
			t.Fatal(err)
		}
		p := startPortalClient(w, id, time.Now)
		p.tokens = store
		r := newPortalRemote(p, w)
		r.Authorize()
		return p, r, w
	}
	p, r, _ := connect()
	defer func() {
		r.Close()
		p.Close()
		for i := 0; i < 20; i++ {
			pumpPortalCallbacks()
			time.Sleep(5 * time.Millisecond)
		}
	}()
	t.Log("Accept keyboard/clipboard control for Clipboard Translator W3Test on the VM.")
	deadline := time.Now().Add(200 * time.Second)
	for time.Now().Before(deadline) && r.phase != "ready" {
		r.Step()
		if r.err != nil {
			t.Fatal(r.err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.phase != "ready" {
		t.Fatal("remote permission was not granted")
	}
	source := "W3 source 日本語\nsecond line"
	translated := "W3 translated 翻訳結果\nsecond line"
	if !portalEditorCreate(source) {
		t.Fatal("could not create the disposable Wayland editor")
	}
	defer portalEditorDestroy()
	t.Log("Click Start clipboard test in the disposable W3 window; keep that window focused until the test completes.")
	deadline = time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) && !portalEditorReady() {
		r.Step()
		time.Sleep(10 * time.Millisecond)
	}
	guard := func() {
		if !portalEditorReady() {
			t.Fatal("test window lost focus; stopped before sending more input")
		}
	}
	guard()
	if !portalEditorSelection() {
		t.Fatal("test fixture selection or text focus was lost")
	}
	wait := func(action func() error) {
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) {
			guard()
			err := action()
			if err == nil {
				return
			}
			if !errors.Is(err, ErrClipboardPending) {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("clipboard operation timed out")
	}
	// Establish known clipboard text before reading: never collect the VM's previous clipboard.
	wait(func() error { return r.clip.Write("W3 controlled baseline") })
	capture := beginPortalCapture(r, func() bool { return true })
	deadline = time.Now().Add(10 * time.Second)
	captured := false
	for time.Now().Before(deadline) {
		guard()
		done, text, fresh, err := capture.Step()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			if text != source || fresh {
				t.Fatalf("copy/selection capture mismatch: selected=%v baseline=%v length=%d fresh=%v phase=%s seq_before=%d seq_now=%d", portalEditorSelection(), text == "W3 controlled baseline", len(text), fresh, capture.(*portalCapture).phase, capture.(*portalCapture).before, r.clip.seq)
			}
			captured = true
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !captured {
		t.Fatal("selection capture timed out")
	}
	t.Log("Copied the controlled UTF-8 selection; automatic paste remains disabled.")
	wait(func() error {
		ok, err := r.clip.ReplaceIfUnchanged(source, translated)
		if err == nil && !ok {
			t.Fatal("source clipboard unexpectedly changed")
		}
		return err
	})
	// The external GTK paste consumer requests the offered content through Unix FDs.
	transfers, writes, writeFailures := 0, 0, 0
	previous := p.onEvent
	p.onEvent = func(e portalEvent) bool {
		if e.kind == "selection-transfer" && e.session == r.session {
			transfers++
		}
		if e.kind == "written" && e.session == r.session {
			writes++
			if e.code != 0 {
				writeFailures++
			}
		}
		return previous != nil && previous(e)
	}
	guard()
	keys := r.beginKeys('v')
	deadline = time.Now().Add(5 * time.Second)
	for !keys.done && time.Now().Before(deadline) {
		guard()
		r.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if !keys.done || keys.err != nil {
		t.Fatal("explicit test paste failed")
	}
	deadline = time.Now().Add(3 * time.Second)
	for portalEditorText() != translated && time.Now().Before(deadline) {
		guard()
		r.Step()
		time.Sleep(10 * time.Millisecond)
	}
	if portalEditorText() != translated || transfers == 0 || writes == 0 || writeFailures != 0 {
		t.Fatalf("explicit FD paste mismatch: matched=%v transfers=%d writes=%d failures=%d", portalEditorText() == translated, transfers, writes, writeFailures)
	}
	p.onEvent = previous
	t.Log("UTF-8 FD transfer and explicit paste into the disposable editor succeeded.")
	r.Close()
	p.Close()
	portalEditorDestroy()
	for i := 0; i < 20; i++ {
		pumpPortalCallbacks()
		time.Sleep(5 * time.Millisecond)
	}
	if token, err := store.Load(); err != nil || token == "" {
		t.Fatal("persistent permission token was not saved")
	}
	p2, r2, _ := connect()
	defer p2.Close()
	defer r2.Close()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && r2.phase != "ready" {
		r2.Step()
		if r2.err != nil {
			t.Fatal(r2.err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r2.phase != "ready" {
		t.Fatal("permission token did not restore access without another prompt")
	}
	t.Log("Reconnected with the saved single-use permission token.")
}

func cleanupPortalRemoteTest(t *testing.T, id string) {
	t.Helper()
	script := `try:
 import sys
 from gi.repository import Gio,GLib
 app=sys.argv[1]
 assert app in ['io.github.ypsilonmeister.ClipboardTranslator.W3Test','io.github.ypsilonmeister.ClipboardTranslator.W4Test']
 bus=Gio.bus_get_sync(Gio.BusType.SESSION,None)
 def call(method,args):
  return bus.call_sync('org.freedesktop.impl.portal.PermissionStore','/org/freedesktop/impl/portal/PermissionStore','org.freedesktop.impl.portal.PermissionStore',method,args,None,Gio.DBusCallFlags.NONE,3000,None)
 for table in ['remote-desktop','screencast','devices']:
  try: ids=call('List',GLib.Variant('(s)',(table,))).unpack()[0]
  except GLib.Error: continue
  for ident in ids:
   permissions,_=call('Lookup',GLib.Variant('(ss)',(table,ident))).unpack()
   if app not in permissions: continue
   if len(permissions)==1: call('Delete',GLib.Variant('(ss)',(table,ident)))
   else: call('DeletePermission',GLib.Variant('(sss)',(table,ident,app)))
except Exception:
 raise SystemExit("test permission cleanup failed")
`
	if out, err := exec.Command("python3", "-c", script, id).CombinedOutput(); err != nil {
		t.Errorf("could not remove test permission state: %v: %s", err, out)
	}
}
