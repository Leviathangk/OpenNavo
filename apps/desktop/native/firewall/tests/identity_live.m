// SPDX-License-Identifier: GPL-3.0-only
// Real Security.framework regression: inspect only our own multi-architecture
// Apple child. No system extension, privileged access or network traffic.
#import "../FWIdentity.h"
#import <Security/Security.h>
#import <mach/mach.h>
#import <mach-o/fat.h>
#import <arpa/inet.h>

static void Check(BOOL condition,NSString *message) {
    if (!condition) @throw [NSException exceptionWithName:@"IdentityRegression" reason:message userInfo:nil];
}
static NSData *CodeHash(SecStaticCodeRef code) {
    CFDictionaryRef raw=NULL;
    Check(SecCodeCopySigningInformation(code,kSecCSSigningInformation,&raw)==errSecSuccess,@"Read code information");
    NSDictionary *info=CFBridgingRelease(raw);
    return info[(__bridge NSString *)kSecCodeInfoUnique];
}
int main(void) { @autoreleasepool {
    NSTask *child=[NSTask new];int result=1;
    mach_port_t task=MACH_PORT_NULL;SecCodeRef running=NULL;SecStaticCodeRef disk=NULL,pathCode=NULL;
    @try {
        NSString *path=@"/bin/sleep";
        NSFileHandle *file=[NSFileHandle fileHandleForReadingAtPath:path];
        NSData *header=[file readDataOfLength:sizeof(struct fat_header)];[file closeFile];
        Check(header.length==sizeof(struct fat_header),@"Read Mach-O header");
        struct fat_header fat;memcpy(&fat,header.bytes,sizeof(fat));
        Check(ntohl(fat.magic)==FAT_MAGIC || ntohl(fat.magic)==FAT_MAGIC_64,@"Expected a fat Apple binary");
        uint32_t slices=ntohl(fat.nfat_arch);Check(slices>1,@"Expected multiple architecture slices");
        child.executableURL=[NSURL fileURLWithPath:path];child.arguments=@[@"30"];
        NSError *error=nil;Check([child launchAndReturnError:&error],@"Launch owned sleep child");
        Check(task_name_for_pid(mach_task_self(),child.processIdentifier,&task)==KERN_SUCCESS,@"Get owned child's task name port");
        audit_token_t audit={0};mach_msg_type_number_t count=TASK_AUDIT_TOKEN_COUNT;
        Check(task_info(task,TASK_AUDIT_TOKEN,(task_info_t)&audit,&count)==KERN_SUCCESS,@"Read real audit token");
        Check(count==TASK_AUDIT_TOKEN_COUNT && audit_token_to_pid(audit)==child.processIdentifier,@"Audit token identifies child");
        NSData *token=[NSData dataWithBytes:&audit length:sizeof(audit)];
        NSDictionary *attributes=@{(__bridge NSString *)kSecGuestAttributeAudit:token};
        Check(SecCodeCopyGuestWithAttributes(NULL,(__bridge CFDictionaryRef)attributes,kSecCSDefaultFlags,&running)==errSecSuccess,@"Resolve running code");
        Check(SecCodeCheckValidity(running,kSecCSStrictValidate,NULL)==errSecSuccess,@"Validate running code");
        Check(SecCodeCopyStaticCode(running,kSecCSDefaultFlags,&disk)==errSecSuccess,@"Resolve matching static slice");
        NSData *liveHash=CodeHash(running),*diskHash=CodeHash(disk);
        Check(liveHash.length && [liveHash isEqual:diskHash],@"Running/static slice hashes must match");
        Check(SecStaticCodeCreateWithPath((__bridge CFURLRef)child.executableURL,kSecCSDefaultFlags,&pathCode)==errSecSuccess,@"Resolve default path slice");
        BOOL defaultMatches=[liveHash isEqual:CodeHash(pathCode)];
        NSDictionary *app=FWIdentifyToken(token);
        fprintf(stdout,"/bin/sleep: slices=%u live/static=match defaultPathMatches=%s signer=%s\n",
            slices,defaultMatches?"yes":"no",[app[@"signer"] UTF8String]);
        Check([app[@"signer"] isEqual:@"apple"],@"Running Apple binary must not be invalid");
        Check([app[@"key"] hasPrefix:@"signed:"],@"Running Apple binary must keep signed identity");
        Check([app[@"path"] isEqual:path],@"Keep executable path");
        result=0;
    } @catch (NSException *exception) {
        fprintf(stderr,"Identity regression failed: %s\n",exception.reason.UTF8String);
    } @finally {
        if(pathCode) CFRelease(pathCode);
        if(disk) CFRelease(disk);
        if(running) CFRelease(running);
        if(task!=MACH_PORT_NULL) mach_port_deallocate(mach_task_self(),task);
        if(child.running) [child terminate];
        if(child.processIdentifier>0) [child waitUntilExit];
    }
    return result;
} }
