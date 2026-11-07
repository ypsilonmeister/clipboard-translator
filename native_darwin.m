//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <string.h>
#include "native_darwin.h"

static EventHotKeyRef binding;
static EventHandlerRef hotkey_handler;
static int hotkey_down, hotkey_fired, wants_menu;
static NSStatusItem *status_item;
static NSMutableArray *commands;

@interface CTMenuTarget : NSObject
- (void)requestMenu:(id)sender;
- (void)command:(id)sender;
@end
@implementation CTMenuTarget
- (void)requestMenu:(id)sender { wants_menu = 1; }
- (void)command:(id)sender { [commands addObject:@([sender tag])]; }
@end
static CTMenuTarget *menu_target;

int ct_mac_init(void) {
    @autoreleasepool {
        if (![NSThread isMainThread]) return 0;
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        [NSApp finishLaunching];
        if (!commands) commands = [[NSMutableArray alloc] init];
        if (!menu_target) menu_target = [[CTMenuTarget alloc] init];
        return 1;
    }
}

static OSStatus handle_hotkey(EventHandlerCallRef next, EventRef event, void *data) {
    EventHotKeyID identity;
    if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(identity), NULL, &identity) != noErr || identity.signature != 'ClTr') return eventNotHandledErr;
    if (GetEventKind(event) == kEventHotKeyPressed && !hotkey_down) { hotkey_fired = 1; hotkey_down = 1; }
    if (GetEventKind(event) == kEventHotKeyReleased) hotkey_down = 0;
    return noErr;
}

// Resolve letters through the current ASCII-capable keyboard layout rather
// than assuming US physical key positions (e.g. QWERTY vs AZERTY).
static int letter_keycode(char letter) {
    TISInputSourceRef source = TISCopyCurrentASCIICapableKeyboardLayoutInputSource();
    if (!source) return -1;
    CFDataRef data = TISGetInputSourceProperty(source, kTISPropertyUnicodeKeyLayoutData);
    int found = -1;
    if (data) {
        const UCKeyboardLayout *layout = (const UCKeyboardLayout *)CFDataGetBytePtr(data);
        for (int key = 0; key < 128; key++) {
            UInt32 dead = 0;
            UniChar text[4];
            UniCharCount length = 0;
            if (UCKeyTranslate(layout, key, kUCKeyActionDown, 0, LMGetKbdType(), kUCKeyTranslateNoDeadKeysMask, &dead, 4, &length, text) == noErr && length == 1 && text[0] == letter) { found = key; break; }
        }
    }
    CFRelease(source);
    return found;
}

void ct_mac_ungrab(void) {
    if (binding) { UnregisterEventHotKey(binding); binding = NULL; }
    hotkey_down = hotkey_fired = 0;
}
int ct_mac_grab(unsigned int mods, char letter) {
    if (!hotkey_handler) {
        EventTypeSpec types[] = {{kEventClassKeyboard, kEventHotKeyPressed}, {kEventClassKeyboard, kEventHotKeyReleased}};
        if (InstallApplicationEventHandler(handle_hotkey, 2, types, NULL, &hotkey_handler) != noErr) return 0;
    }
    int key = letter_keycode(letter);
    if (key < 0) return 0;
    ct_mac_ungrab();
    EventHotKeyID identity = {'ClTr', 1};
    return RegisterEventHotKey(key, mods, identity, GetApplicationEventTarget(), 0, &binding) == noErr;
}
int ct_mac_hotkey(void) { int fired = hotkey_fired; hotkey_fired = 0; return fired; }
int ct_mac_accessible(void) { return AXIsProcessTrusted(); }

uint64_t ct_mac_focus(void) {
    @autoreleasepool {
        pid_t pid = [[[NSWorkspace sharedWorkspace] frontmostApplication] processIdentifier];
        if (!pid) return 0;
        CFArrayRef windows = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
        uint64_t target = 0;
        for (NSDictionary *window in (NSArray *)windows) {
            if ([window[(NSString *)kCGWindowOwnerPID] intValue] == pid && [window[(NSString *)kCGWindowLayer] intValue] == 0) {
                target = ((uint64_t)(uint32_t)pid << 32) | [window[(NSString *)kCGWindowNumber] unsignedIntValue];
                break;
            }
        }
        if (windows) CFRelease(windows);
        return target;
    }
}
int ct_mac_modifiers(void) {
    CGEventFlags flags = CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState);
    return (flags & (kCGEventFlagMaskCommand | kCGEventFlagMaskControl | kCGEventFlagMaskAlternate | kCGEventFlagMaskShift)) != 0;
}
int ct_mac_send(int paste) {
    if (!AXIsProcessTrusted()) return 0;
    int key = letter_keycode(paste ? 'v' : 'c');
    if (key < 0) return 0;
    CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStateCombinedSessionState);
    CGEventRef down = CGEventCreateKeyboardEvent(source, key, true);
    CGEventRef up = CGEventCreateKeyboardEvent(source, key, false);
    if (source) CFRelease(source);
    if (!down || !up) { if (down) CFRelease(down); if (up) CFRelease(up); return 0; }
    CGEventSetFlags(down, kCGEventFlagMaskCommand);
    CGEventSetFlags(up, kCGEventFlagMaskCommand);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down); CFRelease(up);
    return 1;
}

uint32_t ct_mac_sequence(void) { @autoreleasepool { return (uint32_t)[[NSPasteboard generalPasteboard] changeCount]; } }
char *ct_mac_read(void) {
    @autoreleasepool {
        NSString *text = [[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString];
        return strdup(text ? [text UTF8String] : "");
    }
}
int ct_mac_write(const char *text) {
    @autoreleasepool {
        NSString *value = [NSString stringWithUTF8String:text];
        if (!value) return 0;
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        [pb clearContents];
        return [pb setString:value forType:NSPasteboardTypeString];
    }
}
int ct_mac_replace(const char *expected, const char *translated) {
    @autoreleasepool {
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        NSInteger sequence = [pb changeCount];
        NSString *text = [pb stringForType:NSPasteboardTypeString];
        if (!text || ![text isEqualToString:[NSString stringWithUTF8String:expected]] || sequence != [pb changeCount]) return 0;
        return ct_mac_write(translated) ? 1 : -1;
    }
}
void ct_mac_free(void *p) { free(p); }

void ct_mac_tray_create(void) {
    @autoreleasepool {
        status_item = [[[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength] retain];
        NSImage *image = [NSImage imageWithSystemSymbolName:@"character.bubble" accessibilityDescription:@"Clipboard Translator"];
        [image setTemplate:YES];
        [[status_item button] setImage:image];
        if (!image) [[status_item button] setTitle:@"訳"];
        [[status_item button] setTarget:menu_target];
        [[status_item button] setAction:@selector(requestMenu:)];
        [[status_item button] sendActionOn:NSEventMaskLeftMouseUp | NSEventMaskRightMouseUp];
    }
}
void ct_mac_tray_close(void) {
    @autoreleasepool {
        if (status_item) { [[NSStatusBar systemStatusBar] removeStatusItem:status_item]; [status_item release]; status_item = nil; }
        [commands removeAllObjects];
        wants_menu = 0;
    }
}
void ct_mac_status(const char *text) { @autoreleasepool { [[status_item button] setToolTip:[NSString stringWithUTF8String:text]]; } }
void ct_mac_pump(void) {
    @autoreleasepool {
        NSEvent *event;
        while ((event = [NSApp nextEventMatchingMask:NSEventMaskAny untilDate:[NSDate distantPast] inMode:NSDefaultRunLoopMode dequeue:YES])) [NSApp sendEvent:event];
        [NSApp updateWindows];
    }
}
int ct_mac_wants_menu(void) { int wants = wants_menu; wants_menu = 0; return wants; }
void *ct_mac_menu_new(void) { return [[NSMenu alloc] initWithTitle:@""]; }
void ct_mac_menu_add(void *menu, const char *label, int id, int checked, int disabled, int separator, void *sub) {
    @autoreleasepool {
        NSMenuItem *item = separator ? [NSMenuItem separatorItem] : [[[NSMenuItem alloc] initWithTitle:[NSString stringWithUTF8String:label] action:NULL keyEquivalent:@""] autorelease];
        [(NSMenu *)menu setAutoenablesItems:NO];
        [item setEnabled:!disabled];
        [item setState:checked ? NSControlStateValueOn : NSControlStateValueOff];
        if (sub) { [item setSubmenu:(NSMenu *)sub]; [(NSMenu *)sub release]; }
        else if (id) { [item setTag:id]; [item setTarget:menu_target]; [item setAction:@selector(command:)]; }
        [(NSMenu *)menu addItem:item];
    }
}
void ct_mac_menu_show(void *menu) {
    @autoreleasepool {
        [status_item popUpStatusItemMenu:(NSMenu *)menu];
        [(NSMenu *)menu release];
    }
}
int ct_mac_command(void) {
    @autoreleasepool {
        if (![commands count]) return 0;
        int value = [[commands objectAtIndex:0] intValue];
        [commands removeObjectAtIndex:0];
        return value;
    }
}
int ct_mac_dialog(const char *text, int question, int error) {
    @autoreleasepool {
        NSAlert *alert = [[[NSAlert alloc] init] autorelease];
        [alert setMessageText:@"Clipboard Translator"];
        [alert setInformativeText:[NSString stringWithUTF8String:text]];
        [alert setAlertStyle:error ? NSAlertStyleCritical : NSAlertStyleInformational];
        [alert addButtonWithTitle:question ? @"Continue" : @"OK"];
        if (question) [alert addButtonWithTitle:@"Cancel"];
        [NSApp activateIgnoringOtherApps:YES];
        return [alert runModal] == NSAlertFirstButtonReturn;
    }
}
void ct_mac_open(const char *target) {
    @autoreleasepool {
        NSString *text = [NSString stringWithUTF8String:target];
        NSURL *url = [NSURL URLWithString:text];
        if (![url scheme]) url = [NSURL fileURLWithPath:text];
        [[NSWorkspace sharedWorkspace] openURL:url];
    }
}
