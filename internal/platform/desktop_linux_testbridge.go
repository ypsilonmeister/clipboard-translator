//go:build linux && cgo && x11test

package platform

/*
#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/extensions/XTest.h>
#include "native_linux.h"

static GtkWidget *test_window, *test_entry;
// A selection change reaches the app as an event; once the server has answered
// a round trip, that event is queued and the next pump handles it while the
// focus is still where the selection was made.
static void test_sync(void) { XSync(GDK_DISPLAY_XDISPLAY(gdk_display_get_default()), False); }
static void test_create(void) {
    test_window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    test_entry = gtk_entry_new();
    gtk_container_add(GTK_CONTAINER(test_window), test_entry);
    gtk_widget_show_all(test_window);
    gtk_widget_grab_focus(test_entry);
    ct_pump();
    Display *d = GDK_DISPLAY_XDISPLAY(gdk_display_get_default());
    XSetInputFocus(d, GDK_WINDOW_XID(gtk_widget_get_window(test_window)), RevertToParent, CurrentTime);
    XSync(d, False);
    ct_pump();
}
static void test_destroy(void) { gtk_widget_destroy(test_window); ct_pump(); }
static void test_select(const char *text) {
    gtk_entry_set_text(GTK_ENTRY(test_entry), text);
    gtk_editable_select_region(GTK_EDITABLE(test_entry), 0, -1);
    test_sync();
    ct_pump();
}
static const char *test_text(void) { return gtk_entry_get_text(GTK_ENTRY(test_entry)); }

static GtkWidget *test_window_b, *test_entry_b;
static void test_create_b(void) {
    test_window_b = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    test_entry_b = gtk_entry_new();
    gtk_container_add(GTK_CONTAINER(test_window_b), test_entry_b);
    gtk_widget_show_all(test_window_b);
    gtk_widget_grab_focus(test_entry_b);
    ct_pump();
    Display *d = GDK_DISPLAY_XDISPLAY(gdk_display_get_default());
    XSetInputFocus(d, GDK_WINDOW_XID(gtk_widget_get_window(test_window_b)), RevertToParent, CurrentTime);
    XSync(d, False);
    ct_pump();
}
static void test_destroy_b(void) {
    if (test_window_b) {
        gtk_widget_destroy(test_window_b);
        test_window_b = test_entry_b = NULL;
        ct_pump();
    }
}
static void test_focus_b(void) {
    if (!test_window_b) return;
    gtk_widget_grab_focus(test_entry_b);
    ct_pump();
    Display *d = GDK_DISPLAY_XDISPLAY(gdk_display_get_default());
    XSetInputFocus(d, GDK_WINDOW_XID(gtk_widget_get_window(test_window_b)), RevertToParent, CurrentTime);
    XSync(d, False);
    ct_pump();
}
static void test_select_b(const char *text) {
    if (!test_entry_b) return;
    gtk_entry_set_text(GTK_ENTRY(test_entry_b), text);
    gtk_editable_select_region(GTK_EDITABLE(test_entry_b), 0, -1);
    test_sync();
    ct_pump();
}
static const char *test_text_b(void) {
    return test_entry_b ? gtk_entry_get_text(GTK_ENTRY(test_entry_b)) : "";
}

static void test_key(unsigned long sym, int down) {
    Display *d = GDK_DISPLAY_XDISPLAY(gdk_display_get_default());
    XTestFakeKeyEvent(d, XKeysymToKeycode(d, sym), down, 0);
    XSync(d, False);
}
static Display *conflict_display;
static void test_conflict_key(unsigned long sym, unsigned int mods, int enable) {
    if (enable) {
        if (!conflict_display) conflict_display = XOpenDisplay(NULL);
        XGrabKey(conflict_display, XKeysymToKeycode(conflict_display, sym), mods, DefaultRootWindow(conflict_display), False, GrabModeAsync, GrabModeAsync);
        XSync(conflict_display, False);
    } else if (conflict_display) {
        XCloseDisplay(conflict_display);
        conflict_display = NULL;
    }
}
static void test_conflict(int enable) {
    test_conflict_key(XK_e, ControlMask | Mod1Mask, enable);
}
static int test_forwarded_error = 0;
static int test_error_handler(Display *d, XErrorEvent *e) {
    test_forwarded_error = e->error_code;
    return 0;
}
static void test_install_forward_handler(void) {
    test_forwarded_error = 0;
    XSetErrorHandler(test_error_handler);
}
static int test_get_forwarded_error(void) {
    return test_forwarded_error;
}
static void test_set_forwarded_error(int err) {
    test_forwarded_error = err;
}
static void test_trigger_current_error_handler(int error_code) {
    XErrorEvent ev;
    memset(&ev, 0, sizeof(ev));
    ev.display = GDK_DISPLAY_XDISPLAY(gdk_display_get_default());
    ev.error_code = (unsigned char)error_code;
    XErrorHandler cur = XSetErrorHandler(NULL);
    XSetErrorHandler(cur);
    if (cur) cur(ev.display, &ev);
}

static void test_menu_click(int id) {
    // Use a real GtkMenuItem activation to exercise the native command queue.
    extern void ct_menu_add(void *, const char *, int, int, int, int, void *);
    GtkWidget *menu = ct_menu_new();
    ct_menu_add(menu, "Test command", id, 0, 0, 0, NULL);
    GList *children = gtk_container_get_children(GTK_CONTAINER(menu));
    gtk_menu_item_activate(GTK_MENU_ITEM(children->data));
    g_list_free(children);
    gtk_widget_destroy(menu);
}
*/
import "C"

func x11TestCreate()                   { C.test_create() }
func x11TestDestroy()                  { C.test_destroy() }
func x11TestSelect(s string)           { withCString(s, func(p *C.char) { C.test_select(p) }) }
func x11TestText() string              { return C.GoString(C.test_text()) }
func x11TestCreateB()                  { C.test_create_b() }
func x11TestDestroyB()                 { C.test_destroy_b() }
func x11TestFocusB()                   { C.test_focus_b() }
func x11TestSelectB(s string)          { withCString(s, func(p *C.char) { C.test_select_b(p) }) }
func x11TestTextB() string             { return C.GoString(C.test_text_b()) }
func x11TestKey(key uint64, down bool) { C.test_key(C.ulong(key), C.int(boolInt(down))) }
func x11TestMenuClick(id int)          { C.test_menu_click(C.int(id)) }
func x11TestConflict(enable bool)      { C.test_conflict(C.int(boolInt(enable))) }
func x11TestConflictKey(key uint64, mods uint32, enable bool) {
	C.test_conflict_key(C.ulong(key), C.uint(mods), C.int(boolInt(enable)))
}
func x11TestHotkeyGrab(id int, mods uint32, keycode uint32) int {
	return int(C.ct_hotkey_grab(C.int(id), C.uint(mods), C.uint(keycode)))
}
func x11TestHotkeyUngrab(id int) {
	C.ct_hotkey_ungrab(C.int(id))
}
func x11TestHotkeyPump() {
	C.ct_hotkey_pump()
}
func x11TestHotkeyCheck(id int) bool {
	return C.ct_hotkey_check(C.int(id)) != 0
}
func x11TestKeysymToKeycode(sym uint64) uint32 {
	return uint32(C.ct_keysym_to_keycode(C.ulong(sym)))
}
func x11TestSetKeyDown(id int, down bool) {
	C.ct_test_set_key_down(C.int(id), C.int(boolInt(down)))
}
func x11TestGetKeyDown(id int) int {
	return int(C.ct_test_get_key_down(C.int(id)))
}
func x11TestSetAutorepeatSupported(supported bool) {
	C.ct_set_autorepeat_supported(C.int(boolInt(supported)))
}
func x11TestInstallForwardHandler() {
	C.test_install_forward_handler()
}
func x11TestGetForwardedError() int {
	return int(C.test_get_forwarded_error())
}
func x11TestSetForwardedError(err int) {
	C.test_set_forwarded_error(C.int(err))
}
func x11TestTriggerCurrentErrorHandler(errorCode int) {
	C.test_trigger_current_error_handler(C.int(errorCode))
}
func x11TestSetPrevErrorHandler() {
	C.ct_test_set_prev_error_handler()
}
func x11TestCallGrabErrorHandler(matchesDisplay bool, requestCode int, errorCode int) int {
	return int(C.ct_test_call_grab_error_handler(C.int(boolInt(matchesDisplay)), C.int(requestCode), C.int(errorCode)))
}
func x11TestGetGrabError() int {
	return int(C.ct_test_get_grab_error())
}
func x11TestGetSlotVariants(id int) (int, []uint32) {
	var vars [8]C.uint
	n := int(C.ct_test_get_slot_variants(C.int(id), &vars[0], 8))
	if n < 0 {
		return -1, nil
	}
	res := make([]uint32, n)
	for i := 0; i < n; i++ {
		res[i] = uint32(vars[i])
	}
	return n, res
}
func x11TestInjectAutorepeatPair(id int, timestamp uint64) {
	C.ct_test_inject_autorepeat_pair(C.int(id), C.ulong(timestamp))
}
