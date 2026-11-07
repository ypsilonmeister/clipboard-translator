package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
	return cmd.Run()
}
