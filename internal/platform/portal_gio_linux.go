//go:build linux && cgo

package platform

/*
#cgo pkg-config: gio-2.0 gio-unix-2.0
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
	kinds := []string{"", "registered", "response", "closed", "failed", "activated", "deactivated", "shortcuts-changed", "method", "selection-owner", "selection-transfer", "read", "written", "capability"}
	kind := "failed"
	if int(e.kind) > 0 && int(e.kind) < len(kinds) {
		kind = kinds[int(e.kind)]
	}
	event := portalEvent{kind: kind, token: C.GoString(e.token), session: C.GoString(e.session), code: uint32(e.code), id: C.GoString(e.id)}
	event.devices = uint32(e.devices)
	event.clipboard = e.clipboard != 0
	event.owner = e.owner != 0
	event.restore = C.GoString(e.restore)
	event.mime = C.GoString(e.mime)
	event.serial = uint32(e.serial)
	if e.length < 0 || int(e.length) > portalTextLimit {
		return portalEvent{kind: "failed"}, true
	}
	if e.length > 0 {
		event.data = C.GoBytes(e.data, C.int(e.length))
	}
	count := int(C.ct_portal_binding_count(&e))
	if count > maxHotkeys {
		return portalEvent{kind: "failed"}, true
	}
	for i := 0; i < count; i++ {
		id, trigger := C.ct_portal_binding_id(&e, C.int(i)), C.ct_portal_binding_trigger(&e, C.int(i))
		event.bindings = append(event.bindings, portalBinding{id: C.GoString(id), trigger: C.GoString(trigger)})
		C.free(unsafe.Pointer(id))
		C.free(unsafe.Pointer(trigger))
	}
	return event, true
}
func (w *gioPortalWire) Close() {
	if w.native != nil {
		C.ct_portal_close(w.native)
		w.native = nil
	}
}

func pumpPortalCallbacks() { C.ct_portal_dispatch() }

func (w *gioPortalWire) Probe(iface string) {
	if w.native != nil {
		p := C.CString(iface)
		defer C.free(unsafe.Pointer(p))
		C.ct_portal_probe(w.native, p)
	}
}

func (w *gioPortalWire) BindShortcuts(session, token string, bindings []portalBinding) {
	if w.native == nil {
		return
	}
	a, b := C.CString(session), C.CString(token)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(b))
	storage := C.calloc(C.size_t(len(bindings)), C.size_t(C.sizeof_CTShortcut))
	defer C.free(storage)
	items := unsafe.Slice((*C.CTShortcut)(storage), len(bindings))
	for i, x := range bindings {
		items[i].id = C.CString(x.id)
		items[i].description = C.CString(x.description)
		items[i].trigger = C.CString(x.trigger)
		defer C.free(unsafe.Pointer(items[i].id))
		defer C.free(unsafe.Pointer(items[i].description))
		defer C.free(unsafe.Pointer(items[i].trigger))
	}
	C.ct_portal_bind_shortcuts(w.native, a, b, (*C.CTShortcut)(storage), C.int(len(bindings)))
}
func (w *gioPortalWire) CloseSession(session string) {
	if w.native != nil {
		p := C.CString(session)
		defer C.free(unsafe.Pointer(p))
		C.ct_portal_close_session(w.native, p)
	}
}
func (w *gioPortalWire) CloseRequest(token string) {
	if w.native != nil {
		p := C.CString(token)
		defer C.free(unsafe.Pointer(p))
		C.ct_portal_close_request(w.native, p)
	}
}

func (w *gioPortalWire) SelectDevices(session, token, restore string) {
	if w.native != nil {
		a, b, c := C.CString(session), C.CString(token), C.CString(restore)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		defer C.free(unsafe.Pointer(c))
		C.ct_portal_select_devices(w.native, a, b, c)
	}
}
func (w *gioPortalWire) RequestClipboard(session, token string) {
	if w.native != nil {
		a, b := C.CString(session), C.CString(token)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		C.ct_portal_request_clipboard(w.native, a, b)
	}
}
func (w *gioPortalWire) StartRemote(session, token string) {
	if w.native != nil {
		a, b := C.CString(session), C.CString(token)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		C.ct_portal_start_remote(w.native, a, b)
	}
}
func (w *gioPortalWire) SetSelection(session, token string) {
	if w.native != nil {
		a, b := C.CString(session), C.CString(token)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		C.ct_portal_set_selection(w.native, a, b)
	}
}
func (w *gioPortalWire) ReadSelection(session, token, mime string) {
	if w.native != nil {
		a, b, c := C.CString(session), C.CString(token), C.CString(mime)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		defer C.free(unsafe.Pointer(c))
		C.ct_portal_read_selection(w.native, a, b, c)
	}
}
func (w *gioPortalWire) WriteSelection(session, token string, serial uint32, data []byte) {
	if w.native != nil {
		a, b := C.CString(session), C.CString(token)
		p := C.CBytes(data)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		defer C.free(p)
		C.ct_portal_write_selection(w.native, a, b, C.uint32_t(serial), p, C.int(len(data)))
	}
}
func (w *gioPortalWire) WriteDone(session string, serial uint32, success bool) {
	if w.native != nil {
		a := C.CString(session)
		defer C.free(unsafe.Pointer(a))
		C.ct_portal_write_done(w.native, a, C.uint32_t(serial), C.int(boolInt(success)))
	}
}
func (w *gioPortalWire) CancelIO(token string) {
	if w.native != nil {
		a := C.CString(token)
		defer C.free(unsafe.Pointer(a))
		C.ct_portal_cancel_io(w.native, a)
	}
}
func (w *gioPortalWire) Key(session, token string, sym int, down bool) {
	if w.native != nil {
		a, b := C.CString(session), C.CString(token)
		defer C.free(unsafe.Pointer(a))
		defer C.free(unsafe.Pointer(b))
		C.ct_portal_key(w.native, a, b, C.int(sym), C.int(boolInt(down)))
	}
}
