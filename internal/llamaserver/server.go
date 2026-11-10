package llamaserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

// ErrQuit is returned by Supervisor.Start when the user chose "Quit" while the
// model was loading.
var ErrQuit = errors.New("quit requested")

// Supervisor owns the llama-server child process: it starts it, waits until the
// model is loaded, and lets the main loop learn when the process dies.
type Supervisor struct {
	Model, Template string
	ContextTokens   int
	Port            int
	Endpoint        string
	LogOut          io.Writer
	StartupTimeout  time.Duration

	// UI is called about every 50 ms while Start blocks. It must service the
	// tray's message pump and return true when the user asked to quit, which
	// aborts the wait. label says what is going on ("loading model") and
	// elapsed how long it has been going on.
	UI func(label string, elapsed time.Duration) (quit bool)

	// PIDFile records the running server so a later start can stop it if the
	// app dies without stopping it (macOS only; see platform.RecordChild).
	// Empty: none.
	PIDFile string

	// apiKey is the key the running server requires (see serverEnv). Start makes
	// a new one each time; it is never logged. APIKey reads it.
	apiKey string

	OnStarted func() // UI-thread callback after a successful start
	starting  bool
	cmd       *exec.Cmd
	done      chan struct{} // closed when cmd has exited
}

// APIKey is the key the running server requires, "" when none is running. Like
// the rest of the Supervisor it belongs to the UI thread.
func (s *Supervisor) APIKey() string { return s.apiKey }

// BinaryPath is the llama-server executable that is running, "" when none is.
func (s *Supervisor) BinaryPath() string {
	if s.cmd == nil {
		return ""
	}
	return s.cmd.Path
}

// Args is the command line of the running server (without the program name's
// environment, so without the key), nil when none is running.
func (s *Supervisor) Args() []string {
	if s.cmd == nil {
		return nil
	}
	return append([]string(nil), s.cmd.Args...)
}

// Starting reports whether Start is running (the UI callback can be reached
// while it blocks).
func (s *Supervisor) Starting() bool { return s.starting }

// SetStarting overrides Starting. It exists so that tests of the app can stage
// "the server is starting" without running one.
func (s *Supervisor) SetStarting(v bool) { s.starting = v }

// Kill ends the server process at once, without Stop's bookkeeping. It exists
// so that tests can simulate a crash.
func (s *Supervisor) Kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// PID is the process id of the running server, 0 when none is.
func (s *Supervisor) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// newAPIKey returns 32 random bytes as hex.
func newAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// serverEnv is the environment for llama-server. Without an API key any web
// page in the user's browser could use the loopback server (llama-server's
// CORS default allows every origin), so it requires a per-start key.
//
// Both settings go in the environment, not on the command line: an unknown
// flag makes an older llama-server exit, while an unknown variable is ignored,
// and the key stays out of the command line. LLAMA_API_KEY is the variable for
// --api-key; LLAMA_ARG_UI=0 turns off the Web UI (--no-ui) on builds that
// read it. They are appended last so they win over the user's own values.
func serverEnv(base []string, key string) []string {
	env := append([]string(nil), base...)
	return append(env, "LLAMA_API_KEY="+key, "LLAMA_ARG_UI=0")
}

// serverArgs returns the command-line arguments for llama-server.
func serverArgs(model, template string, contextTokens, port, gpuLayers int) []string {
	return []string{
		"-m", model,
		"-c", fmt.Sprint(contextTokens),
		"--host", "127.0.0.1",
		"--port", fmt.Sprint(port),
		"--alias", "plamo-3-translate",
		"--chat-template-file", template,
		"--no-prefill-assistant",
		"-ngl", fmt.Sprint(gpuLayers),
	}
}

// serverArgs returns the command-line arguments for this supervisor's server.
func (s *Supervisor) serverArgs(gpuLayers int) []string {
	return serverArgs(s.Model, s.Template, s.ContextTokens, s.Port, gpuLayers)
}

// Start launches binary and blocks until the server answers /health.
func (s *Supervisor) Start(binary string, gpuLayers int, label string) error {
	s.starting = true
	defer func() { s.starting = false }()
	// Without the VC++ runtime every llama.cpp build dies at once with no output,
	// so say so instead of starting a process that cannot work.
	if !platform.VCRuntimeAvailable(filepath.Dir(binary)) {
		log.Printf("not starting %s: vcruntime140_1.dll was not found", binary)
		return &DLLNotFoundError{binary: filepath.Base(binary), VCMissing: true}
	}
	key, err := newAPIKey()
	if err != nil {
		return fmt.Errorf("could not create a key for the translation server: %w", err)
	}
	args := s.serverArgs(gpuLayers)
	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Dir(binary)
	cmd.Env = serverEnv(os.Environ(), key)
	cmd.Stdout, cmd.Stderr = s.LogOut, s.LogOut
	platform.HideConsole(cmd)
	log.Printf("starting %s (gpu_layers=%d)", binary, gpuLayers)
	if err := cmd.Start(); err != nil {
		return err
	}
	platform.BindToParentLifetime(cmd)
	platform.RecordChild(s.PIDFile, cmd)
	done := make(chan struct{})
	var waitErr error                                 // read only after done is closed
	go func() { waitErr = cmd.Wait(); close(done) }() // the only Wait on this child

	begin := time.Now()
	startupCtx, cancel := context.WithTimeout(context.Background(), s.StartupTimeout)
	defer cancel()
	// waitReady blocks, so run it aside and keep servicing the tray window on
	// this thread while the model loads.
	readyErr := make(chan error, 1)
	go func() { readyErr <- WaitReady(startupCtx, s.Endpoint, done) }()
	pump := time.NewTicker(50 * time.Millisecond)
	defer pump.Stop()
	quit := false
wait:
	for {
		select {
		case err = <-readyErr:
			break wait
		case <-pump.C:
			if s.UI != nil && s.UI(label, time.Since(begin)) && !quit {
				quit = true
				cancel()
			}
		}
	}
	if err != nil {
		_ = cmd.Process.Kill()
		<-done
		platform.ForgetChild(s.PIDFile)
		if quit {
			return ErrQuit
		}
		if IsDLLNotFound(waitErr) {
			return &DLLNotFoundError{binary: filepath.Base(binary), VCMissing: !platform.VCRuntimeAvailable(filepath.Dir(binary))}
		}
		return err
	}
	s.cmd, s.done, s.apiKey = cmd, done, key
	if s.OnStarted != nil {
		s.OnStarted()
	}
	return nil
}

// statusDLLNotFound is the exit code Windows gives a process whose imported DLL
// cannot be found (STATUS_DLL_NOT_FOUND, 0xC0000135). llama-server gets it when
// the Visual C++ runtime is too old or missing; it prints nothing, so
// server.log stays empty.
const statusDLLNotFound = 0xC0000135

func exitCodeIsDLLNotFound(code int) bool { return uint32(code) == statusDLLNotFound }

// IsDLLNotFound reports whether err (from cmd.Wait) is that exit.
func IsDLLNotFound(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && exitCodeIsDLLNotFound(ee.ExitCode())
}

// DLLNotFoundError means llama-server could not even start because Windows
// could not load a DLL it needs. Another engine build will not help in the
// common case (the shared runtime is missing), so the message tells the user
// what to install.
type DLLNotFoundError struct {
	binary    string
	VCMissing bool // vcruntime140_1.dll is neither next to the exe nor in System32
}

func (e *DLLNotFoundError) Error() string {
	return dllNotFoundMessage(e.binary, !e.VCMissing)
}

const vcRedistHelp = "Install the Visual C++ Redistributable (x64), then start Clipboard Translator again:\n" +
	"  winget install --id Microsoft.VCRedist.2015+.x64 -e\n" +
	"or download https://aka.ms/vs/17/release/vc_redist.x64.exe"

func dllNotFoundMessage(binary string, vcInstalled bool) string {
	if !vcInstalled {
		return "The translation server (" + binary + ") cannot start because the Visual C++ runtime is missing or too old (vcruntime140_1.dll not found).\n" + vcRedistHelp
	}
	return "The translation server (" + binary + ") cannot start: Windows could not find a DLL it needs (exit code 0xC0000135).\n" +
		"Most often this is an old Visual C++ runtime. " + vcRedistHelp + "\n" +
		"If that does not help, run Setup again to reinstall the engine."
}

// Exited is closed when the running server dies. It is nil (never ready) when
// no server is running, so it can sit in a select unconditionally.
func (s *Supervisor) Exited() <-chan struct{} {
	if s.done == nil {
		return nil
	}
	return s.done
}

// Running reports whether a server is started and being watched.
func (s *Supervisor) Running() bool { return s.cmd != nil }

// Stop kills the server if it is still running and reaps it.
func (s *Supervisor) Stop() {
	if s.cmd == nil {
		return
	}
	_ = s.cmd.Process.Kill()
	<-s.done
	platform.ForgetChild(s.PIDFile)
	s.cmd, s.done, s.apiKey = nil, nil, ""
}

// RestartLimiter allows at most max restarts within a sliding window, so a
// server that dies at once, again and again, is given up on instead of being
// restarted forever.
type RestartLimiter struct {
	Max    int
	Window time.Duration
	times  []time.Time
}

// Allow records a restart at now and reports whether it is within the limit.
func (r *RestartLimiter) Allow(now time.Time) bool {
	cutoff := now.Add(-r.Window)
	kept := r.times[:0]
	for _, t := range r.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.times = kept
	if len(r.times) >= r.Max {
		return false
	}
	r.times = append(r.times, now)
	return true
}

// Attempt is the number of restarts recorded in the current window.
func (r *RestartLimiter) Attempt() int { return len(r.times) }

// RestartBackoff is how long to wait before restart number attempt (1-based):
// 1s, 2s, 4s, ... capped at 30s.
func RestartBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Second
	for i := 1; i < attempt && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
