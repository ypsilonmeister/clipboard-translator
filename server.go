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

	// apiKey is the key the running server requires (see serverEnv). start makes
	// a new one each time; it is never logged.
	apiKey string

	cmd  *exec.Cmd
	done chan struct{} // closed when cmd has exited
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

// start launches binary and blocks until the server answers /health.
func (s *supervisor) start(binary string, gpuLayers int, label string) error {
	key, err := newAPIKey()
	if err != nil {
		return fmt.Errorf("could not create a key for the translation server: %w", err)
	}
	args := []string{"-m", s.model, "-c", fmt.Sprint(s.contextTokens),
		"--host", "127.0.0.1", "--port", fmt.Sprint(s.port), "--alias", "plamo-3-translate",
		"--chat-template-file", s.template, "--no-prefill-assistant", "-ngl", fmt.Sprint(gpuLayers)}
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
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }() // the only Wait on this child

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
		if quit {
			return errQuit
		}
		return err
	}
	s.cmd, s.done, s.apiKey = cmd, done, key
	return nil
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
