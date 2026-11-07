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
    ct_pump();
}
static const char *test_text(void) { return gtk_entry_get_text(GTK_ENTRY(test_entry)); }
static void test_key(unsigned long sym, int down) {
    Display *d = GDK_DISPLAY_XDISPLAY(gdk_display_get_default());
    XTestFakeKeyEvent(d, XKeysymToKeycode(d, sym), down, 0);
    XSync(d, False);
}
static Display *conflict_display;
static void test_conflict(int enable) {
    if (enable) {
        conflict_display = XOpenDisplay(NULL);
        XGrabKey(conflict_display, XKeysymToKeycode(conflict_display, XK_e), ControlMask | Mod1Mask, DefaultRootWindow(conflict_display), False, GrabModeAsync, GrabModeAsync);
        XSync(conflict_display, False);
    } else if (conflict_display) {
        XCloseDisplay(conflict_display);
        conflict_display = NULL;
    }
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
func x11TestKey(key uint64, down bool) { C.test_key(C.ulong(key), C.int(boolInt(down))) }
func x11TestMenuClick(id int)          { C.test_menu_click(C.int(id)) }
func x11TestConflict(enable bool)      { C.test_conflict(C.int(boolInt(enable))) }
