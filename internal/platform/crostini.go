package platform

import (
	"os"
	"runtime"
)

// evaluateCrostini is the single detection seam, evaluated at desktop initialization.
var evaluateCrostini = func() bool {
	return runtime.GOOS == "linux" && detectCrostini(os.Stat, os.LookupEnv)
}

var desktopCrostini bool

// detectCrostini looks for markers only the ChromeOS guest tools leave behind.
// The DMI product name is deliberately not used: crosvm also runs VMs outside
// ChromeOS and the name may differ between ChromeOS versions. The directory is
// the primary marker because, unlike the environment, every process in the
// container sees it, including ones started by autostart or a systemd user unit.
// /dev/.cros_milestone is not used: it does not exist inside the container.
func detectCrostini(stat func(string) (os.FileInfo, error), env func(string) (string, bool)) bool {
	if _, err := stat("/opt/google/cros-containers"); err == nil {
		return true
	}
	_, version := env("SOMMELIER_VERSION")
	_, identifier := env("SOMMELIER_VM_IDENTIFIER")
	return version || identifier
}
