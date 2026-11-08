package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func hideConsole(cmd *exec.Cmd) {
	// Official archives ship shared libraries beside the server (or in lib/).
	dir := filepath.Dir(cmd.Path)
	libs := dir + ":" + filepath.Join(dir, "lib")
	if old := os.Getenv("LD_LIBRARY_PATH"); old != "" {
		libs += ":" + old
	}
	// Keep what the caller already put in cmd.Env (the server's API key).
	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = append(base, "LD_LIBRARY_PATH="+libs)
}

// bindToParentLifetime is only needed on Windows (job objects).
func bindToParentLifetime(cmd *exec.Cmd) {}

func runSetup(dir string) error {
	cmd := exec.Command("bash", "setup.sh", "--backend", "cpu")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
