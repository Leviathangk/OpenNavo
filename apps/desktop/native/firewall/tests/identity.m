// SPDX-License-Identifier: GPL-3.0-only
// Run the real identity boundary with deterministic OS responses. No extension,
// signing identity, installation, privileged process or network access is needed.
#import "../FWIdentity.h"
#import <Security/Security.h>
#import <libproc.h>
#import <sys/sysctl.h>
#import <mach/mach_time.h>
#import <assert.h>

static OSStatus guestStatus=errSecSuccess,validity=errSecSuccess,staticCopyStatus=errSecSuccess;
static NSData *liveHash,*diskHash;
static BOOL pathAvailable=YES;
static int argumentReads=0,processReads=0;
static int staticChecks=0,dynamicChecks=0;
static uint64_t processStart=10;
static BOOL mockCertificate=NO;
static NSDictionary *certificateValues;
static CFDictionaryRef TestCertificateValues(SecCertificateRef certificate,CFArrayRef keys,CFErrorRef *error) {
    if (!mockCertificate) return SecCertificateCopyValues(certificate,keys,error);
    assert(([(__bridge NSArray *)keys isEqual:@[(__bridge NSString *)kSecOIDX509V1SubjectName]]));
    return certificateValues?CFBridgingRetain(certificateValues):NULL;
}
static OSStatus TestGuest(SecCodeRef host,CFDictionaryRef attributes,SecCSFlags flags,SecCodeRef *code) {
    (void)host;(void)attributes;(void)flags;
    if (guestStatus==errSecSuccess) *code=(SecCodeRef)CFRetain(CFSTR("running-fixture"));
    return guestStatus;
}
static OSStatus TestInfo(SecStaticCodeRef code,SecCSFlags flags,CFDictionaryRef *info) {
    if (CFEqual(code,CFSTR("running-fixture"))) {
        *info=CFBridgingRetain(liveHash?@{(__bridge NSString *)kSecCodeInfoUnique:liveHash}:@{});
        return errSecSuccess;
    }
    if (CFEqual(code,CFSTR("static-fixture"))) {
        *info=CFBridgingRetain(diskHash?@{(__bridge NSString *)kSecCodeInfoUnique:diskHash}:@{});
        return errSecSuccess;
    }
    return SecCodeCopySigningInformation(code,flags,info);
}
static OSStatus TestCopyStaticCode(SecCodeRef code,SecCSFlags flags,SecStaticCodeRef *disk) {
    assert(CFEqual(code,CFSTR("running-fixture")) && flags==kSecCSDefaultFlags);
    if (staticCopyStatus==errSecSuccess) *disk=(SecStaticCodeRef)CFRetain(CFSTR("static-fixture"));
    return staticCopyStatus;
}
static OSStatus TestValidity(SecCodeRef code,SecCSFlags flags,SecRequirementRef requirement) {
    (void)code;(void)flags;(void)requirement;dynamicChecks++;return validity;
}
static OSStatus TestStaticValidity(SecStaticCodeRef code,SecCSFlags flags,SecRequirementRef requirement) {
    assert(flags&kSecCSStrictValidate);
    assert(flags&kSecCSDoNotValidateResources);
    staticChecks++;return SecStaticCodeCheckValidity(code,flags,requirement);
}
static int TestPath(int pid,void *buffer,uint32_t size) {
    (void)pid;if (!pathAvailable) return 0;
    return (int)strlcpy(buffer,"/bin/echo",size);
}
static int TestSysctl(int *mib,u_int length,void *out,size_t *size,void *in,size_t inSize) {
    (void)mib;(void)length;(void)out;(void)size;(void)in;(void)inSize;
    argumentReads++;return -1;
}
static int TestProcInfo(int pid,int flavor,uint64_t arg,void *buffer,int size) {
    (void)pid;(void)flavor;(void)arg;assert(size==sizeof(struct proc_bsdinfo));
    processReads++;struct proc_bsdinfo info={0};info.pbi_start_tvusec=(uint32_t)processStart;
    info.pbi_ppid=1;memcpy(buffer,&info,sizeof(info));return sizeof(info);
}
#define SecCodeCopyGuestWithAttributes TestGuest
#define SecCodeCopySigningInformation TestInfo
#define SecCodeCopyStaticCode TestCopyStaticCode
#define SecCodeCheckValidity TestValidity
#define SecStaticCodeCheckValidity TestStaticValidity
#define SecCertificateCopyValues TestCertificateValues
#define proc_pidpath TestPath
#define sysctl TestSysctl
#define proc_pidinfo TestProcInfo
#include "../FWIdentity.m"

static void ExpectInvalid(NSData *token) {
    NSDictionary *app=FWIdentifyToken(token);
    assert(app && [app[@"signer"] isEqual:@"invalid"]);
    assert([app[@"key"] isEqual:@"path:/bin/echo"]);
    assert(app[@"teamId"]==NSNull.null && app[@"signingId"]==NSNull.null);
    assert(app[@"bundlePath"]==NSNull.null);
}
static NSDictionary *Property(NSString *label,id value) {
    return @{(__bridge NSString *)kSecPropertyKeyLabel:label,(__bridge NSString *)kSecPropertyKeyValue:value,
        (__bridge NSString *)kSecPropertyKeyLocalizedLabel:@"Localized label"};
}
int main(void) { @autoreleasepool {
    mockCertificate=YES;
    NSDictionary *certInfo=@{(__bridge NSString *)kSecCodeInfoCertificates:@[NSNull.null]};
    NSString *subject=(__bridge NSString *)kSecOIDX509V1SubjectName;
    NSString *organization=(__bridge NSString *)kSecOIDOrganizationName;
    certificateValues=@{subject:Property(subject,@[Property(@"2.5.4.11",@"EXAMPLETEAM"),Property(organization,@"Example Developer LLC")])};
    assert([FWOrganization(certInfo) isEqual:@"Example Developer LLC"]);
    // Issuer and OU must never be mistaken for the leaf subject's organization.
    certificateValues=@{subject:Property(subject,@[Property(@"2.5.4.11",@"EXAMPLETEAM")]),
        (__bridge NSString *)kSecOIDX509V1IssuerName:Property(@"issuer",@[Property(organization,@"Example Issuer")])};
    assert(FWOrganization(certInfo)==nil);
    certificateValues=@{subject:Property(subject,@[Property(organization,NSNull.null),Property(organization,@"  ")])};
    assert(FWOrganization(certInfo)==nil);
    certificateValues=@{subject:Property(subject,@[Property(@"section",@[Property(organization,@" Example Developer LLC \n")])])};
    assert([FWOrganization(certInfo) isEqual:@"Example Developer LLC"]);
    certificateValues=nil;assert(FWOrganization(certInfo)==nil);assert(FWOrganization(@{})==nil);
    mockCertificate=NO;

    assert([FWDisplayName(@"/opt/example/versions/2.1.302",nil,@"com.example.claude-code") isEqual:@"claude-code"]);
    assert([FWDisplayName(@"/opt/tools/strange-file",nil,@"org.example.example-tool") isEqual:@"example-tool"]);
    assert([FWDisplayName(@"/opt/example/versions/2.1.302",nil,nil) isEqual:@"example"]);
    assert([FWDisplayName(@"/opt/example/v2.1.302-beta.1",nil,@"org.example.123") isEqual:@"example"]);
    assert([FWDisplayName(@"/usr/local/bin/tool",nil,nil) isEqual:@"tool"]);
    assert([FWDisplayName(@"/usr/libexec/timed",nil,nil) isEqual:@"timed"]);
    assert([FWDisplayName(@"/System/Library/PrivateFrameworks/ApplePushService.framework/apsd",nil,nil) isEqual:@"apsd"]);
    assert([FWDisplayName(@"/opt/claude/versions/2.1.302",nil,nil) isEqual:@"claude"]);
    assert([FWDisplayName(@"/Applications/Example.app/Contents/MacOS/helper",nil,@"org.example.tool") isEqual:@"helper"]);
    assert([FWDisplayName(@"/Applications/Example.app/Contents/MacOS/2.1.302",nil,@"org.example.tool") isEqual:@"tool"]);
    audit_token_t audit={0};audit.val[5]=42;
    NSData *token=[NSData dataWithBytes:&audit length:sizeof(audit)];
    validity=errSecCSReqFailed;ExpectInvalid(token);
    validity=errSecSuccess;guestStatus=errSecParam;ExpectInvalid(token);
    guestStatus=errSecSuccess;diskHash=[@"disk-image" dataUsingEncoding:NSUTF8StringEncoding];
    liveHash=[@"different-image" dataUsingEncoding:NSUTF8StringEncoding];ExpectInvalid(token);
    liveHash=nil;ExpectInvalid(token);
    liveHash=diskHash;diskHash=nil;ExpectInvalid(token);
    liveHash=nil;ExpectInvalid(token);
    staticCopyStatus=errSecParam;ExpectInvalid(token);staticCopyStatus=errSecSuccess;
    // The same path with a verified matching live image keeps its signing identity.
    SecStaticCodeRef disk=NULL;
    assert(SecStaticCodeCreateWithPath((__bridge CFURLRef)[NSURL fileURLWithPath:@"/bin/echo"],0,&disk)==errSecSuccess);
    liveHash=FWCodeInfo(disk)[(__bridge NSString *)kSecCodeInfoUnique];diskHash=liveHash;CFRelease(disk);
    assert([FWIdentifyToken(token)[@"signer"] isEqual:@"apple"]);
    int checks=staticChecks,dynamic=dynamicChecks;
    assert([FWIdentifyToken(token)[@"signer"] isEqual:@"apple"]);
    assert(staticChecks==checks && dynamicChecks==dynamic+1);
    // Cached static trust must never mask a new live audit-token failure.
    validity=errSecCSReqFailed;ExpectInvalid(token);validity=errSecSuccess;
    pathAvailable=NO;assert(FWIdentifyToken(token)==nil);pathAvailable=YES;
    assert(FWIdentifyToken([NSData data])==nil);
    audit.val[5]=0;assert(FWIdentifyToken([NSData dataWithBytes:&audit length:sizeof(audit)])==nil);

    assert(FWProcessDetails(@"allow",42,10)==nil);
    assert(FWProcessDetails(@"block",42,10)==nil);
    assert(argumentReads==0 && processReads==0);
    assert(FWProcessDetails(@"pause",42,10)!=nil);
    assert(argumentReads==1 && processReads==3);
    // A recycled PID must not expose the new process's arguments in an old alert.
    processStart=11;argumentReads=0;
    assert(FWProcessDetails(@"pause",42,10)==nil);
    assert(argumentReads==0);

    NSString *folder=[NSTemporaryDirectory() stringByAppendingPathComponent:NSUUID.UUID.UUIDString];
    NSFileManager *fm=NSFileManager.defaultManager;
    assert([fm createDirectoryAtPath:folder withIntermediateDirectories:YES attributes:nil error:nil]);
    NSString *copy=[folder stringByAppendingPathComponent:@"echo"];
    assert([fm copyItemAtPath:@"/bin/echo" toPath:copy error:nil]);
    assert(FWIdentifyPath(copy));checks=staticChecks;
    assert(FWIdentifyPath(copy));assert(staticChecks==checks);
    NSString *versioned=[folder stringByAppendingPathComponent:@"2.1.302"];
    assert([fm copyItemAtPath:@"/bin/echo" toPath:versioned error:nil]);
    NSDictionary *original=FWIdentifyPath(@"/bin/echo"),*renamed=FWIdentifyPath(versioned);
    assert([renamed[@"name"] isEqual:@"echo"]);
    assert([renamed[@"key"] isEqual:original[@"key"]] && [renamed[@"signingId"] isEqual:original[@"signingId"]]);
    assert([renamed[@"path"] isEqual:versioned]);checks=staticChecks;
    assert([fm setAttributes:@{NSFileModificationDate:[NSDate dateWithTimeIntervalSince1970:1]} ofItemAtPath:copy error:nil]);
    assert(FWIdentifyPath(copy));assert(staticChecks>checks);checks=staticChecks;
    NSString *replacement=[folder stringByAppendingPathComponent:@"replacement"];
    assert([fm copyItemAtPath:@"/bin/echo" toPath:replacement error:nil]);
    assert(rename(replacement.fileSystemRepresentation,copy.fileSystemRepresentation)==0);
    assert(FWIdentifyPath(copy));assert(staticChecks>checks);
    NSString *bundle=[folder stringByAppendingPathComponent:@"Fixture.app"];
    NSString *macOS=[bundle stringByAppendingPathComponent:@"Contents/MacOS"];
    assert([fm createDirectoryAtPath:macOS withIntermediateDirectories:YES attributes:nil error:nil]);
    assert([fm copyItemAtPath:@"/bin/echo" toPath:[macOS stringByAppendingPathComponent:@"fixture"] error:nil]);
    assert(([@{@"CFBundleExecutable":@"fixture",@"CFBundleIdentifier":@"org.example.fixture",@"CFBundleDisplayName":@"Example App"}
        writeToFile:[bundle stringByAppendingPathComponent:@"Contents/Info.plist"] atomically:YES]));
    assert([FWBasicIdentity(bundle)[@"name"] isEqual:@"Example App"]);checks=staticChecks;
    assert(FWBasicIdentity(bundle));assert(staticChecks==checks);
    NSString *seal=[bundle stringByAppendingPathComponent:@"Contents/_CodeSignature"];
    assert([fm createDirectoryAtPath:seal withIntermediateDirectories:YES attributes:nil error:nil]);
    assert([@"fixture" writeToFile:[seal stringByAppendingPathComponent:@"CodeResources"] atomically:YES encoding:NSUTF8StringEncoding error:nil]);
    assert(FWBasicIdentity(bundle));assert(staticChecks>checks);
    __block int found=0;
    FWScanInstalledApps(@[folder],FWMonotonic(),^(NSDictionary *app){(void)app;found++;});
    assert(found==0);
    assert([fm removeItemAtPath:folder error:nil]);
    return 0;
} }
