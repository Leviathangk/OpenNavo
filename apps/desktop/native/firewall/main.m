// SPDX-License-Identifier: GPL-3.0-only
// NE bootstrap adapted from LuLu v4.5.1 (Objective-See / Patrick Wardle).
#import <Foundation/Foundation.h>
#import <NetworkExtension/NetworkExtension.h>
extern void FWStartService(void);
int main(void) {
    @autoreleasepool {
        FWStartService();
        [NEProvider startSystemExtensionMode];
        dispatch_main();
    }
}
