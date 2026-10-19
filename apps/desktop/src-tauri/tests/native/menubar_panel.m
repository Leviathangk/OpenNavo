// Exercise the real bridge against KVO and AppKit exceptions, on the main thread.
#import "../../native/monitor_bridge.m"
#include <assert.h>

// Match the TaoWindow superclass/ivar layout without starting the desktop app.
@interface TaoWindow : NSWindow { @public BOOL focusable; }
@end
@implementation TaoWindow
- (BOOL)canBecomeKeyWindow { return focusable; }
- (BOOL)canBecomeMainWindow { return focusable; }
@end

@interface PanelObserver : NSObject
@property NSUInteger changes;
@end
@implementation PanelObserver
- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object
                       change:(NSDictionary *)change context:(void *)context {
    (void)keyPath; (void)object; (void)change; (void)context;
    self.changes += 1;
}
@end

@interface RejectedConfiguration : TaoWindow
@end
@implementation RejectedConfiguration
- (void)setCollectionBehavior:(NSWindowCollectionBehavior)value {
    if (value & NSWindowCollectionBehaviorTransient)
        [NSException raise:@"PanelTestConfiguration" format:@"injected failure"];
    [super setCollectionBehavior:value];
}
@end

@interface MissingActivationSelector : TaoWindow
@end
@implementation MissingActivationSelector
- (BOOL)respondsToSelector:(SEL)selector {
    if (selector == NSSelectorFromString(@"_setPreventsActivation:")) return NO;
    return [super respondsToSelector:selector];
}
@end

@interface RejectedPresentation : TaoWindow
@end
@implementation RejectedPresentation
- (void)orderFrontRegardless {
    [NSException raise:@"PanelTestPresentation" format:@"injected failure"];
}
@end

static NSWindow *windowOfClass(Class cls) {
    NSWindow *window = [[cls alloc] initWithContentRect:NSMakeRect(0, 0, 120, 80)
        styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
    window.releasedWhenClosed = NO;
    return window;
}

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
        SEL predicate = NSSelectorFromString(@"_isNonactivatingPanel");
        BOOL (^nonactivating)(NSWindow *) = ^BOOL(NSWindow *window) {
            return ((BOOL (*)(id, SEL))objc_msgSend)(window, predicate);
        };
        TaoWindow *normal = (TaoWindow *)windowOfClass(TaoWindow.class);
        TaoWindow *panel = (TaoWindow *)windowOfClass(TaoWindow.class);
        normal->focusable = YES;
        panel->focusable = YES;
        PanelObserver *observer = [PanelObserver new];
        [panel addObserver:observer forKeyPath:@"title" options:0 context:NULL];
        Class observedClass = object_getClass(panel);
        assert(observedClass != TaoWindow.class);
        assert(strstr(class_getName(observedClass), "NSKVONotifying") != NULL);
        assert(on_menubar_panel_configure((__bridge void *)panel) == 1);
        assert(object_getClass(panel) == observedClass);
        assert(panel->focusable && panel.canBecomeKeyWindow);
        assert(!panel.canBecomeMainWindow);
        assert(normal.canBecomeMainWindow);
        assert(nonactivating(panel));
        assert(!nonactivating(normal)); // Shared TaoWindow hook must leave main alone.
        panel.title = @"KVO survives configuration";
        assert(observer.changes == 1);
        assert(on_menubar_panel_configure((__bridge void *)panel) == 1);
        panel.title = @"Repeated configuration is safe";
        assert(observer.changes == 2);
        [panel removeObserver:observer forKeyPath:@"title"];
        // AppKit may retain its own observations after ours is removed.
        panel.title = @"Observer removed";
        assert(observer.changes == 2);
        assert(nonactivating(panel)); // KVO removal must not remove panel behavior.

        NSWindow *fallback = windowOfClass(RejectedConfiguration.class);
        assert(on_menubar_panel_configure((__bridge void *)fallback) == 2);
        assert(!nonactivating(fallback));
        NSWindow *unsupported = windowOfClass(MissingActivationSelector.class);
        assert(on_menubar_panel_configure((__bridge void *)unsupported) == 0);
        assert(on_menubar_panel_show((__bridge void *)unsupported) == 0);
        assert(!unsupported.visible);
        NSWindow *rejected = windowOfClass(RejectedPresentation.class);
        assert(on_menubar_panel_configure((__bridge void *)rejected) == 1);
        assert(on_menubar_panel_show((__bridge void *)rejected) == 0);
        assert(!rejected.visible);
        NSWindow *unconfigured = windowOfClass(TaoWindow.class);
        assert(on_menubar_panel_show((__bridge void *)unconfigured) == 0);
        assert(!unconfigured.visible);
        assert(on_menubar_panel_configure(NULL) == 0);
        assert(on_menubar_panel_show(NULL) == 0);
        for (NSWindow *window in @[normal, panel, fallback, unsupported, rejected, unconfigured]) {
            assert(!window.visible);
            [window close];
        }
        puts("KVO preservation, main-window isolation, fallback and presentation failure passed");
    }
}
