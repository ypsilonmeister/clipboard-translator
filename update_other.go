//go:build !windows

package main

import "errors"

func recordedInstallLocation() string { return "" }

func runInstaller(dir, tag string) error {
	return errors.New("the installer is only available on Windows")
}
