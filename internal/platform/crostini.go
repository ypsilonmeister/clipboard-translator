package platform

import (
	"os"
	"runtime"
	"strings"
)

// IsCrostini uses markers also available outside a graphical session.
func IsCrostini() bool {
	return runtime.GOOS == "linux" && detectCrostini(os.ReadFile, os.Stat, os.LookupEnv)
}
func detectCrostini(read func(string) ([]byte, error), stat func(string) (os.FileInfo, error), env func(string) (string, bool)) bool {
	b, _ := read("/sys/devices/virtual/dmi/id/product_name")
	if strings.TrimSpace(string(b)) == "crosvm" {
		return true
	}
	if _, err := stat("/dev/.cros_milestone"); err == nil {
		return true
	}
	_, ok := env("SOMMELIER_VERSION")
	return ok
}
