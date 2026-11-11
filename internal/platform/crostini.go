package platform

import (
	"os"
	"runtime"
	"strings"
)

// evaluateCrostini is the single detection seam, evaluated at desktop initialization.
var evaluateCrostini = func() bool {
	return runtime.GOOS == "linux" && detectCrostini(os.ReadFile, os.Stat, os.LookupEnv)
}

var desktopCrostini bool

func detectCrostini(read func(string) ([]byte, error), stat func(string) (os.FileInfo, error), env func(string) (string, bool)) bool {
	b, _ := read("/sys/devices/virtual/dmi/id/product_name")
	if strings.TrimSpace(string(b)) != "crosvm" {
		return false
	}
	if _, err := stat("/dev/.cros_milestone"); err == nil {
		return true
	}
	if _, err := stat("/opt/google/cros-containers"); err == nil {
		return true
	}
	_, version := env("SOMMELIER_VERSION")
	_, identifier := env("SOMMELIER_VM_IDENTIFIER")
	return version || identifier
}
