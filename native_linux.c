//go:build linux && cgo

#include "native_linux.h"
#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/XKBlib.h>
#include <X11/extensions/XTest.h>

static Display *display;
static unsigned int grab_mods, num_mask;
static KeyCode grab_key;
static int key_down, fired, grab_error;
static GtkStatusIcon *status_icon;
static GtkWidget *current_menu, *control_window, *status_label;
static int menu_requested;
static GQueue commands = G_QUEUE_INIT;

int ct_init(void) {
    if (display) return 1;
    if (!gtk_init_check(NULL, NULL)) return 0;
    if (!GDK_IS_X11_DISPLAY(gdk_display_get_default())) return 0;
    display = XOpenDisplay(NULL);
    if (display)
        g_signal_connect(gtk_clipboard_get(GDK_SELECTION_PRIMARY), "owner-change", G_CALLBACK(primary_owner_change), NULL);
    return display != NULL;
}

static int grab_error_handler(Display *d, XErrorEvent *e) {
    grab_error = e->error_code;
    return 0;
}

static void grab_variants(int grab) {
    unsigned int locks[] = {0, LockMask, num_mask, LockMask | num_mask};
    for (int i = 0; i < 4; i++) {
        if (grab) XGrabKey(display, grab_key, grab_mods | locks[i], DefaultRootWindow(display), False, GrabModeAsync, GrabModeAsync);
        else XUngrabKey(display, grab_key, grab_mods | locks[i], DefaultRootWindow(display));
    }
}

void ct_ungrab(void) {
    if (!display || !grab_key) return;
    grab_variants(0);
    XSync(display, False);
    grab_key = 0;
    key_down = fired = 0;
    // Discard events for the old binding before registering its replacement.
    while (XPending(display)) { XEvent e; XNextEvent(display, &e); }
}

int ct_grab(unsigned int mods, unsigned long key) {
    if (!ct_init()) return 0;
    ct_ungrab();
    grab_mods = mods;
    grab_key = XKeysymToKeycode(display, key);
    if (!grab_key) return 0;
    num_mask = 0;
    KeyCode num = XKeysymToKeycode(display, XK_Num_Lock);
    XModifierKeymap *map = XGetModifierMapping(display);
    if (map) {
        for (int m = 0; m < 8; m++)
            for (int k = 0; k < map->max_keypermod; k++)
                if (map->modifiermap[m * map->max_keypermod + k] == num) num_mask |= 1U << m;
        XFreeModifiermap(map);
    }
    Bool supported;
    XkbSetDetectableAutoRepeat(display, True, &supported);
    XSync(display, False);
    int (*previous)(Display *, XErrorEvent *) = XSetErrorHandler(grab_error_handler);
    grab_error = 0;
    grab_variants(1);
    XSync(display, False);
    if (grab_error) { grab_variants(0); XSync(display, False); grab_key = 0; }
    XSetErrorHandler(previous);
    return grab_error == 0;
}

int ct_hotkey(void) {
    if (!display) return 0;
    while (XPending(display)) {
        XEvent e;
        XNextEvent(display, &e);
        if (e.xkey.keycode != grab_key) continue;
        if (e.type == KeyPress && !key_down) { fired = 1; key_down = 1; }
        if (e.type == KeyRelease) key_down = 0;
    }
    int result = fired;
    fired = 0;
    return result;
}

unsigned long ct_focus(void) {
    if (!display) return 0;
    Window focus;
    int revert;
    XGetInputFocus(display, &focus, &revert);
    return focus == None || focus == PointerRoot ? 0 : focus;
}

int ct_modifiers(void) {
    if (!display) return 0;
    char keys[32];
    XQueryKeymap(display, keys);
    KeySym mods[] = {XK_Control_L, XK_Control_R, XK_Alt_L, XK_Alt_R, XK_Shift_L, XK_Shift_R, XK_Super_L, XK_Super_R, XK_ISO_Level3_Shift};
    for (unsigned int i = 0; i < sizeof(mods)/sizeof(mods[0]); i++) {
        KeyCode k = XKeysymToKeycode(display, mods[i]);
        if (k && (keys[k / 8] & (1 << (k % 8)))) return 1;
    }
    return 0;
}

int ct_paste(void) {
    if (!display) return 0;
    int event, error, major, minor;
    if (!XTestQueryExtension(display, &event, &error, &major, &minor)) return 0;
    KeyCode ctrl = XKeysymToKeycode(display, XK_Control_L), v = XKeysymToKeycode(display, XK_v);
    if (!ctrl || !v) return 0;
    XTestFakeKeyEvent(display, ctrl, True, 0);
    XTestFakeKeyEvent(display, v, True, 0);
    XTestFakeKeyEvent(display, v, False, 0);
    XTestFakeKeyEvent(display, ctrl, False, 0);
    XSync(display, False);
    return 1;
}

// The window that had the focus when PRIMARY last got a new owner, or 0 when it
// has none. GTK owns selections through a hidden window shared by all of a
// process's windows, and other toolkits do the same, so the owner window cannot
// tell which window the selection belongs to; where the user was when it was
// made can.
static unsigned long primary_window;

static void primary_owner_change(GtkClipboard *clipboard, GdkEventOwnerChange *event, gpointer data) {
    (void)clipboard;
    (void)data;
    primary_window = event->reason == GDK_OWNER_CHANGE_NEW_OWNER ? ct_focus() : 0;
}

// Reports whether PRIMARY was last changed while win had the focus, so it is a
// selection made there and not one left over from another window.
int ct_primary_selected_in(unsigned long win) {
    if (!ct_init() || !win) return 0;
    ct_pump(); // owner changes that arrived since the last pump
    return primary_window == win;
}

char *ct_read(int primary) {
    if (!ct_init()) return NULL;
    return gtk_clipboard_wait_for_text(gtk_clipboard_get(primary ? GDK_SELECTION_PRIMARY : GDK_SELECTION_CLIPBOARD));
}
void ct_free(void *p) { g_free(p); }
int ct_write(const char *text) {
    if (!ct_init()) return 0;
    gtk_clipboard_set_text(gtk_clipboard_get(GDK_SELECTION_CLIPBOARD), text, -1);
    return 1;
}

static void request_menu(GtkStatusIcon *icon, guint button, guint time, gpointer data) { menu_requested = 1; }
static void activate_icon(GtkStatusIcon *icon, gpointer data) { menu_requested = 1; }
static void control_clicked(GtkButton *button, gpointer data) { menu_requested = 1; }
static gboolean close_control(GtkWidget *w, GdkEvent *event, gpointer data) {
    g_queue_push_tail(&commands, GINT_TO_POINTER(2));
    return TRUE;
}
static gboolean check_tray(gpointer data) {
    // Some desktops do not host legacy X11 tray icons. Keep settings and Quit reachable.
    if (status_icon && !gtk_status_icon_is_embedded(status_icon)) {
        control_window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
        gtk_window_set_title(GTK_WINDOW(control_window), "Clipboard Translator");
        gtk_container_set_border_width(GTK_CONTAINER(control_window), 16);
        GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
        status_label = gtk_label_new("Clipboard Translator");
        GtkWidget *button = gtk_button_new_with_label("Open menu");
        gtk_box_pack_start(GTK_BOX(box), status_label, TRUE, TRUE, 0);
        gtk_box_pack_start(GTK_BOX(box), button, FALSE, FALSE, 0);
        gtk_container_add(GTK_CONTAINER(control_window), box);
        g_signal_connect(button, "clicked", G_CALLBACK(control_clicked), NULL);
        g_signal_connect(control_window, "delete-event", G_CALLBACK(close_control), NULL);
        gtk_widget_show_all(control_window);
    }
    return G_SOURCE_REMOVE;
}
static guint tray_check;
void ct_tray_create(const char *icon) {
    status_icon = gtk_status_icon_new_from_file(icon);
    gtk_status_icon_set_title(status_icon, "Clipboard Translator");
    gtk_status_icon_set_visible(status_icon, TRUE);
    g_signal_connect(status_icon, "popup-menu", G_CALLBACK(request_menu), NULL);
    g_signal_connect(status_icon, "activate", G_CALLBACK(activate_icon), NULL);
    tray_check = g_timeout_add(2000, check_tray, NULL);
}
void ct_tray_close(void) {
    if (tray_check) { GSource *s = g_main_context_find_source_by_id(NULL, tray_check); if (s) g_source_destroy(s); tray_check = 0; }
    if (current_menu) { gtk_widget_destroy(current_menu); current_menu = NULL; }
    if (control_window) { gtk_widget_destroy(control_window); control_window = status_label = NULL; }
    if (status_icon) { g_object_unref(status_icon); status_icon = NULL; }
    g_queue_clear(&commands);
    menu_requested = 0;
}
void ct_status(const char *text) {
    if (status_icon) gtk_status_icon_set_tooltip_text(status_icon, text);
    if (status_label) gtk_label_set_text(GTK_LABEL(status_label), text);
}
void ct_pump(void) { while (gtk_events_pending()) gtk_main_iteration_do(FALSE); }
int ct_wants_menu(void) { int r = menu_requested; menu_requested = 0; return r; }
void *ct_menu_new(void) { return gtk_menu_new(); }
static void menu_activate(GtkMenuItem *item, gpointer data) { g_queue_push_tail(&commands, data); }
void ct_menu_add(void *menu, const char *label, int id, int checked, int disabled, int separator, void *sub) {
    GtkWidget *item;
    if (separator) item = gtk_separator_menu_item_new();
    else if (checked) { item = gtk_check_menu_item_new_with_label(label); gtk_check_menu_item_set_active(GTK_CHECK_MENU_ITEM(item), TRUE); }
    else item = gtk_menu_item_new_with_label(label);
    gtk_widget_set_sensitive(item, !disabled);
    if (sub) gtk_menu_item_set_submenu(GTK_MENU_ITEM(item), GTK_WIDGET(sub));
    else if (id) g_signal_connect(item, "activate", G_CALLBACK(menu_activate), GINT_TO_POINTER(id));
    gtk_menu_shell_append(GTK_MENU_SHELL(menu), item);
}
void ct_menu_popup(void *menu) {
    if (current_menu) gtk_widget_destroy(current_menu);
    current_menu = GTK_WIDGET(menu);
    gtk_widget_show_all(current_menu);
    gtk_menu_popup(GTK_MENU(current_menu), NULL, NULL, gtk_status_icon_position_menu, status_icon, 0, gtk_get_current_event_time());
}
int ct_command(void) { return GPOINTER_TO_INT(g_queue_pop_head(&commands)); }
int ct_dialog(const char *text, int question, int error) {
    GtkWidget *d = gtk_message_dialog_new(NULL, GTK_DIALOG_MODAL, question ? GTK_MESSAGE_QUESTION : error ? GTK_MESSAGE_ERROR : GTK_MESSAGE_INFO, question ? GTK_BUTTONS_YES_NO : GTK_BUTTONS_OK, "%s", text);
    gtk_window_set_title(GTK_WINDOW(d), "Clipboard Translator");
    int result = gtk_dialog_run(GTK_DIALOG(d));
    gtk_widget_destroy(d);
    return result == GTK_RESPONSE_YES;
}
