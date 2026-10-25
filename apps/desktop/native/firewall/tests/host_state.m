// SPDX-License-Identifier: GPL-3.0-only
// Exercise the real host callbacks without submitting a system-extension request,
// changing filter preferences, starting an App, or connecting to the extension.
#import <assert.h>
#include "../../../src-tauri/native/firewall_bridge.m"

@interface TestProperties:NSObject
@property NSString *bundleIdentifier;
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
@property NSUInteger activations;
@end
@implementation TestHost
-(NSDictionary *)bundledExtensionInfo {return @{@"CFBundleShortVersionString":@"2.0.0-test",@"CFBundleVersion":@"2.0.0"};}
-(NSString *)extensionIdentifier {return @"org.example.firewall";}
-(void)submitActivation {
    self.activations++;self.canceledSameVersion=NO;
    self.operation=[OSSystemExtensionRequest activationRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
}
-(NEFilterManager *)filterManager {return nil;}
-(void)refreshProperties {
    self.queries++;
    self.propertiesRequest=[OSSystemExtensionRequest propertiesRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    self.propertiesGeneration=self.generation;
}
-(void)enableFilter {self.filterEnables++;self.filterOperation=YES;}
@end

static TestProperties *Entry(NSString *version,NSString *build,BOOL enabled,BOOL removing,BOOL approval) {
    TestProperties *p=[TestProperties new];p.bundleIdentifier=@"org.example.firewall";p.bundleShortVersion=version;p.bundleVersion=build;
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
    TestHost *guard=[TestHost new];assert(!guard.removalConfirmed);
    [guard refreshProperties];guard.filterOperation=YES;Found(guard,@[]);
    assert(!guard.removalConfirmed);
    guard.filterOperation=NO;[guard refreshProperties];Found(guard,@[]);
    assert(guard.removalConfirmed);
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
    // its enabled flag or its version also matches. Nothing is installing either:
    // after an App restart that is a removal waiting for the next reboot.
    host=[TestHost new];[host refreshProperties];Found(host,@[Entry(@"2.0.0-test",@"2.0.0",YES,YES,NO)]);
    assert([host.extensionState isEqual:@"removal_pending"] && host.filterEnables==0);
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

    // A previous build still running after an App update can't be reached at its versioned service name, so it is
    // replaced once per launch, leaving the filter as it was. After a reboot-required activation the filter must not be
    // enabled prematurely.
    host=[TestHost new];[host refreshProperties];Found(host,@[olderBuild]);
    assert([host.extensionState isEqual:@"activating"] && ![host hasBundledVersion]);
    assert(host.activations==1 && host.upgrading && !host.enableAfterActivation);
    host.enableAfterActivation=YES;Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);
    Found(host,@[olderBuild]);assert([host.extensionState isEqual:@"needs_reboot"] && host.filterEnables==0);
    assert(host.activations==1);
    {
        // The update finishes without touching the filter, and a stale poll of the old build doesn't loop.
        TestHost *updated=[TestHost new];[updated refreshProperties];Found(updated,@[olderBuild]);
        [updated request:updated.operation didFinishWithResult:OSSystemExtensionRequestCompleted];
        assert([updated.extensionState isEqual:@"installed"] && [updated hasBundledVersion]);
        assert(updated.filterEnables==0 && !updated.upgrading);
        [updated refreshProperties];Found(updated,@[olderBuild]);assert(updated.activations==1);
        // A failed update keeps the running build: an installation error, not a failed install.
        TestHost *failed=[TestHost new];[failed refreshProperties];Found(failed,@[olderBuild]);
        NSUInteger before=failed.queries;
        [failed request:failed.operation didFailWithError:[NSError errorWithDomain:OSSystemExtensionErrorDomain code:OSSystemExtensionErrorValidationFailed userInfo:nil]];
        assert(![failed.extensionState isEqual:@"failed"] && [failed.failure[@"code"] isEqual:@"E_FW_ACTIVATION"]);
        assert(failed.queries==before+1 && !failed.upgrading);
        Found(failed,@[olderBuild]);assert([failed.extensionState isEqual:@"installed"] && failed.activations==1);
        // Turning it on while the update runs only adds the intent; the update then enables the filter.
        TestHost *wanted=[TestHost new];[wanted refreshProperties];Found(wanted,@[olderBuild]);
        [wanted enable];assert(wanted.activations==1 && wanted.enableAfterActivation);
        [wanted request:wanted.operation didFinishWithResult:OSSystemExtensionRequestCompleted];
        assert(wanted.filterEnables==1);
        // An update awaiting approval behind the old build asks instead of looking installed.
        TestHost *approval=[TestHost new];[approval refreshProperties];
        Found(approval,@[olderBuild,Entry(@"2.0.0-test",@"2.0.0",NO,NO,YES)]);
        assert([approval.extensionState isEqual:@"needs_approval"] && approval.activations==0);
    }

    // A disable/new operation invalidates pending properties and activation callbacks.
    [host refreshProperties];OSSystemExtensionRequest *stale=host.propertiesRequest;host.generation++;host.enableAfterActivation=NO;
    [host request:stale foundProperties:(id)@[current]];assert(host.filterEnables==0);
    [host refreshProperties];Found(host,@[current]);assert(host.filterEnables==0);
    OSSystemExtensionRequest *activation=[OSSystemExtensionRequest activationRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    [host request:activation didFinishWithResult:OSSystemExtensionRequestCompleted];assert(host.filterEnables==0);

    // Removing an in-use filter completes after a reboot, but it stopped already: not an installation in progress.
    host=[TestHost new];host.uninstalling=YES;
    Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);
    assert(host.removalConfirmed);
    assert([host.extensionState isEqual:@"removal_pending"] && !host.activationNeedsReboot && !host.uninstalling);
    Found(host,@[old]);
    assert([host.extensionState isEqual:@"removal_pending"] && !host.enableAfterActivation && host.filterEnables==0);
    // Turning it back on submits a new activation instead of waiting for that reboot.
    [host enable];assert([host.extensionState isEqual:@"activating"] && host.activations==1 && host.enableAfterActivation);
    host=[TestHost new];host.uninstalling=YES;
    Finished(host,OSSystemExtensionRequestWillCompleteAfterReboot);
    [host refreshProperties];Found(host,@[]);assert([host.extensionState isEqual:@"not_installed"]);
    host=[TestHost new];host.enableAfterActivation=YES;Finished(host,OSSystemExtensionRequestCompleted);
    assert([host.extensionState isEqual:@"installed"] && [host hasBundledVersion] && host.filterEnables==1);

    // A failed removal (a dismissed password prompt) leaves the extension installed: a removal error, not a failed
    // installation, and properties are asked again at once.
    host=[TestHost new];host.uninstalling=YES;host.extensionState=@"uninstalling";
    host.operation=[OSSystemExtensionRequest deactivationRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    NSUInteger asked=host.queries;
    [host request:host.operation didFailWithError:[NSError errorWithDomain:OSSystemExtensionErrorDomain code:OSSystemExtensionErrorAuthorizationRequired userInfo:nil]];
    assert([host.failure[@"code"] isEqual:@"E_FW_REMOVAL"] && ![host.extensionState isEqual:@"failed"]);
    assert(host.queries==asked+1 && !host.uninstalling && host.operation==nil);
    Found(host,@[current]);assert(!host.removalConfirmed);assert([host.extensionState isEqual:@"installed"] && host.filterEnables==0);
    // Replacing: a copy macOS is about to delete never blocks turning the firewall back on, even at the same build; the
    // same build already running is kept, and the request canceled that way counts as done.
    host=[TestHost new];[host submitActivation];
    TestProperties *bundle=Entry(@"2.0.0-test",@"2.0.0",NO,NO,NO);
    assert([host request:host.operation actionForReplacingExtension:(id)Entry(@"2.0.0-test",@"2.0.0",NO,YES,NO) withExtension:(id)bundle]==OSSystemExtensionReplacementActionReplace);
    assert(!host.canceledSameVersion);
    assert([host request:host.operation actionForReplacingExtension:(id)current withExtension:(id)bundle]==OSSystemExtensionReplacementActionCancel);
    assert(host.canceledSameVersion);
    assert([host request:host.operation actionForReplacingExtension:(id)olderBuild withExtension:(id)bundle]==OSSystemExtensionReplacementActionReplace);
    host.canceledSameVersion=YES;host.enableAfterActivation=YES;
    [host request:host.operation didFailWithError:[NSError errorWithDomain:OSSystemExtensionErrorDomain code:OSSystemExtensionErrorRequestCanceled userInfo:nil]];
    assert([host.extensionState isEqual:@"installed"] && host.failure==nil && host.filterEnables==1);
    // A stale removal error goes away once macOS has removed the extension after all.
    host=[TestHost new];host.failure=FWFailure(@"E_FW_REMOVAL",nil);
    [host refreshProperties];Found(host,@[old]);assert(host.failure==nil && [host.extensionState isEqual:@"removal_pending"]);
    // An activation that fails is still a failed installation.
    host=[TestHost new];
    host.operation=[OSSystemExtensionRequest activationRequestForExtension:@"org.example.firewall" queue:dispatch_get_main_queue()];
    [host request:host.operation didFailWithError:[NSError errorWithDomain:OSSystemExtensionErrorDomain code:OSSystemExtensionErrorValidationFailed userInfo:nil]];
    assert([host.failure[@"code"] isEqual:@"E_FW_ACTIVATION"] && [host.extensionState isEqual:@"failed"]);
    puts("Native host replacement-state tests passed (no system operations).");
    return 0;
} }
