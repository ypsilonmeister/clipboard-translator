package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// hideConsole prepares the llama-server command before it is started: it adds
// the bundled libraries to the search path and makes the server die with the
// app (see dieWithParent).
func hideConsole(cmd *exec.Cmd) {
	// Official archives ship shared libraries beside the server (or in lib/).
	dir := filepath.Dir(cmd.Path)
	libs := dir + ":" + filepath.Join(dir, "lib")
	if old := os.Getenv("LD_LIBRARY_PATH"); old != "" {
		libs += ":" + old
	}
	// Keep variables the caller already set on cmd.Env (nil means "inherit").
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "LD_LIBRARY_PATH="+libs)
	dieWithParent(cmd)
}

// dieWithParent asks the kernel to SIGKILL the child when the app dies, so a
// crash or `kill -9` does not leave llama-server holding the port and the
// model's memory (Windows does the same with a job object). It must be set
// before cmd.Start.
//
// Pdeathsig fires when the OS thread that created the child exits, not only the
// process. That is safe here: supervisor.start runs on the main goroutine,
// which main.go locks to its OS thread with runtime.LockOSThread, and Go only
// ends a thread when a goroutine locked to it exits without unlocking.
func dieWithParent(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

// bindToParentLifetime is only needed on Windows (job objects); on Linux the
// same is done by dieWithParent before the child starts.
func bindToParentLifetime(cmd *exec.Cmd) {}

func runSetup(dir string) error {
	cmd := exec.Command("bash", "setup.sh", "--backend", "cpu")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
