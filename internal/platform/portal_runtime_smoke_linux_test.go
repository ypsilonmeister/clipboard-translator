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

func TestPortalNativeDesktopRuntime(t *testing.T) {
	if os.Getenv("CT_TEST_PORTAL_W4") != "1" {
		t.Skip("requires consent and a disposable Wayland desktop")
	}
	if exec.Command("python3", "-c", "from gi.repository import Gio,GLib").Run() != nil {
		t.Skip("cleanup requires python3-gi")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := PlatformRuntimeError(); err != nil {
		t.Fatal(err)
	}
	if !linuxWayland {
		t.Fatal("requires Wayland")
	}
	id := waylandAppID + ".W4Test"
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	path := filepath.Join(data, "applications", id+".desktop")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("test desktop entry already exists or cannot be checked")
	}
	if err := initializePortalDesktop(t.TempDir(), id); err != nil {
		t.Fatal(err)
	}
	defer func() {
		CloseDesktop()
		for i := 0; i < 20; i++ {
			pumpPortalCallbacks()
			time.Sleep(5 * time.Millisecond)
		}
		cleanupPortalRemoteTest(t, id)
		cleanupPortalShortcutTest(t, id)
		os.Remove(path)
	}()
	cb, err := NewClipboard()
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHotkey("Ctrl+Shift+F9")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	t.Log("Accept shortcuts, then keyboard/clipboard access for W4Test on the designated VM.")
	deadline := time.Now().Add(200 * time.Second)
	for time.Now().Before(deadline) {
		PumpMessages()
		s := cb.(DesktopSession).DesktopStatus()
		if s.Retry {
			t.Fatal(s.Message)
		}
		if s.Ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cb.(DesktopSession).DesktopStatus().Ready {
		t.Fatal("desktop permissions were not granted")
	}
	if h.(interface{ TriggerDescription() string }).TriggerDescription() == "" {
		t.Fatal("actual trigger is missing")
	}
	if ForegroundWindow() != 0 || WaitModifiersReleased(time.Millisecond) || SendPaste() == nil {
		t.Fatal("Wayland focus/autopaste guard is missing")
	}
	const source = "W4 controlled source 日本語\nsecond line"
	const translated = "W4 controlled translation 翻訳\nsecond line"
	if !portalEditorCreate(source) {
		t.Fatal("could not open disposable editor")
	}
	defer portalEditorDestroy()
	t.Log("Click Start in the disposable W3 editor, then press Ctrl+Shift+F9 once and release the modifiers. Keep the editor focused.")
	deadline = time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		PumpMessages()
		if portalEditorReady() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	guard := func() {
		if !portalEditorReady() {
			t.Fatal("fixture lost focus; stopped before more input")
		}
	}
	guard()
	wait := func(action func() error) {
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) {
			guard()
			PumpMessages()
			err := action()
			if err == nil {
				return
			}
			if !errors.Is(err, ErrClipboardPending) {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("clipboard transfer timed out")
	}
	// Never read the desktop's pre-test clipboard contents.
	wait(func() error { return cb.Write("W4 controlled baseline") })
	pressed := false
	deadline = time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		guard()
		PumpMessages()
		pressed, err = h.Pressed()
		if err != nil {
			t.Fatal(err)
		}
		if pressed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pressed {
		t.Fatal("shortcut activation not received")
	}
	task := BeginSelectionCapture(cb, 0)
	if task == nil {
		t.Fatal("Wayland capture task is missing")
	}
	deadline = time.Now().Add(10 * time.Second)
	captured := false
	for time.Now().Before(deadline) {
		guard()
		PumpMessages()
		done, text, fresh, err := task.Step()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			if text != source || fresh {
				t.Fatal("controlled capture mismatch or auto-paste eligibility")
			}
			captured = true
			break
		}
		time.Sleep(task.Wait())
	}
	if !captured {
		t.Fatal("capture timed out")
	}
	wait(func() error {
		ok, err := cb.ReplaceIfUnchanged(source, translated)
		if err == nil && !ok {
			t.Fatal("source changed")
		}
		return err
	})
	keys := activePortalDesktop.remote.beginKeys('v')
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		guard()
		PumpMessages()
		if keys.done && portalEditorText() == translated {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !keys.done || keys.err != nil || portalEditorText() != translated {
		t.Fatal("explicit fixture paste failed")
	}
	t.Log("Production platform API: actual shortcut, selection capture, guarded compare/write and explicit fixture paste passed.")
}
