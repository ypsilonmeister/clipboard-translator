//go:build windows

package platform

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsClipboardSetFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		replace      bool
		restoreFails bool
	}{
		{name: "Write"},
		{name: "ReplaceIfUnchanged", replace: true},
		{name: "WriteRestoreFailure", restoreFails: true},
		{name: "ReplaceIfUnchangedRestoreFailure", replace: true, restoreFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep the owner window and clipboard calls on the same OS thread.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			cb, err := NewClipboard()
			if err != nil {
				t.Fatal(err)
			}
			defer cb.Close()
			saved, err := cb.Read()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := cb.Write(saved); err != nil {
					t.Errorf("restore test clipboard: %v", err)
				}
			}()

			for _, original := range []string{"こんにちは 😀\r\n  ", ""} {
				if err := cb.Write(original); err != nil {
					t.Fatal(err)
				}
				func() {
					// Only replace the Win32 setter: allocation, emptying and reading
					// still use the real clipboard, so this reproduces data loss.
					set := setClipboardData
					defer func() { setClipboardData = set }()
					calls := 0
					setClipboardData = func(args ...uintptr) (uintptr, uintptr, error) {
						calls++
						if calls == 1 {
							if h, _, _ := getClipboardData.Call(cfUnicodeText); h != 0 {
								t.Error("replacement setter called before emptying the clipboard")
							}
						}
						if calls == 1 || tc.restoreFails {
							return 0, 0, syscall.Errno(8) // ERROR_NOT_ENOUGH_MEMORY
						}
						return set(args...)
					}
					if tc.replace {
						var replaced bool
						replaced, err = cb.ReplaceIfUnchanged(original, "replacement")
						if replaced {
							t.Error("failed replacement reported success")
						}
					} else {
						err = cb.Write("replacement")
					}
					if err == nil || !strings.Contains(err.Error(), "SetClipboardData failed") {
						t.Errorf("write error = %v; want SetClipboardData failure", err)
					}
					want := original
					if tc.restoreFails {
						want = ""
						if err == nil || !strings.Contains(err.Error(), "restoring previous clipboard text also failed") {
							t.Errorf("write error = %v; want restoration failure", err)
						}
					}
					if got, err := cb.Read(); err != nil || got != want {
						t.Errorf("after failed write, Read = %q, %v; want %q", got, err, want)
					}
					if calls != 2 {
						t.Errorf("SetClipboardData calls = %d; want failed write then restoration", calls)
					}
				}()
			}
		})
	}
}

// Round-trips text through the real Windows clipboard, including non-BMP
// characters, and puts the user's previous text back afterwards.
func TestWindowsClipboardRoundTrip(t *testing.T) {
	cb, err := NewClipboard()
	if err != nil {
		t.Skipf("clipboard unavailable: %v", err)
	}
	defer cb.Close()
	saved, err := cb.Read()
	if err != nil {
		t.Skipf("clipboard not readable in this session: %v", err)
	}
	defer cb.Write(saved)

	const text = "こんにちは, world 😀"
	if err := cb.Write(text); err != nil {
		t.Skipf("clipboard not writable in this session: %v", err)
	}
	if got, err := cb.Read(); err != nil || got != text {
		t.Fatalf("Read = %q, %v; want %q", got, err, text)
	}
	if ok, err := cb.ReplaceIfUnchanged("something else", "x"); err != nil || ok {
		t.Fatalf("ReplaceIfUnchanged with a stale original = %v, %v; want false, nil", ok, err)
	}
	if ok, err := cb.ReplaceIfUnchanged(text, "Hello"); err != nil || !ok {
		t.Fatalf("ReplaceIfUnchanged = %v, %v; want true, nil", ok, err)
	}
	if got, _ := cb.Read(); got != "Hello" {
		t.Fatalf("after replace, Read = %q; want Hello", got)
	}
}
