//go:build windows

package platform

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestKillOnCloseJobTerminatesChild(t *testing.T) {
	job, err := newKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ping.exe", "-n", "120", "127.0.0.1")
	HideConsole(cmd)
	if err := cmd.Start(); err != nil {
		syscall.CloseHandle(job)
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	if err := assignPidToJob(job, cmd.Process.Pid); err != nil {
		syscall.CloseHandle(job)
		t.Fatal(err)
	}
	select {
	case <-waited:
		t.Fatal("the child must still be running before the job closes")
	case <-time.After(300 * time.Millisecond):
	}
	syscall.CloseHandle(job) // what the OS does for us when the parent dies
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("closing the job must terminate its processes")
	}
}
