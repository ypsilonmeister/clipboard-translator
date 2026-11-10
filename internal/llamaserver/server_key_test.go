package llamaserver

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/ypsilonmeister/clipboard-translator/internal/languages"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
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
	platform.HideConsole(cmd)
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
		if _, err := Translate(context.Background(), srv.URL, key, "Hello", languages.Direction{Source: "en", Target: "ja"}, 16); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "Bearer secret" || got[1] != "" {
		t.Fatalf("Authorization headers: %q", got)
	}
}
