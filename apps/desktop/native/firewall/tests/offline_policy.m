// SPDX-License-Identifier: GPL-3.0-only
// Real native flow/watchdog boundary with controlled signing and policy replies.
// No provider, socket, XPC listener or privileged storage is started.
#import "../FWIdentity.h"
#import <assert.h>

static dispatch_semaphore_t identityStarted,finishIdentity;
static int policyCalls;
static BOOL askAfterReconnect;
static NSDictionary *ControlledIdentity(NSData *token) {
    (void)token;dispatch_semaphore_signal(identityStarted);
    dispatch_semaphore_wait(finishIdentity,DISPATCH_TIME_FOREVER);
    return @{@"key":@"path:/fixtures/offline",@"name":@"offline",@"path":@"/fixtures/offline",
        @"bundlePath":NSNull.null,@"bundleId":NSNull.null,@"signingId":NSNull.null,
        @"teamId":NSNull.null,@"teamName":NSNull.null,@"signer":@"unsigned",@"iconPath":NSNull.null};
}
#define FWIdentifyToken ControlledIdentity
#include "../FilterDataProvider.m"
#undef FWIdentifyToken

char *on_fw_core_call(const char *input) {
    NSDictionary *request=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:input length:strlen(input)] options:0 error:nil];
    if ([request[@"op"] isEqual:@"flow"]) {
        policyCalls++;
        if (askAfterReconnect) return strdup("{\"value\":{\"verdict\":\"pause\"},\"error\":null,\"resolutions\":[]}");
        return strdup("{\"value\":{\"verdict\":\"block\"},\"error\":null,\"resolutions\":[]}");
    }
    return strdup("{\"value\":null,\"error\":null,\"resolutions\":[]}");
}
void on_fw_core_free(char *value) {free(value);}

@interface OfflineService:FWService
@property NSMutableArray<NSString *> *actions;
@end
@implementation OfflineService
-(BOOL)prepareStorage {return YES;}
-(void)startListener {}
-(void)startTimers {}
-(void)resume:(NSString *)identifier action:(NSString *)action {
    [self.pausedLock lock];
    if(self.paused[identifier]) [self.actions addObject:action];
    [self.pausedLock unlock];
    [super resume:identifier action:action];
}
@end

@interface OfflineSocket:NSObject
@property NSUUID *identifier;
@end
@implementation OfflineSocket
-(BOOL)isKindOfClass:(Class)type {return type==NEFilterSocketFlow.class || [super isKindOfClass:type];}
-(NETrafficDirection)direction {return NETrafficDirectionOutbound;}
-(NWEndpoint *)remoteEndpoint {return [NWHostEndpoint endpointWithHostname:@"192.0.2.1" port:@"443"];}
-(int)socketProtocol {return IPPROTO_TCP;}
-(NSData *)sourceAppAuditToken {return [self.identifier.UUIDString dataUsingEncoding:NSUTF8StringEncoding];}
-(NSString *)remoteHostname {return @"example.test";}
-(NSURL *)URL {return nil;}
@end

int main(void) { @autoreleasepool {
    // No client, expired heartbeat, loss during signing, timeout, queued reconnect.
    for(int scenario=0;scenario<5;scenario++) {
        identityStarted=dispatch_semaphore_create(0);finishIdentity=dispatch_semaphore_create(0);policyCalls=0;
        askAfterReconnect=scenario==4;
        OfflineService *service=[OfflineService new];service.actions=[NSMutableArray array];
        dispatch_sync(service.queue,^{service.filtering=YES;});
        [service.pausedLock lock];
        service.lastHeartbeat=scenario==1?FWMonotonic()-5000:scenario==2?FWMonotonic():0;
        [service.pausedLock unlock];
        OfflineSocket *socket=[OfflineSocket new];socket.identifier=NSUUID.UUID;
        [service handleNewFlow:(id)socket];
        assert(dispatch_semaphore_wait(identityStarted,dispatch_time(DISPATCH_TIME_NOW,2*NSEC_PER_SEC))==0);
        [service.pausedLock lock];
        NSDictionary *entry=service.paused[socket.identifier.UUIDString];
        assert(entry && [entry[@"clientBound"] boolValue]==(scenario==2));
        service.lastHeartbeat=0;
        [service.pausedLock unlock];
        // Offline arrivals survive a watchdog pass while signing is running.
        // Existing pauses release immediately; slow offline work releases at 1s.
        [service watchdogAt:scenario==3?[entry[@"policyDeadline"] unsignedLongLongValue]:FWMonotonic()];
        assert(service.paused.count==((scenario<2 || scenario==4)?1:0));
        if(scenario==4) {
            [service.pausedLock lock];service.lastHeartbeat=FWMonotonic();[service.pausedLock unlock];
        }
        dispatch_semaphore_signal(finishIdentity);
        dispatch_sync(service.queue,^{});
        if(scenario==4) {
            assert(service.paused.count==1 && service.actions.count==0);
            assert([service.paused[socket.identifier.UUIDString][@"clientBound"] boolValue]);
            [service.pausedLock lock];service.lastHeartbeat=0;[service.pausedLock unlock];
            [service watchdogAt:FWMonotonic()];
            dispatch_sync(service.queue,^{});
        }
        assert(service.paused.count==0 && service.actions.count==1);
        assert([service.actions[0] isEqual:scenario<2?@"block":@"allow"]);
        assert(policyCalls==((scenario<2 || scenario==4)?1:0));
    }
    puts("Offline native policy: absent/stale clients enforce decisions; existing pauses and 1s watchdog still release.");
    return 0;
} }
