// SPDX-License-Identifier: GPL-3.0-only
// A state file the core can't restore is set aside instead of disabling the firewall;
// file type stays fail-closed; fresh setup and reset discard set-aside copies and
// persist. Uses a private temporary directory only; no provider, XPC or system state.
#import "../FWIdentity.h"
#import <assert.h>
#include "../FilterDataProvider.m"

static NSMutableArray<NSString *> *ops;
static const char *FWReply(NSString *op,NSDictionary *request) {
    if ([op isEqual:@"restore"]) return [request[@"state"][@"version"] isEqual:@1]
        ?"{\"value\":null,\"error\":null,\"resolutions\":[]}"
        :"{\"value\":null,\"error\":{\"code\":\"E_FW_DISCONNECTED\",\"message\":\"firewall_request\",\"detail\":null},\"resolutions\":[]}";
    if ([op isEqual:@"persist"]) return "{\"value\":{\"version\":1,\"persisted\":true},\"error\":null,\"resolutions\":[]}";
    if ([op isEqual:@"configure"]) return "{\"value\":{\"accepted\":true,\"scan\":false},\"error\":null,\"resolutions\":[]}";
    return "{\"value\":null,\"error\":null,\"resolutions\":[]}";
}
char *on_fw_core_call(const char *input) {
    NSDictionary *request=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:input length:strlen(input)] options:0 error:nil];
    [ops addObject:request[@"op"]];
    return strdup(FWReply(request[@"op"],request));
}
void on_fw_core_free(char *value) {free(value);}

@interface StateService:FWService
@end
@implementation StateService
-(BOOL)prepareStorage {return YES;}
-(void)startListener {}
-(void)startTimers {}
@end

static NSString *folder,*statePath;
static NSArray<NSString *> *Aside(void) {
    NSArray *names=[NSFileManager.defaultManager contentsOfDirectoryAtPath:folder error:nil];
    return [names filteredArrayUsingPredicate:[NSPredicate predicateWithFormat:@"SELF BEGINSWITH 'state.json.unreadable-'"]];
}
static void Reset(void) {
    for (NSString *name in [NSFileManager.defaultManager contentsOfDirectoryAtPath:folder error:nil])
        [NSFileManager.defaultManager removeItemAtPath:[folder stringByAppendingPathComponent:name] error:nil];
    [ops removeAllObjects];
}
static BOOL Restore(StateService *service) {
    __block BOOL restored=NO;
    dispatch_sync(service.queue,^{service.statePath=statePath;restored=[service restoreState];});
    return restored;
}
static void Write(NSString *text) {assert([text writeToFile:statePath atomically:YES encoding:NSUTF8StringEncoding error:nil]);}

int main(void) { @autoreleasepool {
    ops=[NSMutableArray array];
    char path[]="/tmp/opennavo-firewall-state-XXXXXX";
    assert(mkdtemp(path));
    folder=@(path);statePath=[folder stringByAppendingPathComponent:@"state.json"];
    StateService *service=[StateService new];

    // Nothing stored yet.
    assert(Restore(service) && ops.count==0 && Aside().count==0);

    // A torn write and a file the core rejects are both kept aside; filtering continues.
    for (NSString *text in @[@"{\"version\":1,\"rul",@"{\"version\":2}"]) {
        Reset();Write(text);
        assert(Restore(service));
        assert(![NSFileManager.defaultManager fileExistsAtPath:statePath] && Aside().count==1);
        NSString *kept=[NSString stringWithContentsOfFile:[folder stringByAppendingPathComponent:Aside()[0]] encoding:NSUTF8StringEncoding error:nil];
        assert([kept isEqual:text]);
        assert([ops containsObject:@"restore"]==[text hasSuffix:@"}"]);
    }

    // A good file is restored in place.
    Reset();Write(@"{\"version\":1}");
    assert(Restore(service) && [ops isEqual:@[@"restore"]]);
    assert([NSFileManager.defaultManager fileExistsAtPath:statePath] && Aside().count==0);

    // Not a regular file: still fail closed, and nothing is moved.
    Reset();
    NSString *target=[folder stringByAppendingPathComponent:@"elsewhere.json"];
    assert([@"{\"version\":1}" writeToFile:target atomically:YES encoding:NSUTF8StringEncoding error:nil]);
    assert(symlink(target.fileSystemRepresentation,statePath.fileSystemRepresentation)==0);
    assert(!Restore(service) && ops.count==0 && Aside().count==0);

    // A fresh setup and a reset discard set-aside copies, and a reset is on disk before its reply.
    for (int scenario=0;scenario<3;scenario++) {
        Reset();
        NSString *aside=[statePath stringByAppendingString:@".unreadable-1"];
        assert([@"old rules" writeToFile:aside atomically:YES encoding:NSUTF8StringEncoding error:nil]);
        __block BOOL replied=NO;
        dispatch_sync(service.queue,^{
            service.statePath=statePath;service.healthy=YES;
            NSDictionary *request=scenario==2?@{@"op":@"reset"}
                :@{@"op":@"configure",@"id":[NSString stringWithFormat:@"setup-%d",scenario],@"settings":@{},@"fresh":@(scenario==1)};
            [service handleRequest:request completion:^(NSDictionary *response){
                assert(response[@"error"]==NSNull.null);
                if (scenario==2) {
                    NSDictionary *saved=[NSJSONSerialization JSONObjectWithData:[NSData dataWithContentsOfFile:statePath] options:0 error:nil];
                    assert([saved[@"persisted"] isEqual:@YES]);
                    assert([ops indexOfObject:@"persist"]>[ops indexOfObject:@"reset"]);
                }
                replied=YES;
            }];
        });
        assert(replied && [NSFileManager.defaultManager fileExistsAtPath:statePath]);
        assert(Aside().count==(scenario==0?1:0));
    }
    [NSFileManager.defaultManager removeItemAtPath:folder error:nil];
    puts("Native state recovery: unreadable state is set aside, file type fails closed, fresh setup and reset discard old copies.");
    return 0;
} }
