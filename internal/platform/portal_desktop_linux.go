//go:build linux && cgo

package platform

/*
#include <gtk/gtk.h>
#include "native_linux.h"
#include <gdk/gdkwayland.h>
static int ct_wayland_gtk_init(void) {
    gdk_set_allowed_backends("wayland");
    return gtk_init_check(NULL,NULL) && GDK_IS_WAYLAND_DISPLAY(gdk_display_get_default());
}
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

var activePortalDesktop *portalDesktop
var linuxWayland bool

func initializeWaylandGTK() bool { return C.ct_wayland_gtk_init() != 0 }

// Called after acquiring the single-instance lock; all portal ownership is UI-thread local.
func InitializeDesktop(dir string) error {
	if !linuxWayland {
		return nil
	}
	return initializePortalDesktop(dir, waylandAppID)
}

func initializePortalDesktop(dir, id string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share")
	}
	if err = installPortalDesktopEntryForID(data, exe, id); err != nil {
		return err
	}
	tokens, err := newPortalTokenStore(dir)
	if err != nil {
		return err
	}
	d := &portalDesktop{now: time.Now, tokens: tokens, appID: id, open: func() (portalDesktopWire, error) { return openGIOPortal() }}
	d.manualClipboard = &manualGTKClipboard{}
	d.showManual = func() { C.ct_show_control() }
	d.connect()
	activePortalDesktop = d
	return nil // connection errors are recoverable from the control window
}
func CloseDesktop() {
	if activePortalDesktop != nil {
		activePortalDesktop.Close()
		activePortalDesktop = nil
	}
}

// A disconnected runtime still supplies local hotkey handles, so retry never
// requires the application to replace its profile objects.
func (d *portalDesktop) newHotkey(spec string) (Hotkey, error) {
	if d.hotkeys == nil {
		// No native connection exists yet; registration is local until a retry.
		p := &portalClient{phase: "failed", now: d.now, err: errors.New("Wayland desktop connection is unavailable")}
		d.hotkeys = &portalHotkeys{client: p}
	}
	return d.hotkeys.NewHotkey(spec)
}
