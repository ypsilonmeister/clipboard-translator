//go:build linux && cgo
#include "portal_native_linux.h"
#include <gio/gio.h>
#include <string.h>

#define DEST "org.freedesktop.portal.Desktop"
#define PATH "/org/freedesktop/portal/desktop"
struct CTPortal {
    int refs, closed;
    GDBusConnection *bus;
    GCancellable *cancel;
    GQueue events;
    guint response_sub, session_sub, owner_sub;
    gulong closed_handler;
    char *request_prefix, *session_prefix;
};
typedef struct { CTPortal *p; int registered; char *token; } Call;

static void retain(CTPortal *p) { p->refs++; }
void ct_portal_event_free(CTPortalEvent *e) { g_free(e->token); g_free(e->session); }
static void release(CTPortal *p) {
    if (--p->refs) return;
    while (!g_queue_is_empty(&p->events)) {
        CTPortalEvent *e = g_queue_pop_head(&p->events);
        ct_portal_event_free(e); g_free(e);
    }
    g_object_unref(p->cancel); g_object_unref(p->bus);
    g_free(p->request_prefix); g_free(p->session_prefix); g_free(p);
}
static void enqueue(CTPortal *p, int kind, const char *token, const char *session, guint32 code) {
    if (p->closed) return;
    CTPortalEvent *e = g_new0(CTPortalEvent, 1);
    e->kind=kind; e->token=g_strdup(token); e->session=g_strdup(session); e->code=code;
    g_queue_push_tail(&p->events,e);
}
static void response(GDBusConnection *bus, const gchar *sender, const gchar *path,
                     const gchar *iface, const gchar *name, GVariant *args, gpointer data) {
    CTPortal *p=data;
    if (!g_str_has_prefix(path,p->request_prefix)) return;
    if (!g_variant_is_of_type(args,G_VARIANT_TYPE("(ua{sv})"))) { enqueue(p,4,NULL,NULL,0); return; }
    guint32 code; GVariant *results; const char *session=NULL;
    g_variant_get(args,"(u@a{sv})",&code,&results);
    // Both portals historically return session_handle as s, not o.
    GVariant *handle=g_variant_lookup_value(results,"session_handle",NULL);
    if (handle && (g_variant_is_of_type(handle,G_VARIANT_TYPE_STRING) || g_variant_is_of_type(handle,G_VARIANT_TYPE_OBJECT_PATH))) session=g_variant_get_string(handle,NULL);
    if (session && (!g_variant_is_object_path(session) || !g_str_has_prefix(session,p->session_prefix))) { session=NULL; code=2; }
    const char *token=strrchr(path,'/');
    enqueue(p,2,token ? token+1 : "",session,code);
    if (handle) g_variant_unref(handle);
    g_variant_unref(results);
}
static void session_closed(GDBusConnection *bus, const gchar *sender, const gchar *path,
                           const gchar *iface, const gchar *name, GVariant *args, gpointer data) {
    CTPortal *p=data;
    if (g_str_has_prefix(path,p->session_prefix)) enqueue(p,3,NULL,path,0);
}
static void owner_changed(GDBusConnection *bus, const gchar *sender, const gchar *path,
                          const gchar *iface, const gchar *name, GVariant *args, gpointer data) {
    const char *service,*old_owner,*new_owner;
    g_variant_get(args,"(&s&s&s)",&service,&old_owner,&new_owner);
    if (*old_owner) enqueue(data,4,NULL,NULL,0);
}
static void connection_closed(GDBusConnection *bus, gboolean remote, GError *error, gpointer data) {
    enqueue(data,4,NULL,NULL,0);
}
static void call_done(GObject *obj, GAsyncResult *result, gpointer data) {
    Call *c=data; GError *error=NULL;
    GVariant *reply=g_dbus_connection_call_finish(G_DBUS_CONNECTION(obj),result,&error);
    if (!reply) enqueue(c->p,4,c->token,NULL,0);
    else if (c->registered) enqueue(c->p,1,NULL,NULL,0);
    if (reply) g_variant_unref(reply);
    if (error) g_error_free(error); // Never copy a portal error body into app logs.
    release(c->p); g_free(c->token); g_free(c);
}
CTPortal *ct_portal_open(void) {
    GError *error=NULL;
    gchar *address=g_dbus_address_get_for_bus_sync(G_BUS_TYPE_SESSION,NULL,&error);
    if (!address) { g_clear_error(&error); return NULL; }
    // Do not use g_bus_get: Registry.Register is per connection and may only run once.
    GDBusConnection *bus=g_dbus_connection_new_for_address_sync(address,
        G_DBUS_CONNECTION_FLAGS_AUTHENTICATION_CLIENT | G_DBUS_CONNECTION_FLAGS_MESSAGE_BUS_CONNECTION,
        NULL,NULL,&error);
    g_free(address);
    if (!bus) { g_clear_error(&error); return NULL; }
    g_dbus_connection_set_exit_on_close(bus,FALSE);
    CTPortal *p=g_new0(CTPortal,1);p->refs=1;p->bus=bus;p->cancel=g_cancellable_new();
    char *sender=g_strdup(g_dbus_connection_get_unique_name(bus)+1);
    for (char *c=sender;*c;c++) if (*c=='.') *c='_';
    p->request_prefix=g_strdup_printf(PATH "/request/%s/",sender);
    p->session_prefix=g_strdup_printf(PATH "/session/%s/",sender);
    g_free(sender);
    // Subscribe before CreateSession; Response can arrive before the method reply.
    p->response_sub=g_dbus_connection_signal_subscribe(bus,DEST,"org.freedesktop.portal.Request","Response",NULL,NULL,0,response,p,NULL);
    p->session_sub=g_dbus_connection_signal_subscribe(bus,DEST,"org.freedesktop.portal.Session","Closed",NULL,NULL,0,session_closed,p,NULL);
    p->owner_sub=g_dbus_connection_signal_subscribe(bus,"org.freedesktop.DBus","org.freedesktop.DBus","NameOwnerChanged","/org/freedesktop/DBus",DEST,0,owner_changed,p,NULL);
    p->closed_handler=g_signal_connect(bus,"closed",G_CALLBACK(connection_closed),p);
    return p;
}
static void call(CTPortal *p, const char *iface, const char *method, GVariant *args,
                 const GVariantType *reply_type, int registered, const char *token) {
    if (p->closed) { g_variant_unref(g_variant_ref_sink(args)); return; }
    Call *c=g_new0(Call,1);c->p=p;c->registered=registered;c->token=g_strdup(token);retain(p);
    g_dbus_connection_call(p->bus,DEST,PATH,iface,method,args,reply_type,
        G_DBUS_CALL_FLAGS_NONE,10000,p->cancel,call_done,c);
}
void ct_portal_register(CTPortal *p, const char *id) {
    GVariantBuilder opts;g_variant_builder_init(&opts,G_VARIANT_TYPE_VARDICT);
    call(p,"org.freedesktop.host.portal.Registry","Register",g_variant_new("(sa{sv})",id,&opts),G_VARIANT_TYPE_UNIT,1,NULL);
}
void ct_portal_create_session(CTPortal *p, const char *iface, const char *token, const char *session) {
    GVariantBuilder opts;g_variant_builder_init(&opts,G_VARIANT_TYPE_VARDICT);
    g_variant_builder_add(&opts,"{sv}","handle_token",g_variant_new_string(token));
    g_variant_builder_add(&opts,"{sv}","session_handle_token",g_variant_new_string(session));
    call(p,iface,"CreateSession",g_variant_new("(a{sv})",&opts),G_VARIANT_TYPE("(o)"),0,token);
}
void ct_portal_dispatch(void) {
    for (int i=0;i<64 && g_main_context_pending(NULL);i++) g_main_context_iteration(NULL,FALSE);
}
int ct_portal_poll(CTPortal *p, CTPortalEvent *out) {
    ct_portal_dispatch();
    CTPortalEvent *e=g_queue_pop_head(&p->events);
    if (!e) return 0;
    *out=*e;g_free(e);return 1;
}
void ct_portal_close(CTPortal *p) {
    if (p->closed) return;
    p->closed=1;
    g_dbus_connection_signal_unsubscribe(p->bus,p->response_sub);
    g_dbus_connection_signal_unsubscribe(p->bus,p->session_sub);
    g_dbus_connection_signal_unsubscribe(p->bus,p->owner_sub);
    g_signal_handler_disconnect(p->bus,p->closed_handler);
    g_cancellable_cancel(p->cancel);
    // Disconnecting the private peer releases all portal sessions, including ones
    // whose Response has not reached the client yet. No synchronous shutdown wait.
    g_dbus_connection_close(p->bus,NULL,NULL,NULL);
    release(p);
}
