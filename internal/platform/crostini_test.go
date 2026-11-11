package platform

import (
	"os"
	"testing"
)

func TestCrostiniMarkers(t *testing.T) {
	for _, dmi := range []string{"crosvm\n", "ordinary PC", ""} {
		for _, marker := range []string{"none", "/dev/.cros_milestone", "/opt/google/cros-containers", "SOMMELIER_VERSION", "SOMMELIER_VM_IDENTIFIER"} {
			t.Run(dmi+"/"+marker, func(t *testing.T) {
				read := func(path string) ([]byte, error) {
					if path != "/sys/devices/virtual/dmi/id/product_name" {
						t.Fatal(path)
					}
					return []byte(dmi), nil
				}
				stat := func(path string) (os.FileInfo, error) {
					if path == marker {
						return nil, nil
					}
					return nil, os.ErrNotExist
				}
				env := func(key string) (string, bool) { return "", key == marker }
				want := dmi == "crosvm\n" && marker != "none"
				if got := detectCrostini(read, stat, env); got != want {
					t.Fatalf("got %v want %v", got, want)
				}
			})
		}
	}
}
