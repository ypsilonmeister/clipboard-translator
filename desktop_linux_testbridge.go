//go:build linux && cgo && x11test

package main

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
