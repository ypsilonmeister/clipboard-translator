//go:build linux && cgo

package platform

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>

// Probe only X11 Crostini. Bound bus activation and both method calls together.
// Unknown is deliberately not evidence that portals are missing.
typedef struct { GMutex mutex; GCond cond; gboolean done; GCancellable *cancel; } ProbeDeadline;
static gpointer cancel_probe(gpointer data) {
    ProbeDeadline *d = data;
    gint64 until = g_get_monotonic_time() + 2 * G_TIME_SPAN_SECOND;
    g_mutex_lock(&d->mutex);
    while (!d->done && g_cond_wait_until(&d->cond, &d->mutex, until)) {}
    if (!d->done) g_cancellable_cancel(d->cancel);
    g_mutex_unlock(&d->mutex);
    return NULL;
}
static int ct_portal_interfaces_available(void) {
    ProbeDeadline deadline = {0};
    g_mutex_init(&deadline.mutex); g_cond_init(&deadline.cond);
    deadline.cancel = g_cancellable_new();
    GThread *timer = g_thread_new("portal-probe-deadline", cancel_probe, &deadline);
    int available = -1;
    GDBusConnection *bus = g_bus_get_sync(G_BUS_TYPE_SESSION, deadline.cancel, NULL);
    GVariant *reply = NULL, *devices = NULL;
    GDBusNodeInfo *node = NULL;
    if (!bus) goto done;
    reply = g_dbus_connection_call_sync(bus,
        "org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop",
        "org.freedesktop.DBus.Introspectable", "Introspect", NULL,
        G_VARIANT_TYPE("(s)"), G_DBUS_CALL_FLAGS_NONE, 2000, deadline.cancel, NULL);
    if (!reply) goto done;
    const gchar *xml;
    g_variant_get(reply, "(&s)", &xml);
    node = g_dbus_node_info_new_for_xml(xml, NULL);
    if (!node) goto done;
    available = g_dbus_node_info_lookup_interface(node, "org.freedesktop.portal.GlobalShortcuts") &&
        g_dbus_node_info_lookup_interface(node, "org.freedesktop.portal.Clipboard") &&
        g_dbus_node_info_lookup_interface(node, "org.freedesktop.portal.RemoteDesktop");
    if (!available) goto done;
    available = -1;
    devices = g_dbus_connection_call_sync(bus,
        "org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop",
        "org.freedesktop.DBus.Properties", "Get",
        g_variant_new("(ss)", "org.freedesktop.portal.RemoteDesktop", "AvailableDeviceTypes"),
        G_VARIANT_TYPE("(v)"), G_DBUS_CALL_FLAGS_NONE, 2000, deadline.cancel, NULL);
    if (devices) {
        GVariant *value;
        g_variant_get(devices, "(v)", &value);
        if (g_variant_is_of_type(value, G_VARIANT_TYPE_UINT32))
            available = (g_variant_get_uint32(value) & 1) != 0;
        g_variant_unref(value);
    }
done:
    g_mutex_lock(&deadline.mutex); deadline.done = TRUE; g_cond_signal(&deadline.cond); g_mutex_unlock(&deadline.mutex);
    g_thread_join(timer);
    g_object_unref(deadline.cancel); g_cond_clear(&deadline.cond); g_mutex_clear(&deadline.mutex);
    if (devices) g_variant_unref(devices);
    if (node) g_dbus_node_info_unref(node);
    if (reply) g_variant_unref(reply);
    if (bus) g_object_unref(bus);
    if (available < 0) g_warning("Portal capability probe failed or timed out; status unknown, keeping normal X11 workflow");
    return available != 0;
}
*/
import "C"

func nativePortalInterfacesAvailable() bool { return C.ct_portal_interfaces_available() != 0 }
