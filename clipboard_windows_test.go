//go:build windows

package main

import "testing"

// Round-trips text through the real Windows clipboard, including non-BMP
// characters, and puts the user's previous text back afterwards.
func TestWindowsClipboardRoundTrip(t *testing.T) {
	cb, err := newClipboard()
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
		t.Fatalf("Write: %v", err)
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
