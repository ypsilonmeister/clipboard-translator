//go:build !windows

package platform

import "errors"

func RecordedInstallLocation() string { return "" }

func RunInstaller(dir, tag, url string) error {
	return errors.New("the installer is only available on Windows")
}
