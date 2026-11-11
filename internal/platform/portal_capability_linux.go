//go:build linux && cgo

package platform

/*
#cgo pkg-config: gio-2.0
#include <gio/gio.h>

// Introspection checks the service's interfaces without requesting permissions.
// Bound the startup call; a missing bus/service is an unavailable workflow.
static int ct_portal_workflow_available(void) {
    GDBusConnection *bus = g_bus_get_sync(G_BUS_TYPE_SESSION, NULL, NULL);
    if (!bus) return 0;
    GVariant *reply = g_dbus_connection_call_sync(bus,
        "org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop",
        "org.freedesktop.DBus.Introspectable", "Introspect", NULL,
        G_VARIANT_TYPE("(s)"), G_DBUS_CALL_FLAGS_NONE, 2000, NULL, NULL);
    g_object_unref(bus);
    if (!reply) return 0;
    const gchar *xml;
    g_variant_get(reply, "(&s)", &xml);
    GDBusNodeInfo *node = g_dbus_node_info_new_for_xml(xml, NULL);
    int available = node &&
        g_dbus_node_info_lookup_interface(node, "org.freedesktop.portal.GlobalShortcuts") &&
        g_dbus_node_info_lookup_interface(node, "org.freedesktop.portal.Clipboard");
    if (node) g_dbus_node_info_unref(node);
    g_variant_unref(reply);
    return available;
}
*/
import "C"

func nativePortalWorkflowAvailable() bool { return C.ct_portal_workflow_available() != 0 }
