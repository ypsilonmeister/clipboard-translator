//go:build !windows && !linux && !darwin

package setup

import "runtime"

func currentPlatform() (platformSpec, error) {
	return platformSpec{}, errf("first-run setup is not supported on %s", runtime.GOOS)
}
