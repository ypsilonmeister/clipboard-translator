//go:build !darwin

package platform

import "os/exec"

// Windows and Linux stop llama-server together with the app (job object,
// parent-death signal), so they need no record of it.

func RecordChild(file string, cmd *exec.Cmd) {}
func ForgetChild(file string)                {}
func StopLeftoverServer(file string)         {}
