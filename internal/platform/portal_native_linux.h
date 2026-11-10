#ifndef CT_PORTAL_LINUX_H
#define CT_PORTAL_LINUX_H
#include <stdint.h>
typedef struct CTPortal CTPortal;
typedef struct { int kind; char *token; char *session; uint32_t code; } CTPortalEvent;
CTPortal *ct_portal_open(void);
void ct_portal_register(CTPortal *, const char *);
void ct_portal_create_session(CTPortal *, const char *, const char *, const char *);
void ct_portal_dispatch(void);
int ct_portal_poll(CTPortal *, CTPortalEvent *);
void ct_portal_event_free(CTPortalEvent *);
void ct_portal_close(CTPortal *);
#endif
