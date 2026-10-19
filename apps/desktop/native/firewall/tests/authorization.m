// SPDX-License-Identifier: GPL-3.0-only
// Real filesystem permission failures with deterministic Security API responses.
// No XPC, system-extension requests, signing credentials or privileged operations.
#import "../FWIdentity.h"
#import <Security/Security.h>
#import <libproc.h>
#import <sys/stat.h>
#import <assert.h>

static OSStatus guestStatus=errSecSuccess,validity=errSecSuccess;
static NSString *imagePath;
static NSUInteger validations;
static OSStatus TestSelf(SecCSFlags flags,SecCodeRef *code) {
    (void)flags;*code=(SecCodeRef)CFRetain(CFSTR("self"));return errSecSuccess;
}
static OSStatus TestInfo(SecStaticCodeRef code,SecCSFlags flags,CFDictionaryRef *info) {
    (void)code;(void)flags;
    *info=CFBridgingRetain(@{(__bridge NSString *)kSecCodeInfoTeamIdentifier:@"EXAMPLETEAM"});return errSecSuccess;
}
static OSStatus TestGuest(SecCodeRef host,CFDictionaryRef attributes,SecCSFlags flags,SecCodeRef *code) {
    (void)host;(void)attributes;(void)flags;
    if(guestStatus==errSecSuccess) *code=(SecCodeRef)CFRetain(CFSTR("guest"));return guestStatus;
}
static OSStatus TestValidity(SecCodeRef code,SecCSFlags flags,SecRequirementRef requirement) {
    (void)code;assert(flags==kSecCSStrictValidate && requirement);validations++;return validity;
}
static int TestPath(int pid,void *buffer,uint32_t size) {
    assert(pid==42);return imagePath?(int)strlcpy(buffer,imagePath.fileSystemRepresentation,size):0;
}
#define SecCodeCopySelf TestSelf
#define SecCodeCopySigningInformation TestInfo
#define SecCodeCopyGuestWithAttributes TestGuest
#define SecCodeCheckValidity TestValidity
#define proc_pidpath TestPath
#include "../FWIdentity.m"

int main(void) { @autoreleasepool {
    assert(geteuid()!=0); // This regression exercises ordinary-user access.
    NSFileManager *fm=NSFileManager.defaultManager;
    NSString *root=[NSTemporaryDirectory() stringByAppendingPathComponent:NSUUID.UUID.UUIDString];
    NSString *contents=[root stringByAppendingPathComponent:@"Fixture.systemextension/Contents"];
    NSString *macOS=[contents stringByAppendingPathComponent:@"MacOS"];
    NSString *seal=[contents stringByAppendingPathComponent:@"_CodeSignature"];
    assert([fm createDirectoryAtPath:macOS withIntermediateDirectories:YES attributes:nil error:nil]);
    assert([fm createDirectoryAtPath:seal withIntermediateDirectories:YES attributes:nil error:nil]);
    imagePath=[macOS stringByAppendingPathComponent:@"fixture"];
    NSArray *files=@[imagePath,[contents stringByAppendingPathComponent:@"Info.plist"],[seal stringByAppendingPathComponent:@"CodeResources"]];
    for(NSString *file in files) assert([[@"fixture" dataUsingEncoding:NSUTF8StringEncoding] writeToFile:file atomically:YES]);
    audit_token_t token={0};token.val[5]=42;
    NSString *detail=@"stale";
    assert(FWAuthorizeWithDetail(token,@"org.example.firewall",&detail) && detail==nil && validations==1);
    validity=errSecCSReqFailed;
    assert(!FWAuthorizeWithDetail(token,@"org.example.firewall",&detail) && detail==nil);
    for(NSString *protectedPath in @[macOS,imagePath,files[1],files[2]]) {
        struct stat before;assert(stat(protectedPath.fileSystemRepresentation,&before)==0);
        assert(chmod(protectedPath.fileSystemRepresentation,0)==0);
        for(NSNumber *guest in @[@(errSecSuccess),@(errSecCSStaticCodeNotFound)]) {
            guestStatus=guest.intValue;
            assert(!FWAuthorizeWithDetail(token,@"org.example.firewall",&detail));
            assert([detail isEqual:@"扩展文件权限异常，请重新安装"]);
            assert(!FWAuthorize(token,@"org.example.firewall"));
        }
        assert(chmod(protectedPath.fileSystemRepresentation,before.st_mode&0777)==0);
    }
    guestStatus=errSecSuccess;
    imagePath=[root stringByAppendingPathComponent:@"missing"];
    assert(!FWAuthorizeWithDetail(token,@"org.example.firewall",&detail) && detail==nil);
    imagePath=nil;
    assert(!FWAuthorizeWithDetail(token,@"org.example.firewall",&detail) && detail==nil);
    assert(!FWAuthorizeWithDetail(token,@"invalid\"requirement",&detail) && detail==nil);
    validity=errSecSuccess;
    assert(FWAuthorizeWithDetail(token,@"org.example.firewall",&detail) && detail==nil);
    assert([fm removeItemAtPath:root error:nil]);
    puts("Native authorization permissions: denial stays denied; unreadable image is distinguished from signature/missing-image errors.");
    return 0;
} }
