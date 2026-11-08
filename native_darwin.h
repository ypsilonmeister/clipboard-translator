#ifndef CLIPBOARD_TRANSLATOR_MAC_H
#define CLIPBOARD_TRANSLATOR_MAC_H
#include <stdint.h>
int ct_mac_init(void);
void ct_mac_pump(void);
int ct_mac_grab(unsigned int mods, int vk, char letter);
void ct_mac_ungrab(void);
int ct_mac_hotkey(void);
int ct_mac_accessible(void);
uint64_t ct_mac_focus(void);
int ct_mac_modifiers(void);
int ct_mac_send(int paste);
uint32_t ct_mac_sequence(void);
char *ct_mac_read(void);
int ct_mac_write(const char *text);
int ct_mac_replace(const char *expected, const char *translated);
void ct_mac_free(void *p);
void ct_mac_tray_create(void);
void ct_mac_tray_close(void);
void ct_mac_status(const char *text);
int ct_mac_wants_menu(void);
void *ct_mac_menu_new(void);
void ct_mac_menu_add(void *menu, const char *label, int id, int checked, int disabled, int separator, void *sub);
void ct_mac_menu_show(void *menu);
int ct_mac_command(void);
int ct_mac_dialog(const char *text, int question, int error);
void ct_mac_open(const char *target);
void ct_mac_setup_begin(const char *logpath);
int ct_mac_setup_cancelled(void);
void ct_mac_setup_end(void);
#endif
