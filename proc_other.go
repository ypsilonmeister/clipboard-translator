//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

func hideConsole(cmd *exec.Cmd) {}

func runSetup(dir string) error { return errors.New("setup is only available on Windows") }
