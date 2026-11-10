package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func HideConsole(cmd *exec.Cmd) {
	dir := filepath.Dir(cmd.Path)
	libs := dir + ":" + filepath.Join(dir, "lib")
	if old := os.Getenv("DYLD_LIBRARY_PATH"); old != "" {
		libs += ":" + old
	}
	// Keep variables the caller already set on cmd.Env (nil means "inherit").
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "DYLD_LIBRARY_PATH="+libs)
}

// BindToParentLifetime is only implemented on Windows (Job Object). macOS has
// no parent-death signal, so the server is recorded instead (recordChild) and
// a leftover one is stopped at the next start (stopLeftoverServer).
func BindToParentLifetime(cmd *exec.Cmd) {}

// RecordChild notes the started server in file, so that the next start can stop
// it if this app dies without stopping it. Best effort: failure only costs that
// cleanup, so it is logged.
func RecordChild(file string, cmd *exec.Cmd) {
	if file == "" {
		return
	}
	data, err := encodeChildRecord(childRecord{PID: cmd.Process.Pid, Path: cmd.Path})
	if err == nil {
		err = os.WriteFile(file, data, 0o600)
	}
	if err != nil {
		log.Printf("could not record the server process: %v", err)
	}
}

// ForgetChild removes the record once the server has been stopped.
func ForgetChild(file string) {
	if file == "" {
		return
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("could not remove %s: %v", file, err)
	}
}

// StopLeftoverServer stops the llama-server recorded in file if an earlier run
// of the app died without stopping it. It must only run once this process holds
// the single-instance lock. A process is only killed when isLeftoverServer
// confirms it is still that executable, orphaned, so a reused PID is left alone.
func StopLeftoverServer(file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		return // normally no record: the last run stopped its server
	}
	defer ForgetChild(file)
	rec, err := parseChildRecord(data)
	if err != nil {
		log.Printf("ignoring %s: %v", file, err)
		return
	}
	out, err := exec.Command("/bin/ps", "-ww", "-p", strconv.Itoa(rec.PID), "-o", "ppid=,comm=").Output()
	if err != nil {
		return // ps fails when no such process exists any more
	}
	if !isLeftoverServer(rec, string(out), os.Getpid()) {
		log.Printf("process %d is not the server an earlier run left behind; leaving it alone", rec.PID)
		return
	}
	log.Printf("stopping llama-server (pid %d) left over from an earlier run", rec.PID)
	if err := syscall.Kill(rec.PID, syscall.SIGKILL); err != nil {
		log.Printf("could not stop pid %d: %v", rec.PID, err)
		return
	}
	// Wait until it is gone so its port is free when the app checks it.
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if errors.Is(syscall.Kill(rec.PID, 0), syscall.ESRCH) {
			return
		}
	}
}

func RunSetup(dir string) error {
	cmd := exec.Command("/bin/bash", "setup.sh", "--backend", "metal")
	cmd.Dir = dir
	// Finder launches have no terminal. Persist progress/errors beside server.log.
	f, err := os.OpenFile(filepath.Join(dir, "setup.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	beginSetupProgress(f.Name())
	defer endSetupProgress()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("%w; see %s for download/setup details", err, f.Name())
			}
			return err
		case <-ticker.C:
			PumpMessages()
			if setupCancelled() {
				// Terminate the dedicated process group, including the downloader.
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(time.Second):
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					<-done
				}
				return errors.New("setup cancelled")
			}
		}
	}
}
