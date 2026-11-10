//go:build linux && !cgo

package platform

import "errors"

func newWaylandPortal(string) (*portalClient, error) {
	return nil, errors.New("Wayland portal support requires a cgo desktop build")
}
