#ifndef CT_PORTAL_LINUX_H
#define CT_PORTAL_LINUX_H
#include <stdint.h>
typedef struct CTPortal CTPortal;
typedef struct { int kind; char *token; char *session; uint32_t code; char *id; void *bindings; uint32_t devices, serial; int clipboard, owner; char *restore, *mime; void *data; int length; } CTPortalEvent;
CTPortal *ct_portal_open(void);
void ct_portal_register(CTPortal *, const char *);
void ct_portal_probe(CTPortal *, const char *);
void ct_portal_create_session(CTPortal *, const char *, const char *, const char *);
typedef struct { const char *id, *description, *trigger; } CTShortcut;
void ct_portal_bind_shortcuts(CTPortal *, const char *, const char *, CTShortcut *, int);
void ct_portal_close_session(CTPortal *, const char *);
void ct_portal_close_request(CTPortal *, const char *);
int ct_portal_binding_count(CTPortalEvent *);
const char *ct_portal_binding_id(CTPortalEvent *, int);
const char *ct_portal_binding_trigger(CTPortalEvent *, int);
void ct_portal_select_devices(CTPortal *, const char *, const char *, const char *);
void ct_portal_request_clipboard(CTPortal *, const char *, const char *);
void ct_portal_start_remote(CTPortal *, const char *, const char *);
void ct_portal_set_selection(CTPortal *, const char *, const char *);
void ct_portal_read_selection(CTPortal *, const char *, const char *, const char *);
void ct_portal_write_selection(CTPortal *, const char *, const char *, uint32_t, const void *, int);
void ct_portal_write_done(CTPortal *, const char *, uint32_t, int);
void ct_portal_cancel_io(CTPortal *, const char *);
void ct_portal_key(CTPortal *, const char *, const char *, int, int);
void ct_portal_dispatch(void);
int ct_portal_poll(CTPortal *, CTPortalEvent *);
void ct_portal_event_free(CTPortalEvent *);
void ct_portal_close(CTPortal *);
#endif
