//go:build linux && cgo

#include "native_linux.h"
#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <X11/Xlib.h>
#include <X11/Xproto.h>
#include <X11/keysym.h>
#include <X11/XKBlib.h>
#include <X11/extensions/XTest.h>
#include <string.h>

#define MAX_HOTKEYS 8

typedef struct {
    int id;                   // 1..MAX_HOTKEYS (0 means unused)
    unsigned int mods;        // normalized modifiers
    KeyCode keycode;          // X11 keycode
    int key_down;             // 1 if currently held down
    int fired;                // 1 if pressed and waiting for check
    unsigned int variants[8]; // lock mask variants grabbed
    int nvar;                 // number of variants grabbed
} HotkeyGrab;

static Display *display;
static HotkeyGrab grabs[MAX_HOTKEYS];
static unsigned int num_mask, scroll_mask;
static int grab_error;
static XErrorHandler prev_error_handler;
static Bool xkb_autorepeat_supported;

static void primary_owner_change(GtkClipboard *clipboard, GdkEventOwnerChange *event, gpointer data);
static int grab_error_handler(Display *d, XErrorEvent *e);

int ct_init(void) {
    if (display) return 1;
    if (!ct_gtk_init()) return 0;
    if (!GDK_IS_X11_DISPLAY(gdk_display_get_default())) return 0;
    display = XOpenDisplay(NULL);
    if (display) {
        g_signal_connect(gtk_clipboard_get(GDK_SELECTION_PRIMARY), "owner-change", G_CALLBACK(primary_owner_change), NULL);
        XkbSetDetectableAutoRepeat(display, True, &xkb_autorepeat_supported);
    }
    return display != NULL;
}

void ct_set_autorepeat_supported(int supported) {
    xkb_autorepeat_supported = supported ? True : False;
}

void ct_test_set_key_down(int id, int down) {
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            grabs[i].key_down = down;
            break;
        }
    }
}

int ct_test_get_key_down(int id) {
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            return grabs[i].key_down;
        }
    }
    return -1;
}

int ct_test_call_grab_error_handler(int matches_display, int request_code, int error_code) {
    XErrorEvent ev;
    memset(&ev, 0, sizeof(ev));
    ev.display = matches_display ? display : (Display *)0xdeadbeef;
    ev.request_code = (unsigned char)request_code;
    ev.error_code = (unsigned char)error_code;
    return grab_error_handler(ev.display, &ev);
}

void ct_test_set_prev_error_handler(void) {
    XErrorHandler cur = XSetErrorHandler(NULL);
    XSetErrorHandler(cur);
    prev_error_handler = cur;
}

int ct_test_get_grab_error(void) {
    return grab_error;
}

int ct_test_get_slot_variants(int id, unsigned int *variants, int max_var) {
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            int count = grabs[i].nvar;
            if (count > max_var) count = max_var;
            for (int v = 0; v < count; v++) {
                variants[v] = grabs[i].variants[v];
            }
            return grabs[i].nvar;
        }
    }
    return -1;
}

void ct_test_inject_autorepeat_pair(int id, unsigned long timestamp) {
    if (!display || !id) return;
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            XEvent ev;
            memset(&ev, 0, sizeof(ev));
            ev.type = KeyPress;
            ev.xkey.type = KeyPress;
            ev.xkey.display = display;
            ev.xkey.window = DefaultRootWindow(display);
            ev.xkey.root = DefaultRootWindow(display);
            ev.xkey.keycode = grabs[i].keycode;
            ev.xkey.state = grabs[i].mods;
            ev.xkey.time = (Time)timestamp;
            ev.xkey.same_screen = True;
            XPutBackEvent(display, &ev);

            ev.type = KeyRelease;
            ev.xkey.type = KeyRelease;
            XPutBackEvent(display, &ev);
            break;
        }
    }
}


static void update_lock_masks(void) {
    num_mask = 0;
    scroll_mask = 0;
    KeyCode num = XKeysymToKeycode(display, XK_Num_Lock);
    KeyCode scroll = XKeysymToKeycode(display, XK_Scroll_Lock);
    XModifierKeymap *map = XGetModifierMapping(display);
    if (map) {
        for (int m = 0; m < 8; m++) {
            for (int k = 0; k < map->max_keypermod; k++) {
                KeyCode code = map->modifiermap[m * map->max_keypermod + k];
                if (num && code == num) num_mask |= 1U << m;
                if (scroll && code == scroll) scroll_mask |= 1U << m;
            }
        }
        XFreeModifiermap(map);
    }
}

static int get_lock_variants(unsigned int *variants, int max_variants) {
    unsigned int masks[3];
    int mask_count = 0;
    masks[mask_count++] = LockMask;
    if (num_mask && num_mask != LockMask) {
        masks[mask_count++] = num_mask;
    }
    if (scroll_mask && scroll_mask != LockMask && scroll_mask != num_mask) {
        masks[mask_count++] = scroll_mask;
    }

    int total = 1 << mask_count;
    if (total > max_variants) total = max_variants;

    for (int i = 0; i < total; i++) {
        unsigned int var = 0;
        for (int m = 0; m < mask_count; m++) {
            if (i & (1 << m)) {
                var |= masks[m];
            }
        }
        variants[i] = var;
    }
    return total;
}

static int grab_error_handler(Display *d, XErrorEvent *e) {
    if (d == display && e->request_code == X_GrabKey && e->error_code == BadAccess) {
        grab_error = BadAccess;
        return 0;
    }
    if (prev_error_handler) {
        return prev_error_handler(d, e);
    }
    return 0;
}

int ct_keysym_to_keycode(unsigned long keysym) {
    if (!ct_init()) return 0;
    return (int)XKeysymToKeycode(display, (KeySym)keysym);
}

void ct_hotkey_ungrab(int id) {
    if (!display || !id) return;
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            for (int v = 0; v < grabs[i].nvar; v++) {
                XUngrabKey(display, grabs[i].keycode, grabs[i].mods | grabs[i].variants[v], DefaultRootWindow(display));
            }
            XSync(display, False);
            ct_hotkey_pump();
            grabs[i].id = 0;
            grabs[i].keycode = 0;
            grabs[i].mods = 0;
            grabs[i].key_down = 0;
            grabs[i].fired = 0;
            grabs[i].nvar = 0;
            for (int v = 0; v < 8; v++) grabs[i].variants[v] = 0;
            break;
        }
    }
}

void ct_ungrab(void) {
    if (!display) return;
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id) {
            ct_hotkey_ungrab(grabs[i].id);
        }
    }
    while (XPending(display)) { XEvent e; XNextEvent(display, &e); }
}

int ct_hotkey_grab(int id, unsigned int mods, unsigned int keycode) {
    if (!ct_init()) return 0;
    if (!keycode || !id) return 0;

    int existing_slot = -1;
    int free_slot = -1;
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            existing_slot = i;
        } else if (grabs[i].id != 0) {
            if (grabs[i].keycode == (KeyCode)keycode && grabs[i].mods == mods) {
                return -2; // duplicate with another registered hotkey
            }
        } else if (free_slot < 0) {
            free_slot = i;
        }
    }
    int slot = (existing_slot >= 0) ? existing_slot : free_slot;
    if (slot < 0) return -1; // table full

    if (existing_slot >= 0 && grabs[existing_slot].keycode == (KeyCode)keycode && grabs[existing_slot].mods == mods) {
        return 1;
    }

    update_lock_masks();
    unsigned int variants[8];
    int nvar = get_lock_variants(variants, 8);

    prev_error_handler = XSetErrorHandler(grab_error_handler);
    grab_error = 0;
    int grabbed = 0;
    for (int i = 0; i < nvar; i++) {
        XGrabKey(display, (KeyCode)keycode, mods | variants[i], DefaultRootWindow(display), False, GrabModeAsync, GrabModeAsync);
        grabbed++;
    }
    XSync(display, False);
    XSetErrorHandler(prev_error_handler);
    prev_error_handler = NULL;

    if (grab_error) {
        for (int i = 0; i < grabbed; i++) {
            XUngrabKey(display, (KeyCode)keycode, mods | variants[i], DefaultRootWindow(display));
        }
        XSync(display, False);
        if (grab_error == BadAccess) {
            return -3;
        }
        return 0;
    }

    if (existing_slot >= 0) {
        for (int v = 0; v < grabs[existing_slot].nvar; v++) {
            XUngrabKey(display, grabs[existing_slot].keycode, grabs[existing_slot].mods | grabs[existing_slot].variants[v], DefaultRootWindow(display));
        }
        XSync(display, False);
        ct_hotkey_pump();
    }

    grabs[slot].id = id;
    grabs[slot].mods = mods;
    grabs[slot].keycode = (KeyCode)keycode;
    grabs[slot].key_down = 0;
    grabs[slot].fired = 0;
    grabs[slot].nvar = nvar;
    for (int v = 0; v < nvar; v++) {
        grabs[slot].variants[v] = variants[v];
    }
    return 1;
}

int ct_grab(unsigned int mods, unsigned long key) {
    if (!ct_init()) return 0;
    ct_ungrab();
    KeyCode kc = XKeysymToKeycode(display, (KeySym)key);
    if (!kc) return 0;
    return ct_hotkey_grab(1, mods, kc) == 1;
}

void ct_hotkey_pump(void) {
    if (!display) return;
    while (XPending(display)) {
        XEvent e;
        XNextEvent(display, &e);
        if (e.type == MappingNotify) {
            XRefreshKeyboardMapping(&e.xmapping);
            update_lock_masks();
            continue;
        }
        if (e.type == KeyRelease && !xkb_autorepeat_supported) {
            if (XEventsQueued(display, QueuedAfterReading)) {
                XEvent next_event;
                XPeekEvent(display, &next_event);
                if (next_event.type == KeyPress &&
                    next_event.xkey.keycode == e.xkey.keycode &&
                    next_event.xkey.time == e.xkey.time) {
                    XNextEvent(display, &next_event);
                    continue;
                }
            }
        }
        if (e.type != KeyPress && e.type != KeyRelease) continue;
        unsigned int state_clean = e.xkey.state & (ShiftMask | ControlMask | Mod1Mask | Mod4Mask);
        for (int i = 0; i < MAX_HOTKEYS; i++) {
            if (!grabs[i].id) continue;
            if (grabs[i].keycode != e.xkey.keycode) continue;
            if (e.type == KeyPress) {
                if (grabs[i].mods == state_clean) {
                    if (grabs[i].key_down) {
                        char keys[32];
                        XQueryKeymap(display, keys);
                        KeyCode kc = grabs[i].keycode;
                        if (!(keys[kc / 8] & (1 << (kc % 8)))) {
                            grabs[i].key_down = 0;
                        }
                    }
                    if (!grabs[i].key_down) {
                        grabs[i].fired = 1;
                        grabs[i].key_down = 1;
                    }
                }
            } else if (e.type == KeyRelease) {
                grabs[i].key_down = 0;
            }
        }
    }
}

int ct_hotkey_check(int id) {
    if (!id) return 0;
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id == id) {
            int result = grabs[i].fired;
            grabs[i].fired = 0;
            return result;
        }
    }
    return 0;
}

int ct_hotkey(void) {
    ct_hotkey_pump();
    for (int i = 0; i < MAX_HOTKEYS; i++) {
        if (grabs[i].id && grabs[i].fired) {
            grabs[i].fired = 0;
            return 1;
        }
    }
    return 0;
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
int ct_write(const char *text) {
    if (!ct_init()) return 0;
    gtk_clipboard_set_text(gtk_clipboard_get(GDK_SELECTION_CLIPBOARD), text, -1);
    return 1;
}

