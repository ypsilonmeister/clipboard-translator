package llamaserver

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRestartBackoff(t *testing.T) {
	want := map[int]time.Duration{
		0: time.Second, 1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		5: 16 * time.Second, 6: 30 * time.Second, 50: 30 * time.Second,
	}
	for attempt, d := range want {
		if got := RestartBackoff(attempt); got != d {
			t.Errorf("restartBackoff(%d) = %v, want %v", attempt, got, d)
		}
	}
}

func TestRestartLimiter(t *testing.T) {
	r := RestartLimiter{Max: 3, Window: 10 * time.Minute}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		if !r.Allow(t0.Add(time.Duration(i) * time.Minute)) {
			t.Fatalf("restart %d must be allowed", i)
		}
		if r.Attempt() != i {
			t.Fatalf("Attempt = %d, want %d", r.Attempt(), i)
		}
	}
	if r.Allow(t0.Add(4 * time.Minute)) {
		t.Fatal("a 4th restart within the window must be refused")
	}
	// At minute 12 the window is (2, 12]: the restarts at minutes 1 and 2 are out
	// (a restart exactly one window ago no longer counts), minute 3 is still in.
	if !r.Allow(t0.Add(12 * time.Minute)) {
		t.Fatal("old restarts must age out of the window")
	}
	if r.Attempt() != 2 {
		t.Fatalf("Attempt after aging out = %d, want 2 (minutes 3 and 12)", r.Attempt())
	}
}

func TestSupervisorExitedIsNilWhenStopped(t *testing.T) {
	var s Supervisor
	if s.Exited() != nil {
		t.Fatal("Exited must be a nil channel when no server runs, so it never fires in a select")
	}
	s.Stop() // must be a harmless no-op
}

func TestExitCodeIsDLLNotFound(t *testing.T) {
	// ExitCode() is an int: 0xC0000135 is positive on 64-bit Windows and
	// negative where int is 32 bits wide; both must match.
	for _, code := range []int{0xC0000135, int(int32(-1073741515))} {
		if !exitCodeIsDLLNotFound(code) {
			t.Errorf("exit code %d must be STATUS_DLL_NOT_FOUND", code)
		}
	}
	for _, code := range []int{0, 1, -1, 0xC0000005, 0xC0000142} {
		if exitCodeIsDLLNotFound(code) {
			t.Errorf("exit code %#x must not be STATUS_DLL_NOT_FOUND", uint32(code))
		}
	}
	if IsDLLNotFound(nil) || IsDLLNotFound(errors.New("x")) {
		t.Error("only an *exec.ExitError carries an exit code")
	}
}

func TestDLLNotFoundMessage(t *testing.T) {
	missing := dllNotFoundMessage("llama-server.exe", false)
	for _, want := range []string{"vcruntime140_1.dll", "winget install --id Microsoft.VCRedist.2015+.x64 -e", "vc_redist.x64.exe"} {
		if !strings.Contains(missing, want) {
			t.Errorf("message for a missing runtime lacks %q:\n%s", want, missing)
		}
	}
	other := dllNotFoundMessage("llama-server.exe", true)
	for _, want := range []string{"0xC0000135", "winget install", "Setup"} {
		if !strings.Contains(other, want) {
			t.Errorf("generic message lacks %q:\n%s", want, other)
		}
	}
}

func TestServerArgs(t *testing.T) {
	s := &Supervisor{
		Model:         "models/test.gguf",
		Template:      "models/test.jinja",
		ContextTokens: 2048,
		Port:          18119,
	}
	args := s.serverArgs(0)
	want := []string{
		"-m", "models/test.gguf",
		"-c", "2048",
		"--host", "127.0.0.1",
		"--port", "18119",
		"--alias", "plamo-3-translate",
		"--chat-template-file", "models/test.jinja",
		"--no-prefill-assistant",
		"-ngl", "0",
	}
	if len(args) != len(want) {
		t.Fatalf("got %d args, want %d", len(args), len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}
