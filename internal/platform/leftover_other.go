//go:build !darwin

package platform

import "os/exec"

// Windows and Linux stop llama-server together with the app (job object,
// parent-death signal), so they need no record of it.

func recordChild(file string, cmd *exec.Cmd) {}
func forgetChild(file string)                {}
func stopLeftoverServer(file string)         {}
