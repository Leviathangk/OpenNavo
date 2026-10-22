// SPDX-License-Identifier: GPL-3.0-only
// Cache only this running image's identity, including a successful unsigned read.
#import "../FWIdentity.h"
#import <Security/Security.h>
#import <assert.h>

static OSStatus selfStatus=errSecSuccess,infoStatus=errSecSuccess;
static NSUInteger selfReads,infoReads;
static BOOL unsignedImage;
static OSStatus TestSelf(SecCSFlags flags,SecCodeRef *code) {
    (void)flags;selfReads++;
    if(selfStatus==errSecSuccess) *code=(SecCodeRef)CFRetain(CFSTR("self"));
    return selfStatus;
}
static OSStatus TestInfo(SecStaticCodeRef code,SecCSFlags flags,CFDictionaryRef *info) {
    (void)code;(void)flags;infoReads++;
    if(infoStatus==errSecSuccess) *info=CFBridgingRetain(unsignedImage?@{}:@{(__bridge NSString *)kSecCodeInfoTeamIdentifier:@"EXAMPLETEAM"});
    return infoStatus;
}
#define SecCodeCopySelf TestSelf
#define SecCodeCopySigningInformation TestInfo
#include "../FWIdentity.m"

int main(int argc,const char **argv) { @autoreleasepool {
    unsignedImage=argc>1 && strcmp(argv[1],"--unsigned")==0;
    selfStatus=errSecNotAvailable;
    assert(FWOwnTeam()==nil && selfReads==1 && infoReads==0);
    selfStatus=errSecSuccess;infoStatus=errSecNotAvailable;
    assert(FWOwnTeam()==nil && selfReads==2 && infoReads==1);
    infoStatus=errSecSuccess;
    dispatch_apply(1000,dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^(size_t index){
        (void)index;
        @autoreleasepool {
            NSString *team=FWOwnTeam();
            assert(unsignedImage?team==nil:[team isEqual:@"EXAMPLETEAM"]);
        }
    });
    assert(selfReads==3 && infoReads==2);
    puts("Own signing identity: retries failures, then reads once across concurrent calls.");
    return 0;
} }
