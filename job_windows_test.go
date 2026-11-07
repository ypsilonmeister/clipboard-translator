//go:build windows

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const (
	synchronize                    = 0x00100000
	processQueryLimitedInformation = 0x1000
)

var isProcessInJob = kernel32.NewProc("IsProcessInJob")

// waitPidGone waits for process pid to end. A pid that cannot be opened any
// more counts as gone.
func waitPidGone(pid int, d time.Duration) bool {
	h, err := syscall.OpenProcess(synchronize|processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return true
	}
	defer syscall.CloseHandle(h)
	ev, _ := syscall.WaitForSingleObject(h, uint32(d/time.Millisecond))
	return ev == syscall.WAIT_OBJECT_0
}

func TestKillOnCloseJobTerminatesChild(t *testing.T) {
	job, err := newKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ping.exe", "-n", "120", "127.0.0.1")
	hideConsole(cmd)
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

func TestSupervisorTiesServerToJob(t *testing.T) {
	a, _, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	if jobErr != nil {
		t.Fatalf("job object unavailable: %v", jobErr)
	}
	ph, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(a.sup.cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(ph)
	var in int32
	if r, _, err := isProcessInJob.Call(uintptr(ph), uintptr(jobHandle), uintptr(unsafe.Pointer(&in))); r == 0 {
		t.Fatalf("IsProcessInJob: %v", err)
	}
	if in == 0 {
		t.Fatal("llama-server must be assigned to the kill-on-close job")
	}
}

// TestHelperParentProcess is not a test: it is the "app" that the test below
// kills. It starts a (fake) server through the supervisor, prints its pid and
// then waits to be terminated.
func TestHelperParentProcess(t *testing.T) {
	if os.Getenv("CT_TEST_PARENT") != "1" {
		t.Skip("helper for TestServerDiesWithTerminatedParent")
	}
	os.Setenv("CT_TEST_FAKE_SERVER", "1") // for the child server only
	port := freePort(t)
	s := &supervisor{port: port, endpoint: fmt.Sprintf("http://127.0.0.1:%d", port),
		logOut: io.Discard, startupTimeout: 30 * time.Second, contextTokens: 1}
	self, _ := os.Executable()
	if err := s.start(self, 0, "test"); err != nil {
		fmt.Println("CT_ERROR=" + err.Error())
		os.Exit(2)
	}
	fmt.Printf("CT_CHILD_PID=%d\n", s.cmd.Process.Pid)
	select {}
}

// TestServerDiesWithTerminatedParent is the #19 scenario: the app is killed
// (like Stop-Process -Force) and llama-server must not survive it.
func TestServerDiesWithTerminatedParent(t *testing.T) {
	self, _ := os.Executable()
	parent := exec.Command(self, "-test.run=^TestHelperParentProcess$")
	parent.Env = append(os.Environ(), "CT_TEST_PARENT=1")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })

	pidCh := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "CT_CHILD_PID="); ok {
				n, _ := strconv.Atoi(v)
				pidCh <- n
				return
			}
			if strings.HasPrefix(line, "CT_ERROR=") {
				pidCh <- -1
				t.Log(line)
				return
			}
		}
		pidCh <- -1
	}()
	var child int
	select {
	case child = <-pidCh:
	case <-time.After(60 * time.Second):
		t.Fatal("the helper app did not start its server")
	}
	if child <= 0 {
		t.Fatal("the helper app could not start its server")
	}
	t.Cleanup(func() { // never leave a stray server behind if the assertion fails
		if p, err := os.FindProcess(child); err == nil {
			_ = p.Kill()
		}
	})
	if waitPidGone(child, 200*time.Millisecond) {
		t.Fatal("the server must be running while the parent is alive")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !waitPidGone(child, 15*time.Second) {
		t.Fatal("the server outlived its terminated parent")
	}
}
