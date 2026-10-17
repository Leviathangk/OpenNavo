// SPDX-License-Identifier: GPL-3.0-only
// OpenNavo S2 platform boundary. No upstream product code or artwork.
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <Security/Security.h>
#import <ApplicationServices/ApplicationServices.h>
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>
#include <string.h>

typedef bool (*PasteCallback)(void *, int, long long, long long, const char *, const char *, const char *, size_t, bool);
static CFMachPortRef pasteTap;
static CFRunLoopSourceRef pasteSource;
static PasteCallback pasteCallback;
static void *pasteContext;
static int64_t replayMarker;

void on_security_free(char *value) { if(value) { volatile unsigned char *p=(volatile unsigned char *)value; size_t size=strlen(value); while(size--) *p++=0; free(value); } }
static NSMutableDictionary *keyQuery(void) {
    LAContext *context=[LAContext new]; context.interactionNotAllowed=YES;
    return [@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,
              (__bridge id)kSecAttrService:[(NSBundle.mainBundle.bundleIdentifier ?: @"com.opennavo.desktop") stringByAppendingString:@".security.virustotal"],
              (__bridge id)kSecAttrAccount:@"user-api-key",
              (__bridge id)kSecUseAuthenticationContext:context} mutableCopy];
}
int on_security_key_read(char **out) {
    *out=NULL; NSMutableDictionary *query=keyQuery();
    query[(__bridge id)kSecReturnData]=@YES;
    CFTypeRef data=NULL; OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)query,&data);
    if(status==errSecItemNotFound) return 0;
    if(status!=errSecSuccess) return (int)status;
    NSData *bytes=CFBridgingRelease(data);
    NSString *key=[[NSString alloc] initWithData:bytes encoding:NSUTF8StringEncoding];
    if(!key || key.length>1024) return -50;
    *out=strdup(key.UTF8String); return *out ? 0 : -108;
}
int on_security_key_set(const char *value) {
    NSMutableDictionary *query=keyQuery();
    if(!value) { OSStatus status=SecItemDelete((__bridge CFDictionaryRef)query); return status==errSecItemNotFound ? 0 : (int)status; }
    NSData *data=[[NSString stringWithUTF8String:value] dataUsingEncoding:NSUTF8StringEncoding];
    NSDictionary *attributes=@{(__bridge id)kSecValueData:data};
    OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)attributes);
    if(status==errSecItemNotFound) {
        [query addEntriesFromDictionary:attributes];
        query[(__bridge id)kSecAttrAccessible]=(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly;
        status=SecItemAdd((__bridge CFDictionaryRef)query,NULL);
    }
    return (int)status;
}
bool on_security_ax(bool prompt) {
    if(!prompt) return AXIsProcessTrusted();
    return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)@{(__bridge id)kAXTrustedCheckOptionPrompt:@YES});
}
static bool terminal(NSString *bundle) {
    return [@[@"com.apple.Terminal",@"com.googlecode.iterm2",@"dev.warp.Warp-Stable",@"dev.warp.Warp-Preview",@"net.kovidgoyal.kitty",@"org.alacritty",@"com.mitchellh.ghostty",@"com.github.wez.wezterm",@"co.zeit.hyper",@"org.tabby"] containsObject:bundle ?: @""];
}
// This bounded conversion is shared by interception and explicit replay checks.
// Do not use UTF8String/strlen for clipboard data: embedded NUL must not truncate
// inspection, and oversized data must not cause an unbounded callback allocation.
#define MAX_PASTE_BYTES (1024u * 1024u)
static char *pasteBytes(NSString *text, size_t *length, bool *tooLong) {
    *length=0; *tooLong=false;
    if(!text) return NULL;
    char *bytes=malloc(MAX_PASTE_BYTES+1);
    if(!bytes) return NULL;
    if(text.length==0) { bytes[0]=0; return bytes; }
    NSUInteger used=0; NSRange remaining=NSMakeRange(0,text.length);
    BOOL ok=[text getBytes:bytes maxLength:MAX_PASTE_BYTES usedLength:&used
                 encoding:NSUTF8StringEncoding options:NSStringEncodingConversionAllowLossy
                 range:NSMakeRange(0,text.length) remainingRange:&remaining];
    *length=used;
    *tooLong=!ok || remaining.length>0;
    bytes[used]=0;
    return bytes;
}
void on_security_buffer_free(char *bytes, size_t length) {
    if(bytes) { volatile unsigned char *p=(volatile unsigned char *)bytes; while(length--) *p++=0; free(bytes); }
}
static bool inspectPaste(NSString *text, PasteCallback callback, void *context, int pid,
                         long long launched, long long change, const char *bundle, const char *name) {
    if(!text) return false;
    size_t length=0; bool tooLong=false;
    char *bytes=pasteBytes(text,&length,&tooLong);
    if(!bytes) return true; // Allocation failure must not bypass protection.
    bool blocked=!callback || callback(context,pid,launched,change,bundle,name,bytes,length,tooLong);
    on_security_buffer_free(bytes,length);
    return blocked;
}
// Offline probe: exercises exactly the text-handling callback path, without
// reading the real clipboard, creating a tap, posting keys, or requesting AX.
// Timing includes UTF-8 conversion, Rust rules/hash/event dispatch and zeroing.
bool on_security_paste_probe(const char *input, size_t length, PasteCallback callback,
                             void *context, double *milliseconds) {
    @autoreleasepool {
        NSString *text=[[NSString alloc] initWithBytes:input length:length encoding:NSUTF8StringEncoding];
        if(!text) return false;
        CFAbsoluteTime start=CFAbsoluteTimeGetCurrent();
        bool blocked=inspectPaste(text,callback,context,42,1,7,"com.apple.Terminal","Fixture terminal");
        *milliseconds=(CFAbsoluteTimeGetCurrent()-start)*1000.0;
        return blocked;
    }
}
static CGEventRef pasteEvent(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *context) {
    (void)proxy; (void)context;
    if(type==kCGEventTapDisabledByTimeout || type==kCGEventTapDisabledByUserInput) {
        if(pasteTap && AXIsProcessTrusted()) CGEventTapEnable(pasteTap,true);
        return event;
    }
    if(type!=kCGEventKeyDown || CGEventGetIntegerValueField(event,kCGKeyboardEventKeycode)!=9 || !(CGEventGetFlags(event)&kCGEventFlagMaskCommand)) return event;
    if(replayMarker && CGEventGetIntegerValueField(event,kCGEventSourceUserData)==replayMarker) { replayMarker=0; return event; }
    @autoreleasepool {
        NSRunningApplication *app=NSWorkspace.sharedWorkspace.frontmostApplication;
        if(!app || !app.launchDate || !terminal(app.bundleIdentifier)) return event;
        NSPasteboard *board=NSPasteboard.generalPasteboard;
        NSString *text=[board stringForType:NSPasteboardTypeString];
        if(inspectPaste(text,pasteCallback,pasteContext,app.processIdentifier,
            (long long)(app.launchDate.timeIntervalSince1970*1000),board.changeCount,
            app.bundleIdentifier.UTF8String,app.localizedName.UTF8String)) return NULL;
    }
    return event;
}
bool on_security_paste_enable(bool enabled, PasteCallback callback, void *context) {
    if(!enabled) {
        if(pasteTap) { CGEventTapEnable(pasteTap,false); CFMachPortInvalidate(pasteTap); CFRelease(pasteTap); pasteTap=NULL; }
        if(pasteSource) { CFRunLoopRemoveSource(CFRunLoopGetMain(),pasteSource,kCFRunLoopCommonModes); CFRelease(pasteSource); pasteSource=NULL; }
        pasteCallback=NULL; pasteContext=NULL; return false;
    }
    if(!AXIsProcessTrusted()) return false;
    if(pasteTap) return CGEventTapIsEnabled(pasteTap);
    pasteCallback=callback; pasteContext=context;
    pasteTap=CGEventTapCreate(kCGSessionEventTap,kCGHeadInsertEventTap,kCGEventTapOptionDefault,CGEventMaskBit(kCGEventKeyDown),pasteEvent,NULL);
    if(!pasteTap) return false;
    pasteSource=CFMachPortCreateRunLoopSource(kCFAllocatorDefault,pasteTap,0);
    if(!pasteSource) { CFRelease(pasteTap); pasteTap=NULL; return false; }
    CFRunLoopAddSource(CFRunLoopGetMain(),pasteSource,kCFRunLoopCommonModes);
    CGEventTapEnable(pasteTap,true); return true;
}
bool on_security_paste_listening(void) { return pasteTap && CGEventTapIsEnabled(pasteTap); }
char *on_security_clipboard(long long *change, size_t *length, bool *tooLong) {
    @autoreleasepool {
        NSPasteboard *board=NSPasteboard.generalPasteboard;
        NSString *text=[board stringForType:NSPasteboardTypeString];
        *change=board.changeCount;
        return pasteBytes(text,length,tooLong);
    }
}
bool on_security_paste_replay(int pid, long long launched, long long change, const char *bundle) {
    @autoreleasepool {
        if(!AXIsProcessTrusted() || NSPasteboard.generalPasteboard.changeCount!=change) return false;
        NSRunningApplication *app=[NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if(!app || app.terminated || ![app.bundleIdentifier isEqualToString:[NSString stringWithUTF8String:bundle]] || !terminal(app.bundleIdentifier) || (long long)(app.launchDate.timeIntervalSince1970*1000)!=launched) return false;
        if(@available(macOS 14.0,*)) {
            [NSApp yieldActivationToApplication:app];
            if(![app activateFromApplication:NSRunningApplication.currentApplication options:0]) return false;
        } else if(![app activateWithOptions:0]) { return false; }
        CGEventRef down=CGEventCreateKeyboardEvent(NULL,9,true), up=CGEventCreateKeyboardEvent(NULL,9,false);
        if(!down || !up) { if(down) CFRelease(down); if(up) CFRelease(up); return false; }
        arc4random_buf(&replayMarker,sizeof(replayMarker));
        CGEventSetFlags(down,kCGEventFlagMaskCommand); CGEventSetFlags(up,kCGEventFlagMaskCommand);
        CGEventSetIntegerValueField(down,kCGEventSourceUserData,replayMarker);
        CGEventSetIntegerValueField(up,kCGEventSourceUserData,replayMarker);
        CGEventPostToPid(pid,down); CGEventPostToPid(pid,up);
        CFRelease(down); CFRelease(up); return true;
    }
}
