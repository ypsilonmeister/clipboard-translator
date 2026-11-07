//go:build !windows && !linux && !darwin

package main

import (
	"errors"
	"os/exec"
)

func hideConsole(cmd *exec.Cmd) {}

// bindToParentLifetime is only needed on Windows (job objects).
func bindToParentLifetime(cmd *exec.Cmd) {}

func runSetup(dir string) error { return errors.New("setup is only available on Windows") }
