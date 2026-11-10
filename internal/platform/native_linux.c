//go:build linux && cgo

// GTK UI shared by Linux desktop backends. Do not depend on Xlib here.
#include "native_linux.h"
#include <gtk/gtk.h>

static GtkStatusIcon *status_icon;
static GtkWidget *current_menu, *control_window, *status_label;
static char *last_status;
static int menu_requested;
static GQueue commands = G_QUEUE_INIT;

int ct_gtk_init(void) { return gtk_init_check(NULL, NULL); }
void ct_free(void *p) { g_free(p); }

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
        status_label = gtk_label_new(last_status ? last_status : "Clipboard Translator");
        gtk_label_set_line_wrap(GTK_LABEL(status_label),TRUE);
        gtk_label_set_max_width_chars(GTK_LABEL(status_label),70);
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
    g_clear_pointer(&last_status,g_free);
}
void ct_status(const char *text) {
    g_free(last_status);last_status=g_strdup(text);
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
