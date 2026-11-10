//go:build linux && cgo && portaltest

package platform

/*
#include <gtk/gtk.h>
#include <gdk/gdkwayland.h>
#include <stdlib.h>
#include "native_linux.h"
static GtkWidget *w3_window,*w3_view;
static int w3_clicked,w3_closed;
static void w3_begin(GtkButton *b,gpointer data) {
    GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(w3_view));
    GtkTextIter begin,end;gtk_text_buffer_get_bounds(buffer,&begin,&end);
    gtk_widget_grab_focus(w3_view);
    gtk_text_buffer_select_range(buffer,&begin,&end);w3_clicked=1;
}
static gboolean w3_delete(GtkWidget *w,GdkEvent *e,gpointer data) {w3_closed=1;return TRUE;}
static int w3_create(const char *text) {
    if (!ct_gtk_init() || !GDK_IS_WAYLAND_DISPLAY(gdk_display_get_default())) return 0;
    w3_clicked=0;w3_closed=0;
    w3_window=gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(w3_window),"Clipboard Translator W3 - disposable test");
    gtk_window_set_default_size(GTK_WINDOW(w3_window),600,240);
    GtkWidget *box=gtk_box_new(GTK_ORIENTATION_VERTICAL,12);
    w3_view=gtk_text_view_new();
    gtk_text_buffer_set_text(gtk_text_view_get_buffer(GTK_TEXT_VIEW(w3_view)),text,-1);
    GtkWidget *button=gtk_button_new_with_label("Start clipboard test (keep this window focused)");
    gtk_box_pack_start(GTK_BOX(box),w3_view,TRUE,TRUE,0);
    gtk_box_pack_start(GTK_BOX(box),button,FALSE,FALSE,0);
    gtk_container_add(GTK_CONTAINER(w3_window),box);
    g_signal_connect(button,"clicked",G_CALLBACK(w3_begin),NULL);
    g_signal_connect(w3_window,"delete-event",G_CALLBACK(w3_delete),NULL);
    gtk_widget_show_all(w3_window);gtk_window_present(GTK_WINDOW(w3_window));return 1;
}
static int w3_ready(void) {return w3_clicked && !w3_closed && gtk_window_is_active(GTK_WINDOW(w3_window));}
static int w3_selection(void) { return gtk_widget_has_focus(w3_view) && gtk_text_buffer_get_has_selection(gtk_text_view_get_buffer(GTK_TEXT_VIEW(w3_view))); }
static char *w3_text(void) {
    GtkTextBuffer *buffer=gtk_text_view_get_buffer(GTK_TEXT_VIEW(w3_view));
    GtkTextIter begin,end;gtk_text_buffer_get_bounds(buffer,&begin,&end);
    return gtk_text_buffer_get_text(buffer,&begin,&end,TRUE);
}
static void w3_destroy(void) {if (w3_window) gtk_widget_destroy(w3_window);w3_window=w3_view=NULL;}
*/
import "C"

import "unsafe"

func portalEditorCreate(text string) bool {
	p := C.CString(text)
	defer C.free(unsafe.Pointer(p))
	return C.w3_create(p) != 0
}
func portalEditorReady() bool { return C.w3_ready() != 0 }
func portalEditorText() string {
	p := C.w3_text()
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}
func portalEditorDestroy() { C.w3_destroy() }

func portalEditorSelection() bool { return C.w3_selection() != 0 }
