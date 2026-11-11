package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

func TestStartTranslationReportsAbortedCapture(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{platform.ErrCaptureModifiersHeld, "still held down"},
		{platform.ErrCaptureFocusChanged, "focused window changed"},
	} {
		a, tray, cb, _, _ := uxApp(t)
		cb.text = "stale clipboard"
		a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "", false, tc.err }
		a.startTranslation(0)
		if a.translating || len(a.hist.Items()) != 0 {
			t.Fatal("an aborted capture must not start a translation")
		}
		if n := tray.lastNote(); !strings.Contains(n, tc.want) || !strings.Contains(n, "Nothing was translated") {
			t.Fatalf("note = %q, want mention of %q", n, tc.want)
		}
		if cb.text != "stale clipboard" {
			t.Fatalf("the clipboard must be left alone, got %q", cb.text)
		}
	}
}

func TestStartTranslationPassesHotkeyWindowToCapture(t *testing.T) {
	a, _, _, _, fg := uxApp(t)
	*fg = 42
	var got uintptr
	a.capture = func(_ platform.Clipboard, win uintptr) (string, bool, error) {
		got = win
		return "", false, errors.New("stop here")
	}
	a.startTranslation(0)
	if got != 42 {
		t.Fatalf("capture window = %d, want the window in front at the hotkey press", got)
	}
}

// Manual mode has no hotkey, so the failure text must not tell the user to
// press one again.
func TestCaptureFailureNotificationMentionsHotkeyOnlyWithHotkeys(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      platform.DesktopStatus
		wantHotkey bool
	}{
		{"portal workflow", platform.DesktopStatus{Wayland: true, Ready: true}, true},
		{"manual mode", platform.DesktopStatus{Wayland: true, Manual: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, tray, base, _, _ := uxApp(t)
			a.cb = &asyncDesktopClipboard{testClipboard: base, state: tc.state}
			a.translateCaptured(a.conf.Profiles[0], 0, "", false, platform.ErrClipboardNoValue)
			note := tray.lastNote()
			if !strings.Contains(note, "Nothing was translated") {
				t.Fatalf("note = %q", note)
			}
			if got := strings.Contains(note, "press the hotkey again"); got != tc.wantHotkey {
				t.Fatalf("note = %q, mentions hotkey = %v, want %v", note, got, tc.wantHotkey)
			}
		})
	}
}
