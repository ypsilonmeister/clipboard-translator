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

	"github.com/ypsilonmeister/clipboard-translator/internal/llamaserver"
	"github.com/ypsilonmeister/clipboard-translator/internal/platform"
)

const (
	synchronize                    = 0x00100000
	processQueryLimitedInformation = 0x1000
)

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

func TestSupervisorTiesServerToJob(t *testing.T) {
	a, _, _ := newTestApp(t)
	if err := a.startServer(); err != nil {
		t.Fatal(err)
	}
	in, err := platform.ProcessInKillOnCloseJob(a.sup.PID())
	if err != nil {
		t.Fatal(err)
	}
	if !in {
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
	s := &llamaserver.Supervisor{Port: port, Endpoint: fmt.Sprintf("http://127.0.0.1:%d", port),
		LogOut: io.Discard, StartupTimeout: 30 * time.Second, ContextTokens: 1}
	self, _ := os.Executable()
	if err := s.Start(self, 0, "test"); err != nil {
		fmt.Println("CT_ERROR=" + err.Error())
		os.Exit(2)
	}
	fmt.Printf("CT_CHILD_PID=%d\n", s.PID())
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
