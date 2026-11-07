package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func hideConsole(cmd *exec.Cmd) {
	dir := filepath.Dir(cmd.Path)
	libs := dir + ":" + filepath.Join(dir, "lib")
	if old := os.Getenv("DYLD_LIBRARY_PATH"); old != "" {
		libs += ":" + old
	}
	cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+libs)
}

func runSetup(dir string) error {
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
			return err
		case <-ticker.C:
			pumpMessages()
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
