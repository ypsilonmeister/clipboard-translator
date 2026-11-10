//go:build linux && cgo

package platform

/*
#cgo pkg-config: gio-2.0
#include <stdlib.h>
#include "portal_native_linux.h"
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

type gioPortalWire struct{ native *C.CTPortal }

// Called during Wayland startup on the locked UI thread, before entering serve.
// W1 does not yet connect this to PlatformRuntimeError (W2-W4 supply the UX).
func newWaylandPortal(appDir string) (*portalClient, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err = installPortalDesktopEntry(dataHome, exe); err != nil {
		return nil, err
	}
	store, err := newPortalTokenStore(appDir)
	if err != nil {
		return nil, err
	}
	if _, err = store.Load(); err != nil {
		return nil, err
	}
	wire, err := openGIOPortal()
	if err != nil {
		return nil, err
	}
	p := startPortalClient(wire, waylandAppID, time.Now)
	p.tokens = store
	return p, nil
}

func openGIOPortal() (*gioPortalWire, error) {
	native := C.ct_portal_open()
	if native == nil {
		return nil, errors.New("could not connect to the desktop session bus; start Clipboard Translator in a graphical session")
	}
	return &gioPortalWire{native}, nil
}
func (w *gioPortalWire) Register(id string) {
	if w.native == nil {
		return
	}
	p := C.CString(id)
	defer C.free(unsafe.Pointer(p))
	C.ct_portal_register(w.native, p)
}
func (w *gioPortalWire) CreateSession(iface, token, session string) {
	if w.native == nil {
		return
	}
	a, b, c := C.CString(iface), C.CString(token), C.CString(session)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(b))
	defer C.free(unsafe.Pointer(c))
	C.ct_portal_create_session(w.native, a, b, c)
}
func (w *gioPortalWire) Poll() (portalEvent, bool) {
	if w.native == nil {
		return portalEvent{}, false
	}
	var e C.CTPortalEvent
	if C.ct_portal_poll(w.native, &e) == 0 {
		return portalEvent{}, false
	}
	defer C.ct_portal_event_free(&e)
	kinds := []string{"", "registered", "response", "closed", "failed"}
	kind := "failed"
	if int(e.kind) > 0 && int(e.kind) < len(kinds) {
		kind = kinds[int(e.kind)]
	}
	return portalEvent{kind: kind, token: C.GoString(e.token), session: C.GoString(e.session), code: uint32(e.code)}, true
}
func (w *gioPortalWire) Close() {
	if w.native != nil {
		C.ct_portal_close(w.native)
		w.native = nil
	}
}

func pumpPortalCallbacks() { C.ct_portal_dispatch() }
