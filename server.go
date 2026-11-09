package main

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
)

// errQuit is returned by supervisor.start when the user chose "Quit" while the
// model was loading.
var errQuit = errors.New("quit requested")

// supervisor owns the llama-server child process: it starts it, waits until the
// model is loaded, and lets the main loop learn when the process dies.
type supervisor struct {
	model, template string
	contextTokens   int
	port            int
	endpoint        string
	logOut          io.Writer
	startupTimeout  time.Duration

	// ui is called about every 50 ms while start blocks. It must service the
	// tray's message pump and return true when the user asked to quit, which
	// aborts the wait. label says what is going on ("loading model") and
	// elapsed how long it has been going on.
	ui func(label string, elapsed time.Duration) (quit bool)

	// pidFile records the running server so a later start can stop it if the
	// app dies without stopping it (macOS only; see recordChild). Empty: none.
	pidFile string

	// apiKey is the key the running server requires (see serverEnv). start makes
	// a new one each time; it is never logged.
	apiKey string

	onStarted func() // UI-thread callback after a successful start
	starting  bool
	cmd       *exec.Cmd
	done      chan struct{} // closed when cmd has exited
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
func (s *supervisor) serverArgs(gpuLayers int) []string {
	return serverArgs(s.model, s.template, s.contextTokens, s.port, gpuLayers)
}

// start launches binary and blocks until the server answers /health.
func (s *supervisor) start(binary string, gpuLayers int, label string) error {
	s.starting = true
	defer func() { s.starting = false }()
	// Without the VC++ runtime every llama.cpp build dies at once with no output,
	// so say so instead of starting a process that cannot work.
	if !vcRuntimeAvailable(filepath.Dir(binary)) {
		log.Printf("not starting %s: vcruntime140_1.dll was not found", binary)
		return &dllNotFoundError{binary: filepath.Base(binary), vcMissing: true}
	}
	key, err := newAPIKey()
	if err != nil {
		return fmt.Errorf("could not create a key for the translation server: %w", err)
	}
	args := s.serverArgs(gpuLayers)
	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Dir(binary)
	cmd.Env = serverEnv(os.Environ(), key)
	cmd.Stdout, cmd.Stderr = s.logOut, s.logOut
	hideConsole(cmd)
	log.Printf("starting %s (gpu_layers=%d)", binary, gpuLayers)
	if err := cmd.Start(); err != nil {
		return err
	}
	bindToParentLifetime(cmd)
	recordChild(s.pidFile, cmd)
	done := make(chan struct{})
	var waitErr error                                 // read only after done is closed
	go func() { waitErr = cmd.Wait(); close(done) }() // the only Wait on this child

	begin := time.Now()
	startupCtx, cancel := context.WithTimeout(context.Background(), s.startupTimeout)
	defer cancel()
	// waitReady blocks, so run it aside and keep servicing the tray window on
	// this thread while the model loads.
	readyErr := make(chan error, 1)
	go func() { readyErr <- waitReady(startupCtx, s.endpoint, done) }()
	pump := time.NewTicker(50 * time.Millisecond)
	defer pump.Stop()
	quit := false
wait:
	for {
		select {
		case err = <-readyErr:
			break wait
		case <-pump.C:
			if s.ui != nil && s.ui(label, time.Since(begin)) && !quit {
				quit = true
				cancel()
			}
		}
	}
	if err != nil {
		_ = cmd.Process.Kill()
		<-done
		forgetChild(s.pidFile)
		if quit {
			return errQuit
		}
		if isDLLNotFound(waitErr) {
			return &dllNotFoundError{binary: filepath.Base(binary), vcMissing: !vcRuntimeAvailable(filepath.Dir(binary))}
		}
		return err
	}
	s.cmd, s.done, s.apiKey = cmd, done, key
	if s.onStarted != nil {
		s.onStarted()
	}
	return nil
}

// statusDLLNotFound is the exit code Windows gives a process whose imported DLL
// cannot be found (STATUS_DLL_NOT_FOUND, 0xC0000135). llama-server gets it when
// the Visual C++ runtime is too old or missing; it prints nothing, so
// server.log stays empty.
const statusDLLNotFound = 0xC0000135

func exitCodeIsDLLNotFound(code int) bool { return uint32(code) == statusDLLNotFound }

// isDLLNotFound reports whether err (from cmd.Wait) is that exit.
func isDLLNotFound(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && exitCodeIsDLLNotFound(ee.ExitCode())
}

// dllNotFoundError means llama-server could not even start because Windows
// could not load a DLL it needs. Another engine build will not help in the
// common case (the shared runtime is missing), so the message tells the user
// what to install.
type dllNotFoundError struct {
	binary    string
	vcMissing bool // vcruntime140_1.dll is neither next to the exe nor in System32
}

func (e *dllNotFoundError) Error() string {
	return dllNotFoundMessage(e.binary, !e.vcMissing)
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
func (s *supervisor) Exited() <-chan struct{} {
	if s.done == nil {
		return nil
	}
	return s.done
}

// Running reports whether a server is started and being watched.
func (s *supervisor) Running() bool { return s.cmd != nil }

// Stop kills the server if it is still running and reaps it.
func (s *supervisor) Stop() {
	if s.cmd == nil {
		return
	}
	_ = s.cmd.Process.Kill()
	<-s.done
	forgetChild(s.pidFile)
	s.cmd, s.done, s.apiKey = nil, nil, ""
}

// restartLimiter allows at most max restarts within a sliding window, so a
// server that dies at once, again and again, is given up on instead of being
// restarted forever.
type restartLimiter struct {
	max    int
	window time.Duration
	times  []time.Time
}

// Allow records a restart at now and reports whether it is within the limit.
func (r *restartLimiter) Allow(now time.Time) bool {
	cutoff := now.Add(-r.window)
	kept := r.times[:0]
	for _, t := range r.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.times = kept
	if len(r.times) >= r.max {
		return false
	}
	r.times = append(r.times, now)
	return true
}

// Attempt is the number of restarts recorded in the current window.
func (r *restartLimiter) Attempt() int { return len(r.times) }

// restartBackoff is how long to wait before restart number attempt (1-based):
// 1s, 2s, 4s, ... capped at 30s.
func restartBackoff(attempt int) time.Duration {
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
