package platform

import "testing"

func TestLinuxBackendSelection(t *testing.T) {
	for _, tc := range []struct {
		display, session, wayland string
		want                      linuxBackend
		valid                     bool
	}{
		{":1", "x11", "", linuxBackendX11, true},
		{":1", "", "", linuxBackendX11, true},
		{"", "x11", "", linuxBackendX11, false},
		{"", "wayland", "wayland-0", linuxBackendWayland, true},
		{":1", "WAYLAND", "", linuxBackendWayland, true},
		{":1", "x11", "wayland-0", linuxBackendWayland, true},
	} {
		got, err := selectLinuxBackend(tc.display, tc.session, tc.wayland)
		if got != tc.want || (err == nil) != tc.valid {
			t.Errorf("%+v: got %v, %v", tc, got, err)
		}
	}
}
