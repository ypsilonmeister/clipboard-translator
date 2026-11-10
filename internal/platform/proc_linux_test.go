//go:build linux

package platform

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The parent-death test re-executes the test binary as a stand-in for the app
// (CT_TEST_PDEATH_HELPER=1). That helper starts the fake llama-server the way
// the supervisor does (hideConsole before Start), prints its PID and waits to
// be killed. init runs before TestMain, so the helper never runs the tests.
func init() {
	if os.Getenv("CT_TEST_PDEATH_HELPER") != "1" || os.Getenv("CT_TEST_FAKE_SERVER") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "--port", "0")
	// An explicit Env without the helper variable: the child must be the fake
	// server, not another helper. hideConsole has to keep it.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CT_TEST_PDEATH_HELPER=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "CT_TEST_FAKE_SERVER=1")
	hideConsole(cmd)
	if !envHas(cmd.Env, "CT_TEST_FAKE_SERVER=1") {
		fmt.Fprintln(os.Stderr, "hideConsole dropped cmd.Env")
		os.Exit(2)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(cmd.Process.Pid)
	// Block until the test kills this process. (A bare select{} would be
	// reported as a deadlock and end the helper by itself.)
	_ = cmd.Wait()
	fmt.Fprintln(os.Stderr, "the fake server exited before the helper was killed")
	os.Exit(3)
}

func envHas(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestHideConsoleKeepsEnvAndSetsPdeathsig(t *testing.T) {
	cmd := exec.Command("/opt/x/llama-server")
	cmd.Env = []string{"LLAMA_API_KEY=secret"}
	hideConsole(cmd)
	if !envHas(cmd.Env, "LLAMA_API_KEY=secret") {
		t.Fatalf("hideConsole dropped a variable set on cmd.Env: %q", cmd.Env)
	}
	if len(cmd.Env) != 2 || !strings.HasPrefix(cmd.Env[1], "LD_LIBRARY_PATH=/opt/x:/opt/x/lib") {
		t.Fatalf("Env = %q, want the given variable plus LD_LIBRARY_PATH", cmd.Env)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatal("llama-server must get SIGKILL when the app dies")
	}

	inherit := exec.Command("/opt/x/llama-server")
	hideConsole(inherit)
	if len(inherit.Env) != len(os.Environ())+1 {
		t.Fatalf("nil Env must inherit the environment: got %d entries", len(inherit.Env))
	}
}

// gone reports whether pid has exited. A killed orphan is a zombie until init
// reaps it, and kill(pid, 0) still succeeds on a zombie.
func gone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && strings.HasPrefix(strings.TrimSpace(s[i+1:]), "Z")
}

func TestServerDiesWithKilledApp(t *testing.T) {
	helper := exec.Command(os.Args[0], "-test.run=^$")
	helper.Env = append(os.Environ(), "CT_TEST_PDEATH_HELPER=1")
	helper.Stderr = os.Stderr
	out, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = helper.Process.Kill(); _ = helper.Wait() }()

	pidc := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		pidc <- strings.TrimSpace(line)
	}()
	var pid int
	select {
	case line := <-pidc:
		if pid, err = strconv.Atoi(line); err != nil || pid <= 1 {
			t.Fatalf("helper printed %q, want the server PID", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not start the server")
	}
	// Only ever this PID, which the helper started for this test.
	defer func() {
		if !gone(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
	time.Sleep(100 * time.Millisecond) // let a helper or server that fails at once do so
	if gone(pid) || gone(helper.Process.Pid) {
		t.Fatal("the helper or the server exited on its own; the test cannot tell anything")
	}

	// Like a crash or kill -9 of the app: no chance to stop the server itself.
	if err := helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if gone(pid) {
			return
		}
	}
	t.Fatalf("llama-server (pid %d) outlived the killed app", pid)
}
