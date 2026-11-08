package main

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNewAPIKeyIsRandomHex(t *testing.T) {
	a, err := newAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 64 {
		t.Errorf("want 32 bytes as hex, got %d characters", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("not hex: %v", err)
	}
	if a == b {
		t.Error("two keys must differ")
	}
}

// hideConsole runs after the key is put in cmd.Env. On Linux and macOS it adds
// the library path; it must keep the key (it once replaced the whole
// environment, which silently started the server without a key).
func TestHideConsoleKeepsTheServerKey(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = serverEnv(os.Environ(), "the-key")
	hideConsole(cmd)
	found := false
	for _, kv := range cmd.Env {
		found = found || kv == "LLAMA_API_KEY=the-key"
	}
	if !found {
		t.Fatalf("hideConsole dropped LLAMA_API_KEY from the environment")
	}
}

func TestServerEnvPutsTheKeyLast(t *testing.T) {
	base := []string{"LLAMA_API_KEY=from-user", "PATH=x"}
	env := serverEnv(base, "k")
	n := len(env)
	// exec.Cmd keeps the last of duplicate names, so ours must come last.
	if n != 4 || env[n-2] != "LLAMA_API_KEY=k" || env[n-1] != "LLAMA_ARG_UI=0" {
		t.Fatalf("env = %q", env)
	}
	if base[0] != "LLAMA_API_KEY=from-user" || len(base) != 2 {
		t.Errorf("the caller's slice must not change: %q", base)
	}
}

func TestTranslateSendsTheKey(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	for _, key := range []string{"secret", ""} {
		if _, err := translate(context.Background(), srv.URL, key, "Hello", translationDirection{"en", "ja"}, 16); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "Bearer secret" || got[1] != "" {
		t.Fatalf("Authorization headers: %q", got)
	}
}

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
	first := a.sup.apiKey
	if len(first) != 64 {
		t.Fatalf("a running server must have a key, got %d characters", len(first))
	}
	for _, arg := range a.sup.cmd.Args {
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
	got, err := translate(context.Background(), a.endpoint, first, "Hello", translationDirection{"en", "ja"}, 16)
	if err != nil || got != fakeTranslation {
		t.Fatalf("with the key: got %q, %v", got, err)
	}

	// A restart (crash recovery, engine or model switch) gets a new key.
	a.sup.Stop()
	if a.sup.apiKey != "" {
		t.Error("a stopped supervisor must forget its key")
	}
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	second := a.sup.apiKey
	if second == "" || second == first {
		t.Fatal("each start must make a new key")
	}
	if s := chatStatus(t, a.endpoint, "Bearer "+first); s != http.StatusUnauthorized {
		t.Errorf("the previous key: status %d, want 401", s)
	}
	if got, err := translate(context.Background(), a.endpoint, second, "Hello", translationDirection{"en", "ja"}, 16); err != nil || got != fakeTranslation {
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
	a.capture = func(Clipboard, uintptr) (string, bool, error) { return "Hello", true, nil }

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
	if cb.text != fakeTranslation {
		t.Errorf("clipboard = %q, want %q (notes: %v)", cb.text, fakeTranslation, tray.notes)
	}
}
