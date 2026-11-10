//go:build linux && cgo

package platform

import "testing"

func TestX11EnvironmentValidation(t *testing.T) {
	for _, tc := range []struct {
		display, session, wayland string
		ok                        bool
	}{
		{":1", "x11", "", true}, {":1", "", "", true},
		{"", "x11", "", false}, {":1", "wayland", "", false}, {":1", "x11", "wayland-0", false},
	} {
		if err := x11EnvironmentError(tc.display, tc.session, tc.wayland); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
