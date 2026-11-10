package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
	"github.com/ypsilonmeister/clipboard-translator/internal/testsupport"
)

// chatStatus posts a minimal chat request with the given Authorization header
// (none when empty) and returns the HTTP status.
func chatStatus(t *testing.T, endpoint, auth string) int {
	t.Helper()
	req, err := http.NewRequest("POST", endpoint+"/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// The server only answers requests with this start's key; /health stays open
// so waitReady works without it.
func TestServerRequiresThePerStartKey(t *testing.T) {
	a, _, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	first := a.sup.APIKey()
	if len(first) != 64 {
		t.Fatalf("a running server must have a key, got %d characters", len(first))
	}
	for _, arg := range a.sup.Args() {
		if strings.Contains(arg, first) {
			t.Fatal("the key must not be on the command line")
		}
	}
	if !healthy(a) {
		t.Fatal("/health must answer without the key")
	}
	if s := chatStatus(t, a.endpoint, ""); s != http.StatusUnauthorized {
		t.Errorf("no key: status %d, want 401", s)
	}
	if s := chatStatus(t, a.endpoint, "Bearer wrong"); s != http.StatusUnauthorized {
		t.Errorf("wrong key: status %d, want 401", s)
	}
	got, err := llamaserver.Translate(context.Background(), a.endpoint, first, "Hello", languages.Direction{Source: "en", Target: "ja"}, 16)
	if err != nil || got != testsupport.FakeTranslation {
		t.Fatalf("with the key: got %q, %v", got, err)
	}

	// A restart (crash recovery, engine or model switch) gets a new key.
	a.sup.Stop()
	if a.sup.APIKey() != "" {
		t.Error("a stopped supervisor must forget its key")
	}
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	second := a.sup.APIKey()
	if second == "" || second == first {
		t.Fatal("each start must make a new key")
	}
	if s := chatStatus(t, a.endpoint, "Bearer "+first); s != http.StatusUnauthorized {
		t.Errorf("the previous key: status %d, want 401", s)
	}
	if got, err := llamaserver.Translate(context.Background(), a.endpoint, second, "Hello", languages.Direction{Source: "en", Target: "ja"}, 16); err != nil || got != testsupport.FakeTranslation {
		t.Fatalf("with the new key: got %q, %v", got, err)
	}
}

// The app's own hotkey path sends the key: a translation through
// startTranslation reaches the key-checking fake server and lands on the
// clipboard.
func TestAppTranslationUsesTheServerKey(t *testing.T) {
	a, tray, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	cb := &testClipboard{text: "Hello"}
	a.cb = cb
	a.foreground = func() uintptr { return 1 }
	a.paste = func(uintptr) error { return nil }
	a.capture = func(platform.Clipboard, uintptr) (string, bool, error) { return "Hello", true, nil }

	a.startTranslation(0)
	select {
	case res := <-a.results:
		if res.err != nil {
			t.Fatalf("translation failed: %v", res.err)
		}
		a.handleResult(res)
	case <-time.After(10 * time.Second):
		t.Fatal("no result")
	}
	if cb.text != testsupport.FakeTranslation {
		t.Errorf("clipboard = %q, want %q (notes: %v)", cb.text, testsupport.FakeTranslation, tray.notes)
	}
}
