#ifndef CLIPBOARD_TRANSLATOR_LINUX_H
#define CLIPBOARD_TRANSLATOR_LINUX_H
#include <stdint.h>
int ct_init(void);
void ct_pump(void);
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
