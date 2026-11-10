//go:build darwin && cgo && mactest

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#include "native_darwin.h"

void ct_mac_test_menu(int id) {
    @autoreleasepool {
        NSMenu *menu = (NSMenu *)ct_mac_menu_new();
        ct_mac_menu_add(menu, "Test command", id, 0, 0, 0, NULL);
        NSMenuItem *item = [menu itemAtIndex:0];
        [[item target] performSelector:[item action] withObject:item];
        [menu release];
    }
}

void ct_mac_test_hotkey(int pressed) {
    EventRef event = NULL;
    EventHotKeyID identity = {'ClTr', 1};
    CreateEvent(NULL, kEventClassKeyboard, pressed ? kEventHotKeyPressed : kEventHotKeyReleased, GetCurrentEventTime(), kEventAttributeUserEvent, &event);
    SetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, sizeof(identity), &identity);
    SendEventToEventTarget(event, GetApplicationEventTarget());
    ReleaseEvent(event);
}

void ct_mac_test_cancel_setup_soon(void) {
    [NSTimer scheduledTimerWithTimeInterval:0.15 repeats:NO block:^(NSTimer *timer) {
        for (NSWindow *window in [NSApp windows]) {
            if (![[window title] isEqualToString:@"Clipboard Translator — Setup"]) continue;
            for (NSView *view in [[window contentView] subviews]) {
                if ([view isKindOfClass:[NSButton class]] && [[(NSButton *)view title] isEqualToString:@"Cancel"]) [(NSButton *)view performClick:nil];
            }
        }
    }];
}
