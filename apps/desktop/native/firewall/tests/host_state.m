// SPDX-License-Identifier: GPL-3.0-only
// Exercise the real host callbacks without submitting a system-extension request,
// changing filter preferences, starting an App, or connecting to the extension.
#import <assert.h>
#include "../../../src-tauri/native/firewall_bridge.m"

@interface TestProperties:NSObject
@property NSString *bundleShortVersion;
@property NSString *bundleVersion;
@property(getter=isEnabled) BOOL enabled;
@property(getter=isAwaitingUserApproval) BOOL awaitingUserApproval;
@property(getter=isUninstalling) BOOL uninstalling;
@end
@implementation TestProperties
@end

@interface TestHost:FWHost
@property NSUInteger queries;
@property NSUInteger filterEnables;
@end
@implementation TestHost
-(NSDictionary *)bundledExtensionInfo {return @{@"CFBundleShortVersionString":@"2.0.0-test",@"CFBundleVersion":@"2.0.0"};}
-(void)refreshProperties {
    self.queries++;
    self.propertiesRequest=[OSSystemExtensionRequest propertiesRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    self.propertiesGeneration=self.generation;
}
-(void)enableFilter {self.filterEnables++;self.filterOperation=YES;}
@end

static TestProperties *Entry(NSString *version,NSString *build,BOOL enabled,BOOL removing,BOOL approval) {
    TestProperties *p=[TestProperties new];p.bundleShortVersion=version;p.bundleVersion=build;
    p.enabled=enabled;p.uninstalling=removing;p.awaitingUserApproval=approval;return p;
}
static void Found(TestHost *host,NSArray *entries) {
    [host request:host.propertiesRequest foundProperties:entries];
}
static void Finished(TestHost *host,OSSystemExtensionRequestResult result) {
    host.operation=[OSSystemExtensionRequest activationRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    [host request:host.operation didFinishWithResult:result];
}
int main(void) { @autoreleasepool {
    TestProperties *current=Entry(@"2.0.0-test",@"2.0.0",YES,NO,NO);
    TestProperties *old=Entry(@"1.0.0-test",@"1.0.0",YES,YES,NO);
    for (NSArray *entries in @[@[old,current],@[current,old]]) {
        TestHost *host=[TestHost new];host.enableAfterActivation=YES;
        Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);
        assert(host.queries==1 && host.enableAfterActivation && host.filterEnables==0);
        Found(host,entries);
        assert([host.extensionState isEqual:@"installed"] && [host hasBundledVersion]);
        assert([host.installedVersion isEqual:@"2.0.0-test"] && host.filterEnables==1);
        assert(!host.activationNeedsReboot);
        [host refreshProperties];Found(host,entries);assert(host.filterEnables==1);
    }
    TestHost *host=[TestHost new];host.enableAfterActivation=YES;
    Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);Found(host,@[old]);
    assert([host.extensionState isEqual:@"needs_reboot"] && host.filterEnables==0);
    assert(host.installedVersion==nil && host.enableAfterActivation);
    // Later polling can recover from needs_reboot without another activation request.
    [host refreshProperties];Found(host,@[current,old]);
    assert([host.extensionState isEqual:@"installed"] && host.filterEnables==1);

    // Only an uninstalling entry is not evidence of an active extension, even if
    // its enabled flag or its version also matches.
    host=[TestHost new];[host refreshProperties];Found(host,@[Entry(@"2.0.0-test",@"2.0.0",YES,YES,NO)]);
    assert([host.extensionState isEqual:@"needs_reboot"] && host.filterEnables==0);
    [host refreshProperties];Found(host,@[]);assert([host.extensionState isEqual:@"not_installed"]);

    // Prefer the bundled build regardless of array order; don't treat an enabled
    // but still awaiting-approval entry as installed.
    TestProperties *olderBuild=Entry(@"2.0.0-test",@"1.9.0",YES,NO,NO);
    for (NSArray *entries in @[@[olderBuild,current],@[current,olderBuild]]) {
        host=[TestHost new];[host refreshProperties];Found(host,entries);
        assert([host hasBundledVersion] && host.filterEnables==0);
    }
    host=[TestHost new];[host refreshProperties];Found(host,@[Entry(@"2.0.0-test",@"2.0.0",YES,NO,YES),old]);
    assert([host.extensionState isEqual:@"needs_approval"] && host.installedVersion==nil);

    // A previous active version is visible after an App restart. But after a
    // reboot-required activation it must not prematurely enable the filter.
    host=[TestHost new];[host refreshProperties];Found(host,@[olderBuild]);
    assert([host.extensionState isEqual:@"installed"] && ![host hasBundledVersion]);
    host.enableAfterActivation=YES;Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);
    Found(host,@[olderBuild]);assert([host.extensionState isEqual:@"needs_reboot"] && host.filterEnables==0);

    // A disable/new operation invalidates pending properties and activation callbacks.
    [host refreshProperties];OSSystemExtensionRequest *stale=host.propertiesRequest;host.generation++;host.enableAfterActivation=NO;
    [host request:stale foundProperties:(id)@[current]];assert(host.filterEnables==0);
    [host refreshProperties];Found(host,@[current]);assert(host.filterEnables==0);
    OSSystemExtensionRequest *activation=[OSSystemExtensionRequest activationRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    [host request:activation didFinishWithResult:OSSystemExtensionRequestCompleted];assert(host.filterEnables==0);

    host=[TestHost new];host.uninstalling=YES;
    Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);Found(host,@[old]);
    assert([host.extensionState isEqual:@"needs_reboot"] && !host.enableAfterActivation && host.filterEnables==0);
    [host refreshProperties];Found(host,@[]);assert([host.extensionState isEqual:@"not_installed"]);
    host=[TestHost new];host.enableAfterActivation=YES;Finished(host,OSSystemExtensionRequestCompleted);
    assert([host.extensionState isEqual:@"installed"] && [host hasBundledVersion] && host.filterEnables==1);
    puts("Native host replacement-state tests passed (no system operations).");
    return 0;
} }
