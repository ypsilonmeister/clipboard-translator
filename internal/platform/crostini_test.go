package platform

import (
	"os"
	"testing"
)

func TestCrostiniMarkers(t *testing.T) {
	for _, tc := range []struct {
		marker string
		want   bool
	}{
		{"none", false},
		{"/dev/.cros_milestone", false}, // absent inside the container, never a signal
		{"/sys/devices/virtual/dmi/id/product_name", false},
		{"/opt/google/cros-containers", true},
		{"SOMMELIER_VERSION", true},
		{"SOMMELIER_VM_IDENTIFIER", true},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			stat := func(path string) (os.FileInfo, error) {
				if path == tc.marker {
					return nil, nil
				}
				return nil, os.ErrNotExist
			}
			env := func(key string) (string, bool) { return "", key == tc.marker }
			if got := detectCrostini(stat, env); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// The markers seen on a real Chromebook (penguin container): the directory and
// both SOMMELIER variables exist, /dev/.cros_milestone does not. A process
// started without the environment (autostart, systemd user unit) must still match.
func TestCrostiniMarkersOnRealChromebook(t *testing.T) {
	onlyDirectory := func(path string) (os.FileInfo, error) {
		if path == "/opt/google/cros-containers" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	if !detectCrostini(onlyDirectory, func(string) (string, bool) { return "", false }) {
		t.Fatal("the directory alone must identify Crostini, even without the environment")
	}
	env := func(key string) (string, bool) {
		return "x", key == "SOMMELIER_VERSION" || key == "SOMMELIER_VM_IDENTIFIER"
	}
	if !detectCrostini(func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, env) {
		t.Fatal("the SOMMELIER variables must identify Crostini without the directory")
	}
}
