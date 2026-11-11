package platform

import (
	"os"
	"testing"
)

func TestCrostiniMarkers(t *testing.T) {
	for _, marker := range []string{"none", "dmi", "device", "env", "wrong-dmi"} {
		t.Run(marker, func(t *testing.T) {
			read := func(path string) ([]byte, error) {
				if path != "/sys/devices/virtual/dmi/id/product_name" {
					t.Fatal(path)
				}
				if marker == "dmi" {
					return []byte("crosvm\n"), nil
				}
				return []byte("ordinary PC"), nil
			}
			stat := func(path string) (os.FileInfo, error) {
				if path != "/dev/.cros_milestone" {
					t.Fatal(path)
				}
				if marker == "device" {
					return nil, nil
				}
				return nil, os.ErrNotExist
			}
			env := func(key string) (string, bool) {
				if key != "SOMMELIER_VERSION" {
					t.Fatal(key)
				}
				return "", marker == "env"
			}
			want := marker == "dmi" || marker == "device" || marker == "env"
			if got := detectCrostini(read, stat, env); got != want {
				t.Fatalf("got %v want %v", got, want)
			}
		})
	}
}
