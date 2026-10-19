// SPDX-License-Identifier: GPL-3.0-only
// Drive the real detector with simulated peers/preferences. Never submit system
// requests, change real NE preferences, or connect to a running extension.
#import <assert.h>
#include "../../../src-tauri/native/firewall_bridge.m"

@interface MissingProperties:NSObject
@property NSString *bundleIdentifier;
@property NSString *bundleShortVersion;
@property NSString *bundleVersion;
@end
@implementation MissingProperties
@end
@interface MemoryFilter:NSObject
@property(getter=isEnabled) BOOL enabled;
@property NEFilterProviderConfiguration *providerConfiguration;
@property NSString *localizedDescription;
@end
@implementation MemoryFilter
@end
@interface MissingHost:FWHost
@property NSUInteger activations;
@property NSUInteger removals;
@property NSUInteger saves;
@property MemoryFilter *manager;
@end
@implementation MissingHost
-(instancetype)init {
    self=[super init];if(self) {
        self.manager=[MemoryFilter new];self.manager.enabled=YES;
        self.manager.localizedDescription=@"Original description";
        NEFilterProviderConfiguration *config=[NEFilterProviderConfiguration new];
        config.filterSockets=YES;config.filterDataProviderBundleIdentifier=@"org.example.firewall";
        config.vendorConfiguration=@{@"preserve":@42};self.manager.providerConfiguration=config;
        self.extensionState=@"installed";self.filterState=@"on";self.bundledVersionConfirmed=YES;
        self.installedVersion=@"0.0.4-firewalltest";self.installedBuildVersion=@"0.0.4";
    }return self;
}
-(NSString *)extensionIdentifier {return @"org.example.firewall";}
-(NSDictionary *)bundledExtensionInfo {return @{@"CFBundleShortVersionString":@"0.0.4-firewalltest",@"CFBundleVersion":@"0.0.4"};}
-(NEFilterManager *)filterManager {return (id)self.manager;}
-(void)loadFilter:(void (^)(NSError *))completion {completion(nil);}
-(void)saveFilter:(void (^)(NSError *))completion {self.saves++;completion(nil);}
-(void)removeFilter:(void (^)(NSError *))completion {self.removals++;completion(nil);}
-(void)submitActivation {self.activations++;}
-(void)refreshProperties {self.bundledVersionConfirmed=YES;}
@end
static void Observe(MissingHost *host,FWPeerState state,uint64_t at) {
    [host observePeer:state at:at generation:host.generation disconnects:FWDisconnectGeneration()];
}
static void Missing(MissingHost *host,uint64_t start,NSUInteger seconds) {
    for(NSUInteger i=0;i<=seconds;i++) Observe(host,FWPeerMissing,start+i*1000);
}
static MissingProperties *Version(NSString *version) {
    MissingProperties *p=[MissingProperties new];p.bundleIdentifier=@"org.example.firewall";
    p.bundleShortVersion=version;p.bundleVersion=version;return p;
}
int main(void) { @autoreleasepool {
    assert(FWClassifyPeer(NO,YES,250)==FWPeerMissing);
    assert(FWClassifyPeer(NO,YES,251)==FWPeerOther);
    assert(FWClassifyPeer(NO,NO,1)==FWPeerOther);
    assert(FWClassifyPeer(YES,NO,500)==FWPeerReady);
    MissingHost *host=[MissingHost new];
    Missing(host,1000,14);assert(!host.failure);
    Observe(host,FWPeerMissing,16000);
    assert([host.failure[@"code"] isEqual:@"E_FW_DISCONNECTED"]);
    assert([host.failure[@"detail"] isEqual:FWServiceMissing]);
    Missing(host,17000,300);
    assert(host.activations==0 && host.removals==0 && host.saves==0);
    assert([host.extensionState isEqual:@"installed"] && [host.filterState isEqual:@"on"]);
    assert([host.manager.providerConfiguration.vendorConfiguration isEqual:@{@"preserve":@42}]);
    Observe(host,FWPeerOther,318000);assert([host.failure[@"detail"] isEqual:FWServiceMissing]);
    Observe(host,FWPeerReady,319000);assert(!host.failure);
    Missing(host,320000,15);assert(host.failure); // no once-per-run latch

    // Toggle uses normal enable/disable, with a fresh observation window. It
    // must never force a same-version activation or remove preferences.
    [host disable:NO];assert(!host.manager.enabled && !host.failure);
    [host enable];assert(host.manager.enabled && !host.failure);
    assert(host.activations==0 && host.removals==0 && host.saves==2);
    Missing(host,400000,14);assert(!host.failure);
    Observe(host,FWPeerMissing,415000);assert([host.failure[@"detail"] isEqual:FWServiceMissing]);

    for(NSUInteger scenario=0;scenario<7;scenario++) {
        host=[MissingHost new];
        if(scenario==0) host.bundledVersionConfirmed=NO;
        if(scenario==1) host.installedBuildVersion=@"0.0.3";
        if(scenario==2) host.extensionState=@"needs_approval";
        if(scenario==3) host.filterState=@"off";
        if(scenario==4) host.filterState=@"needs_consent";
        if(scenario==5) host.filterOperation=YES;
        if(scenario==6) host.uninstalling=YES;
        Missing(host,1000,30);assert(!host.failure && !host.activations && !host.removals);
    }
    host=[MissingHost new];Missing(host,1000,10);Observe(host,FWPeerOther,12000);
    Missing(host,13000,14);assert(!host.failure);
    Observe(host,FWPeerReady,28000);Missing(host,29000,14);assert(!host.failure);
    Missing(host,60000,14);assert(!host.failure); // sleep/gap resets the window
    uint64_t generation=host.generation,disconnects=FWDisconnectGeneration();on_fw_disconnect();
    [host observePeer:FWPeerMissing at:75000 generation:generation disconnects:disconnects];assert(!host.failure);
    host.generation++;
    [host observePeer:FWPeerMissing at:75000 generation:generation disconnects:FWDisconnectGeneration()];assert(!host.failure);
    // At least three observations are required; gaps never count as continuity.
    host=[MissingHost new];Observe(host,FWPeerMissing,1000);Observe(host,FWPeerMissing,16000);assert(!host.failure);

    host=[MissingHost new];
    host.operation=[OSSystemExtensionRequest activationRequestForExtension:[host extensionIdentifier] queue:dispatch_get_main_queue()];
    MissingProperties *same=Version(@"0.0.4"),*old=Version(@"0.0.3");
    assert([host request:host.operation actionForReplacingExtension:(id)same withExtension:(id)same]==OSSystemExtensionReplacementActionCancel);
    assert([host request:host.operation actionForReplacingExtension:(id)old withExtension:(id)same]==OSSystemExtensionReplacementActionReplace);
    same.bundleIdentifier=@"org.example.other";
    assert([host request:host.operation actionForReplacingExtension:(id)old withExtension:(id)same]==OSSystemExtensionReplacementActionCancel);
    puts("Native missing-service detection: windows, eligibility, stale callbacks, read-only reporting and toggle reset passed; no system operations.");
    return 0;
} }
