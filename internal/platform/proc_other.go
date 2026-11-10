//go:build !windows && !linux && !darwin

package platform

import (
	"errors"
	"os/exec"
)

func HideConsole(cmd *exec.Cmd) {}

// BindToParentLifetime is only needed on Windows (job objects).
func BindToParentLifetime(cmd *exec.Cmd) {}

func RunSetup(dir string) error { return errors.New("setup is only available on Windows") }
