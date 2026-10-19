// SPDX-License-Identifier: GPL-3.0-only
// Exercise the real queue boundary with fake socket metadata and slow signing.
// No provider is installed or started; no sockets or privileged storage are used.
#import "../FWIdentity.h"
#import <stdatomic.h>
#import <assert.h>
#import <sys/resource.h>
#import <unistd.h>

static atomic_int identityCalls;
static dispatch_semaphore_t started,finishIdentity;
static NSDictionary *SlowIdentity(NSData *token) {
    (void)token;
    if (atomic_fetch_add(&identityCalls,1)==0) {
        dispatch_semaphore_signal(started);
        dispatch_semaphore_wait(finishIdentity,DISPATCH_TIME_FOREVER);
    } else usleep(50000); // A regression would cost 200+ seconds for this burst.
    return @{@"signer":@"unsigned"};
}
#define FWIdentifyToken SlowIdentity
#include "../FilterDataProvider.m"
#undef FWIdentifyToken

char *on_fw_core_call(const char *input) {
    (void)input;return strdup("{\"value\":null,\"error\":null,\"resolutions\":[]}");
}
void on_fw_core_free(char *value) {free(value);}

@interface TestService:FWService
@end
@implementation TestService
-(BOOL)prepareStorage {return YES;}
-(void)startListener {}
-(void)startTimers {}
@end

@interface TestSocket:NSObject
@property NSUUID *identifier;
@property NSData *audit;
@end
@implementation TestSocket
-(BOOL)isKindOfClass:(Class)type {return type==NEFilterSocketFlow.class || [super isKindOfClass:type];}
-(NETrafficDirection)direction {return NETrafficDirectionOutbound;}
-(NWEndpoint *)remoteEndpoint {return [NWHostEndpoint endpointWithHostname:@"192.0.2.1" port:@"443"];}
-(int)socketProtocol {return IPPROTO_TCP;}
// Distinct identities prevent the token cache from hiding a queue regression.
-(NSData *)sourceAppAuditToken {return self.audit?:[self.identifier.UUIDString dataUsingEncoding:NSUTF8StringEncoding];}
@end

static double CPU(void) {
    struct rusage r;getrusage(RUSAGE_SELF,&r);
    return r.ru_utime.tv_sec+r.ru_utime.tv_usec/1e6+r.ru_stime.tv_sec+r.ru_stime.tv_usec/1e6;
}
static TestSocket *Socket(void) {TestSocket *flow=[TestSocket new];flow.identifier=NSUUID.UUID;return flow;}
int main(void) { @autoreleasepool {
    started=dispatch_semaphore_create(0);finishIdentity=dispatch_semaphore_create(0);
    TestService *service=[TestService new];
    dispatch_sync(service.queue,^{service.filtering=YES;});
    TestSocket *own=Socket();audit_token_t audit={0};audit.val[5]=(uint32_t)getpid();
    own.audit=[NSData dataWithBytes:&audit length:sizeof(audit)];
    [service handleNewFlow:(id)own];assert(service.paused.count==0 && atomic_load(&identityCalls)==0);
    [service handleNewFlow:(id)Socket()];
    assert(dispatch_semaphore_wait(started,dispatch_time(DISPATCH_TIME_NOW,2*NSEC_PER_SEC))==0);
    for (int i=1;i<4096;i++) [service handleNewFlow:(id)Socket()];
    // Simulate the independent watchdog releasing the entire burst while the
    // first signature is still running, exactly as in the reported failure.
    [service releaseAll];
    double cpu=CPU();uint64_t wall=FWMonotonic();
    dispatch_semaphore_signal(finishIdentity);
    dispatch_semaphore_t drained=dispatch_semaphore_create(0);
    dispatch_async(service.queue,^{dispatch_semaphore_signal(drained);});
    assert(dispatch_semaphore_wait(drained,dispatch_time(DISPATCH_TIME_NOW,3*NSEC_PER_SEC))==0);
    assert(atomic_load(&identityCalls)==1);
    assert(service.paused.count==0);
    double used=CPU()-cpu;assert(used<1.0);
    printf("expired burst: flows=4096 identity_calls=%d drain_ms=%llu cpu_ms=%.2f\n",
        atomic_load(&identityCalls),(unsigned long long)(FWMonotonic()-wall),used*1000);
    return 0;
} }
