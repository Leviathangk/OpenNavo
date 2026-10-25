// SPDX-License-Identifier: GPL-3.0-only
// Flows that can't wait for an answer still get the core's verdict: late in the queue,
// without identity, or late after signing. No provider, socket, XPC or storage is used.
#import "../FWIdentity.h"
#import <assert.h>

static dispatch_semaphore_t identityStarted,finishIdentity;
static int identityCalls;
static BOOL identityFails,identitySlow;
static NSDictionary *ControlledIdentity(NSData *token) {
    (void)token;identityCalls++;
    if (identitySlow) {dispatch_semaphore_signal(identityStarted);dispatch_semaphore_wait(finishIdentity,DISPATCH_TIME_FOREVER);}
    if (identityFails) return nil;
    return @{@"key":@"path:/fixtures/late",@"name":@"late",@"path":@"/fixtures/late",
        @"bundlePath":NSNull.null,@"bundleId":NSNull.null,@"signingId":NSNull.null,
        @"teamId":NSNull.null,@"teamName":NSNull.null,@"signer":@"unsigned",@"iconPath":NSNull.null};
}
#define FWIdentifyToken ControlledIdentity
#include "../FilterDataProvider.m"
#undef FWIdentifyToken

static NSMutableArray<NSDictionary *> *requests;
char *on_fw_core_call(const char *input) {
    NSDictionary *request=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:input length:strlen(input)] options:0 error:nil];
    @synchronized(requests) {[requests addObject:request];}
    // Destination policy blocks everything these flows reach; a question would pause.
    if ([request[@"op"] isEqual:@"bypass"]) return strdup("{\"value\":{\"verdict\":\"block\"},\"error\":null,\"resolutions\":[]}");
    if ([request[@"op"] isEqual:@"flow"]) return strdup("{\"value\":{\"verdict\":\"pause\"},\"error\":null,\"resolutions\":[]}");
    return strdup("{\"value\":null,\"error\":null,\"resolutions\":[]}");
}
void on_fw_core_free(char *value) {free(value);}

@interface LateService:FWService
@property NSMutableArray<NSString *> *actions;
@end
@implementation LateService
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

@interface LateSocket:NSObject
@property NSUUID *identifier;
@end
@implementation LateSocket
-(BOOL)isKindOfClass:(Class)type {return type==NEFilterSocketFlow.class || [super isKindOfClass:type];}
-(NETrafficDirection)direction {return NETrafficDirectionOutbound;}
-(NWEndpoint *)remoteEndpoint {return [NWHostEndpoint endpointWithHostname:@"192.0.2.1" port:@"443"];}
-(int)socketProtocol {return IPPROTO_TCP;}
-(NSData *)sourceAppAuditToken {return [@"late-token" dataUsingEncoding:NSUTF8StringEncoding];}
-(NSString *)remoteHostname {return @"blocked.example";}
-(NSURL *)URL {return nil;}
@end

static LateService *Service(void) {
    LateService *service=[LateService new];service.actions=[NSMutableArray array];
    dispatch_sync(service.queue,^{service.filtering=YES;});
    identityCalls=0;identityFails=NO;identitySlow=NO;
    @synchronized(requests) {[requests removeAllObjects];}
    return service;
}
static LateSocket *Socket(void) {LateSocket *socket=[LateSocket new];socket.identifier=NSUUID.UUID;return socket;}
static NSDictionary *Bypass(void) {
    @synchronized(requests) {
        NSArray *found=[requests filteredArrayUsingPredicate:[NSPredicate predicateWithFormat:@"op == 'bypass'"]];
        assert(found.count==1);
        return found[0];
    }
}

int main(void) { @autoreleasepool {
    requests=[NSMutableArray array];

    // No identifier: nothing could resume it later, so it isn't paused at all.
    LateService *service=Service();
    [service handleNewFlow:(id)[LateSocket new]];
    dispatch_sync(service.queue,^{});
    assert(service.paused.count==0 && identityCalls==0 && requests.count==0);

    // Signing failed: destination policy still decides, nothing is asked.
    service=Service();identityFails=YES;
    [service handleNewFlow:(id)Socket()];
    dispatch_sync(service.queue,^{});
    NSDictionary *bypass=Bypass();
    assert(![bypass[@"identified"] boolValue] && ![bypass[@"released"] boolValue]);
    assert([bypass[@"flow"][@"app"][@"signer"] isEqual:@"invalid"]);
    assert([bypass[@"flow"][@"remoteHost"] isEqual:@"blocked.example"]);
    assert(service.actions.count==1 && [service.actions[0] isEqual:@"block"] && service.paused.count==0);

    // Past its policy budget in the queue: no signing work, with or without a cached identity.
    for(int cached=0;cached<2;cached++) {
        service=Service();
        if(cached) service.identities[[@"late-token" dataUsingEncoding:NSUTF8StringEncoding]]=@{@"app":ControlledIdentity(nil),@"at":@(FWMonotonic())};
        identityCalls=0;
        dispatch_semaphore_t gate=dispatch_semaphore_create(0);
        dispatch_async(service.queue,^{dispatch_semaphore_wait(gate,DISPATCH_TIME_FOREVER);});
        LateSocket *socket=Socket();
        [service handleNewFlow:(id)socket];
        [service.pausedLock lock];
        NSMutableDictionary *entry=[service.paused[socket.identifier.UUIDString] mutableCopy];
        entry[@"policyDeadline"]=@0;service.paused[socket.identifier.UUIDString]=entry;
        [service.pausedLock unlock];
        dispatch_semaphore_signal(gate);
        dispatch_sync(service.queue,^{});
        bypass=Bypass();
        assert(identityCalls==0);
        assert([bypass[@"identified"] boolValue]==(cached==1) && ![bypass[@"released"] boolValue]);
        assert([bypass[@"flow"][@"app"][@"signer"] isEqual:cached?@"unsigned":@"invalid"]);
        assert(service.actions.count==1 && [service.actions[0] isEqual:@"block"]);
    }

    // Identified, but signing took longer than the one-second budget.
    service=Service();identitySlow=YES;
    identityStarted=dispatch_semaphore_create(0);finishIdentity=dispatch_semaphore_create(0);
    [service handleNewFlow:(id)Socket()];
    assert(dispatch_semaphore_wait(identityStarted,dispatch_time(DISPATCH_TIME_NOW,2*NSEC_PER_SEC))==0);
    usleep(1100*1000);
    dispatch_semaphore_signal(finishIdentity);
    dispatch_sync(service.queue,^{});
    bypass=Bypass();
    assert([bypass[@"identified"] boolValue] && ![bypass[@"released"] boolValue]);
    assert(service.actions.count==1 && [service.actions[0] isEqual:@"block"]);

    // In time and identified: the ordinary path may pause for a question.
    service=Service();
    [service handleNewFlow:(id)Socket()];
    dispatch_sync(service.queue,^{});
    @synchronized(requests) {assert([[requests valueForKey:@"op"] containsObject:@"flow"] && ![[requests valueForKey:@"op"] containsObject:@"bypass"]);}
    assert(service.actions.count==0 && service.paused.count==1);
    puts("Late native policy: unidentified and late flows get destination/rule verdicts; no identifier never pauses.");
    return 0;
} }
