#ifndef CLIPBOARD_TRANSLATOR_LINUX_H
#define CLIPBOARD_TRANSLATOR_LINUX_H
#include <stdint.h>
int ct_gtk_init(void);
char *ct_manual_read(int *complete);
void ct_manual_cancel_read(void);
void ct_manual_write(const char *text);
void ct_show_control(void);
// X11 initialization; never call from a Wayland backend.
int ct_init(void);
void ct_pump(void);
int ct_keysym_to_keycode(unsigned long keysym);
int ct_hotkey_grab(int id, unsigned int mods, unsigned int keycode);
void ct_hotkey_ungrab(int id);
void ct_hotkey_pump(void);
int ct_hotkey_check(int id);
void ct_set_autorepeat_supported(int supported);
void ct_test_set_key_down(int id, int down);
int ct_test_get_key_down(int id);
int ct_test_call_grab_error_handler(int matches_display, int request_code, int error_code);
void ct_test_set_prev_error_handler(void);
int ct_test_get_grab_error(void);
int ct_test_get_slot_variants(int id, unsigned int *variants, int max_var);
void ct_test_inject_autorepeat_pair(int id, unsigned long timestamp);
int ct_grab(unsigned int mods, unsigned long key);
void ct_ungrab(void);
int ct_hotkey(void);
unsigned long ct_focus(void);
int ct_modifiers(void);
int ct_paste(void);
int ct_primary_selected_in(unsigned long win);
char *ct_read(int primary);
void ct_free(void *p);
int ct_write(const char *text);
void ct_tray_create(const char *icon);
void ct_tray_close(void);
void ct_status(const char *text);
int ct_wants_menu(void);
void *ct_menu_new(void);
void ct_menu_add(void *menu, const char *label, int id, int checked, int disabled, int separator, void *sub);
void ct_menu_popup(void *menu);
int ct_command(void);
int ct_dialog(const char *text, int question, int error);
#endif
