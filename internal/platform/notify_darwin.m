//go:build darwin && cgo

// Notification Center (Issue #67). Manual reference counting, like
// native_darwin.m. Completion handlers run on background queues: they only
// write the lock-protected snapshot below, which Go reads from the UI thread
// in PumpMessages. Nothing here touches AppKit or Go state.

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>
#include <os/lock.h>
#include <stdio.h>
#include <string.h>
#include "native_darwin.h"

// Values shared with Go's AuthStatus.
enum { CT_UNAVAILABLE = 0, CT_UNKNOWN = 1, CT_NOT_DETERMINED = 2, CT_DENIED = 3, CT_AUTHORIZED = 4, CT_PROVISIONAL = 5 };

static UNUserNotificationCenter *center; // retained; nil = unavailable
static id center_delegate;               // owned: the center keeps only a weak reference
static int touched;                      // a UN API was called (tests: never when unbundled)
static os_unfair_lock notify_lock = OS_UNFAIR_LOCK_INIT;
static int status_value = CT_UNAVAILABLE;
static long refresh_started, refresh_applied; // a stale answer never overwrites a newer one
static int request_in_flight;
static int failure_count;
static char failure_last[256];

static void record_failure(const char *op, NSString *domain, long code) {
    @autoreleasepool {
        char text[256];
        const char *name = domain ? [domain UTF8String] : NULL;
        snprintf(text, sizeof text, "%s: %s %ld", op, name ? name : "unknown", code);
        os_unfair_lock_lock(&notify_lock);
        failure_count++;
        memcpy(failure_last, text, sizeof failure_last);
        os_unfair_lock_unlock(&notify_lock);
    }
}

static int map_status(UNAuthorizationStatus s) {
    switch (s) {
    case UNAuthorizationStatusNotDetermined: return CT_NOT_DETERMINED;
    case UNAuthorizationStatusDenied: return CT_DENIED;
    case UNAuthorizationStatusAuthorized: return CT_AUTHORIZED;
    case UNAuthorizationStatusProvisional: return CT_PROVISIONAL;
    default: return CT_UNKNOWN;
    }
}

@interface CTNoticeDelegate : NSObject <UNUserNotificationCenterDelegate>
@end
@implementation CTNoticeDelegate
// Shown even in the rare moments the accessory app is frontmost (an NSAlert is
// up). The Sound bit is needed too, or an error notice would be silent then.
- (void)userNotificationCenter:(UNUserNotificationCenter *)c willPresentNotification:(UNNotification *)notification withCompletionHandler:(void (^)(UNNotificationPresentationOptions))done {
    UNNotificationPresentationOptions options = UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList;
    if ([[[notification request] content] sound]) options |= UNNotificationPresentationOptionSound;
    done(options);
}
// No custom click actions (deferred, Issue #67); the handler must still be called.
- (void)userNotificationCenter:(UNUserNotificationCenter *)c didReceiveNotificationResponse:(UNNotificationResponse *)response withCompletionHandler:(void (^)(void))done {
    done();
}
@end

// start_refresh reads the permission asynchronously and returns the read's
// ticket, or 0 if it could not start (recorded as a failure). With
// clears_request the request's in-flight bit clears only once the answer is
// in status_value, so a request never looks finished while the snapshot still
// says "not determined".
static long start_refresh(dispatch_semaphore_t answered, int clears_request) {
    if (!center) return 0;
    os_unfair_lock_lock(&notify_lock);
    long seq = ++refresh_started;
    os_unfair_lock_unlock(&notify_lock);
    @try {
        [center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
            int value = map_status([settings authorizationStatus]);
            os_unfair_lock_lock(&notify_lock);
            if (seq > refresh_applied) {
                refresh_applied = seq;
                status_value = value;
            }
            if (clears_request) request_in_flight = 0;
            os_unfair_lock_unlock(&notify_lock);
            if (answered) dispatch_semaphore_signal(answered);
        }];
    } @catch (NSException *e) {
        record_failure("refresh", [e name], 0);
        return 0;
    }
    return seq;
}

int ct_mac_notify_init(void) {
    if (center) return 1;
    @autoreleasepool {
        // +currentNotificationCenter raises in a process without a bundle
        // identifier (go run, a bare go build binary, the test harness), and
        // an exception crossing cgo aborts. So do not even call it there.
        NSBundle *bundle = [NSBundle mainBundle];
        if (![bundle bundleIdentifier] || ![[bundle bundlePath] hasSuffix:@".app"]) return 0;
        touched = 1;
        @try {
            UNUserNotificationCenter *c = [UNUserNotificationCenter currentNotificationCenter];
            if (!c) return 0;
            center_delegate = [[CTNoticeDelegate alloc] init];
            [c setDelegate:center_delegate];
            center = [c retain];
        } @catch (NSException *e) {
            return 0;
        }
    }
    os_unfair_lock_lock(&notify_lock);
    status_value = CT_UNKNOWN;
    os_unfair_lock_unlock(&notify_lock);
    // Startup notices come right after this: wait briefly for the first
    // answer instead of treating them as "not allowed". Never blocks long.
    dispatch_semaphore_t answered = dispatch_semaphore_create(0);
    start_refresh(answered, 0);
    dispatch_semaphore_wait(answered, dispatch_time(DISPATCH_TIME_NOW, 300 * NSEC_PER_MSEC));
    dispatch_release(answered);
    return 1;
}

long long ct_mac_notify_refresh(void) { return start_refresh(NULL, 0); }

long long ct_mac_notify_applied(void) {
    os_unfair_lock_lock(&notify_lock);
    long value = refresh_applied;
    os_unfair_lock_unlock(&notify_lock);
    return value;
}

int ct_mac_notify_status(void) {
    os_unfair_lock_lock(&notify_lock);
    int value = status_value;
    os_unfair_lock_unlock(&notify_lock);
    return value;
}

int ct_mac_notify_request(void) {
    if (!center) return 0;
    os_unfair_lock_lock(&notify_lock);
    if (request_in_flight) {
        os_unfair_lock_unlock(&notify_lock);
        return 0;
    }
    request_in_flight = 1;
    os_unfair_lock_unlock(&notify_lock);
    @try {
        // Alert and sound; no badge (there is no Dock icon). Banner or alert
        // style stays the user's choice in System Settings.
        [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound) completionHandler:^(BOOL granted, NSError *error) {
            if (error) record_failure("request", [error domain], (long)[error code]);
            if (!start_refresh(NULL, 1)) {
                os_unfair_lock_lock(&notify_lock);
                request_in_flight = 0;
                os_unfair_lock_unlock(&notify_lock);
            }
        }];
    } @catch (NSException *e) {
        os_unfair_lock_lock(&notify_lock);
        request_in_flight = 0;
        os_unfair_lock_unlock(&notify_lock);
        record_failure("request", [e name], 0);
        return 0;
    }
    return 1;
}

static NSString *utf8(const char *text) {
    NSString *s = [NSString stringWithUTF8String:text];
    return s ? s : @"";
}

void ct_mac_notify_post(const char *ident, const char *title, const char *body, int sound) {
    if (!center) return;
    @autoreleasepool {
        @try {
            UNMutableNotificationContent *content = [[[UNMutableNotificationContent alloc] init] autorelease];
            [content setTitle:utf8(title)];
            [content setBody:utf8(body)];
            [content setThreadIdentifier:@"clipboard-translator"];
            if (sound) [content setSound:[UNNotificationSound defaultSound]];
            // The same identifier replaces the older entry of that kind.
            UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:utf8(ident) content:content trigger:nil];
            [center addNotificationRequest:request withCompletionHandler:^(NSError *error) {
                if (error) record_failure("post", [error domain], (long)[error code]);
            }];
        } @catch (NSException *e) {
            record_failure("post", [e name], 0);
        }
    }
}

int ct_mac_notify_failures(char *last, int size) {
    os_unfair_lock_lock(&notify_lock);
    int count = failure_count;
    if (last && size > 0) {
        strncpy(last, failure_last, (size_t)size - 1);
        last[size - 1] = 0;
    }
    os_unfair_lock_unlock(&notify_lock);
    return count;
}

int ct_mac_notify_touched(void) { return touched; }
