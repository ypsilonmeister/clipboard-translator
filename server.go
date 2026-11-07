package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
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

	cmd  *exec.Cmd
	done chan struct{} // closed when cmd has exited
}

// start launches binary and blocks until the server answers /health.
func (s *supervisor) start(binary string, gpuLayers int, label string) error {
	args := []string{"-m", s.model, "-c", fmt.Sprint(s.contextTokens),
		"--host", "127.0.0.1", "--port", fmt.Sprint(s.port), "--alias", "plamo-3-translate",
		"--chat-template-file", s.template, "--no-prefill-assistant", "-ngl", fmt.Sprint(gpuLayers)}
	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Dir(binary)
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
	var err error
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
	s.cmd, s.done = cmd, done
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
	s.cmd, s.done = nil, nil
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
