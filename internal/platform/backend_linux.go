package platform

import (
	"errors"
	"strings"
)

type linuxBackend int

const (
	linuxBackendX11 linuxBackend = iota
	linuxBackendWayland
)

// A Wayland session may also expose DISPLAY for Xwayland. Never mistake it for X11.
func selectLinuxBackend(display, session, wayland string) (linuxBackend, error) {
	if strings.EqualFold(session, "wayland") || wayland != "" {
		return linuxBackendWayland, nil
	}
	if display == "" {
		return linuxBackendX11, errors.New("an X11 desktop is required (DISPLAY is not set)")
	}
	return linuxBackendX11, nil
}
