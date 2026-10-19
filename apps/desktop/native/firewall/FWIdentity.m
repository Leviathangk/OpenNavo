// SPDX-License-Identifier: GPL-3.0-only
// OpenNavo code-signing boundary; audit-token validation follows the existing fan bridge.
#import "FWIdentity.h"
#import <Security/Security.h>
#import <libproc.h>
#import <sys/sysctl.h>
#import <mach/mach_time.h>
#import <sys/stat.h>
#import <mach-o/loader.h>
#import <mach-o/fat.h>
#import <fcntl.h>
#import <unistd.h>
#import <errno.h>

// Validate executable pages/signature structure, never every resource in a bundle.
static const SecCSFlags FWStaticFlags=kSecCSStrictValidate|kSecCSDoNotValidateResources;
static NSString *FWFileStamp(NSString *path) {
    struct stat st;
    if (!path || stat(path.fileSystemRepresentation,&st)!=0) return @"missing";
    return [NSString stringWithFormat:@"%llu:%llu:%llu:%lld:%ld:%lld:%ld",
        (unsigned long long)st.st_dev,(unsigned long long)st.st_ino,(unsigned long long)st.st_size,
        (long long)st.st_mtimespec.tv_sec,st.st_mtimespec.tv_nsec,
        (long long)st.st_ctimespec.tv_sec,st.st_ctimespec.tv_nsec];
}
static NSString *FWIdentityStamp(NSString *path,NSString *bundlePath) {
    return [NSString stringWithFormat:@"%@|%@|%@",FWFileStamp(path),
        FWFileStamp([bundlePath stringByAppendingPathComponent:@"Contents/Info.plist"]),
        FWFileStamp([bundlePath stringByAppendingPathComponent:@"Contents/_CodeSignature/CodeResources"])];
}
static NSCache *FWStaticCache(void) {
    static NSCache *cache;static dispatch_once_t once;
    dispatch_once(&once,^{cache=[NSCache new];cache.countLimit=4096;});return cache;
}

static NSDictionary *FWCodeInfo(SecStaticCodeRef code) {
    CFDictionaryRef raw=NULL;
    if (SecCodeCopySigningInformation(code,kSecCSSigningInformation,&raw)!=errSecSuccess) return nil;
    return CFBridgingRelease(raw);
}
NSString *FWOwnTeam(void) {
    SecCodeRef code=NULL;CFDictionaryRef raw=NULL;
    if (SecCodeCopySelf(kSecCSDefaultFlags,&code)!=errSecSuccess) return nil;
    OSStatus status=SecCodeCopySigningInformation(code,kSecCSSigningInformation,&raw);CFRelease(code);
    if (status!=errSecSuccess || !raw) return nil;
    NSDictionary *info=CFBridgingRelease(raw);return info[(__bridge NSString *)kSecCodeInfoTeamIdentifier];
}
static BOOL FWRequirement(SecStaticCodeRef code,NSString *text) {
    SecRequirementRef requirement=NULL;
    if (SecRequirementCreateWithString((__bridge CFStringRef)text,kSecCSDefaultFlags,&requirement)!=errSecSuccess) return NO;
    OSStatus result=SecStaticCodeCheckValidity(code,FWStaticFlags,requirement);CFRelease(requirement);return result==errSecSuccess;
}
static NSString *FWAuthorizationPermissionDetail(audit_token_t token) {
    pid_t pid=audit_token_to_pid(token);char buffer[PROC_PIDPATHINFO_MAXSIZE]={0};
    if(pid<=0 || proc_pidpath(pid,buffer,sizeof(buffer))<=0) return nil;
    NSString *path=[NSString stringWithUTF8String:buffer];
    if(!path.length) return nil;
    NSMutableArray<NSString *> *files=[NSMutableArray arrayWithObject:path];
    // Inspect the actual running image, not the new copy in the host App. A
    // protected parent also makes open fail with EACCES. Don't infer permissions
    // from a generic signature error or a missing/replaced process image.
    for(NSString *parent=path.stringByDeletingLastPathComponent;parent.length>1;parent=parent.stringByDeletingLastPathComponent) {
        if([parent.pathExtension isEqual:@"systemextension"]) {
            [files addObject:[parent stringByAppendingPathComponent:@"Contents/Info.plist"]];
            [files addObject:[parent stringByAppendingPathComponent:@"Contents/_CodeSignature/CodeResources"]];
            break;
        }
    }
    for(NSString *file in files) {
        int fd=open(file.fileSystemRepresentation,O_RDONLY|O_CLOEXEC);
        if(fd>=0) close(fd);
        else if(errno==EACCES || errno==EPERM) return @"扩展文件权限异常，请重新安装";
    }
    return nil;
}
BOOL FWAuthorizeWithDetail(audit_token_t token, NSString *identifier, NSString **detail) {
    if(detail) *detail=nil;
    NSString *team=FWOwnTeam();if (!team.length || !identifier.length) return NO;
    NSCharacterSet *invalid=[[NSCharacterSet characterSetWithCharactersInString:@"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_"] invertedSet];
    if ([identifier rangeOfCharacterFromSet:invalid].location!=NSNotFound || [team rangeOfCharacterFromSet:invalid].location!=NSNotFound) return NO;
    NSData *data=[NSData dataWithBytes:&token length:sizeof(token)];
    NSDictionary *attributes=@{(__bridge NSString *)kSecGuestAttributeAudit:data};
    SecCodeRef code=NULL;SecRequirementRef requirement=NULL;
    if (SecCodeCopyGuestWithAttributes(NULL,(__bridge CFDictionaryRef)attributes,kSecCSDefaultFlags,&code)!=errSecSuccess) {
        if(detail) *detail=FWAuthorizationPermissionDetail(token);
        return NO;
    }
    NSString *text=[NSString stringWithFormat:@"anchor apple generic and identifier \"%@\" and certificate leaf[subject.OU] = \"%@\"",identifier,team];
    OSStatus status=SecRequirementCreateWithString((__bridge CFStringRef)text,kSecCSDefaultFlags,&requirement);
    if (status==errSecSuccess) status=SecCodeCheckValidity(code,kSecCSStrictValidate,requirement);
    if (requirement) CFRelease(requirement);CFRelease(code);
    if(status!=errSecSuccess && detail) *detail=FWAuthorizationPermissionDetail(token);
    return status==errSecSuccess;
}
BOOL FWAuthorize(audit_token_t token, NSString *identifier) {
    return FWAuthorizeWithDetail(token,identifier,NULL);
}
static NSString *FWSubjectOrganization(id property) {
    if ([property isKindOfClass:NSArray.class]) {
        for (id child in property) {NSString *name=FWSubjectOrganization(child);if(name.length) return name;}
    } else if ([property isKindOfClass:NSDictionary.class]) {
        id value=property[(__bridge NSString *)kSecPropertyKeyValue];
        // Match the stable OID, not the localized label or the OU (team ID).
        if ([property[(__bridge NSString *)kSecPropertyKeyLabel] isEqual:(__bridge NSString *)kSecOIDOrganizationName]
            && [value isKindOfClass:NSString.class]) {
            NSString *name=[value stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
            if(name.length) return name;
        }
        if ([value isKindOfClass:NSArray.class] || [value isKindOfClass:NSDictionary.class]) return FWSubjectOrganization(value);
    }
    return nil;
}
static NSString *FWOrganization(NSDictionary *info) {
    NSArray *certs=info[(__bridge NSString *)kSecCodeInfoCertificates];if (!certs.count) return nil;
    // Organization is nested under Subject Name, not a top-level certificate OID.
    CFDictionaryRef raw=SecCertificateCopyValues((__bridge SecCertificateRef)certs[0],(__bridge CFArrayRef)@[(__bridge NSString *)kSecOIDX509V1SubjectName],NULL);
    if (!raw) return nil;
    NSDictionary *values=CFBridgingRelease(raw);
    return FWSubjectOrganization(values[(__bridge NSString *)kSecOIDX509V1SubjectName]);
}
static BOOL FWVersionName(NSString *name) {
    return [name rangeOfString:@"^[vV]?[0-9]+(?:\\.[0-9]+)*(?:[-+][0-9A-Za-z.-]+)?$" options:NSRegularExpressionSearch].location!=NSNotFound;
}
static NSString *FWDisplayName(NSString *path,NSBundle *bundle,NSString *signingID) {
    NSString *name=[bundle objectForInfoDictionaryKey:@"CFBundleDisplayName"]?:[bundle objectForInfoDictionaryKey:@"CFBundleName"];
    if(name.length) return name;
    NSString *file=path.lastPathComponent;
    BOOL insideApp=NO;
    for(NSString *component in path.pathComponents) if([component.pathExtension isEqual:@"app"]) {insideApp=YES;break;}
    if(!insideApp || FWVersionName(file)) {
        NSString *signedName=[signingID componentsSeparatedByString:@"."].lastObject;
        if(signedName.length && !FWVersionName(signedName)) return signedName;
        if(FWVersionName(file)) {
            NSString *parent=path.stringByDeletingLastPathComponent.lastPathComponent;
            if([parent isEqual:@"versions"]) parent=path.stringByDeletingLastPathComponent.stringByDeletingLastPathComponent.lastPathComponent;
            if(parent.length && !FWVersionName(parent) && ![@[@"/",@"bin",@"sbin",@"MacOS"] containsObject:parent]) return parent;
        }
    }
    return file;
}
static NSMutableDictionary *FWBasicIdentity(NSString *path) {
    NSFileManager *fm=NSFileManager.defaultManager;
    path=path.stringByResolvingSymlinksInPath;
    NSString *bundlePath=nil;
    BOOL bundleDirectory=NO;
    if ([@[@"app",@"systemextension",@"bundle",@"xpc"] containsObject:path.pathExtension]
        && [fm fileExistsAtPath:path isDirectory:&bundleDirectory] && bundleDirectory) {
        bundlePath=path;path=[NSBundle bundleWithPath:path].executablePath.stringByResolvingSymlinksInPath;
    }
    // Identity inspection does not execute the file. Root-owned helpers may be
    // executable only by their owner while their code signature is readable.
    struct stat executableStat;
    if (!path.length || stat(path.fileSystemRepresentation,&executableStat)!=0
        || !S_ISREG(executableStat.st_mode) || !(executableStat.st_mode&(S_IXUSR|S_IXGRP|S_IXOTH))) return nil;
    NSString *cacheKey=bundlePath?:path;
    NSString *stamp=FWIdentityStamp(path,bundlePath);
    NSDictionary *cached=[FWStaticCache() objectForKey:cacheKey];
    if ([cached[@"stamp"] isEqual:stamp]) return [cached[@"app"] mutableCopy];
    SecStaticCodeRef code=NULL;
    OSStatus created=SecStaticCodeCreateWithPath((__bridge CFURLRef)[NSURL fileURLWithPath:path],kSecCSDefaultFlags,&code);
    NSDictionary *info=created==errSecSuccess?FWCodeInfo(code):nil;
    OSStatus validity=code?SecStaticCodeCheckValidity(code,FWStaticFlags,NULL):created;
    NSString *signer=@"invalid";
    if (validity==errSecCSUnsigned) signer=@"unsigned";
    else if (validity==errSecSuccess) {
        if ([info[(__bridge NSString *)kSecCodeInfoFlags] unsignedIntValue]&kSecCodeSignatureAdhoc) signer=@"adhoc";
        else if (FWRequirement(code,@"anchor apple")) signer=@"apple";
        else if (FWRequirement(code,@"anchor apple generic and certificate leaf[field.1.2.840.113635.100.6.1.9] exists")) signer=@"app_store";
        else if (FWRequirement(code,@"anchor apple generic and certificate leaf[field.1.2.840.113635.100.6.1.13] exists")) signer=@"developer_id";
    }
    if (code) CFRelease(code);
    NSBundle *bundle=bundlePath?[NSBundle bundleWithPath:bundlePath]:nil;
    NSString *name=FWDisplayName(path,bundle,info[(__bridge NSString *)kSecCodeInfoIdentifier]);
    NSMutableDictionary *app=[@{@"key":@"",@"name":name,@"path":path,@"bundlePath":bundlePath?:NSNull.null,
        @"bundleId":bundle.bundleIdentifier?:NSNull.null,@"signingId":info[(__bridge NSString *)kSecCodeInfoIdentifier]?:NSNull.null,
        @"teamId":info[(__bridge NSString *)kSecCodeInfoTeamIdentifier]?:NSNull.null,
        @"teamName":[signer isEqual:@"apple"]?NSNull.null:(FWOrganization(info)?:NSNull.null),@"signer":signer,@"iconPath":NSNull.null} mutableCopy];
    // Do not hold a cache lock across Security calls: the baseline worker must
    // never serialize network-flow identity checks behind its disk work.
    if ([stamp isEqual:FWIdentityStamp(path,bundlePath)])
        [FWStaticCache() setObject:@{@"stamp":stamp,@"app":[app copy]} forKey:cacheKey];
    else return nil; // File changed during verification; do not attribute stale trust.
    return app;
}
static BOOL FWSigned(NSDictionary *app) {return [@[@"apple",@"app_store",@"developer_id"] containsObject:app[@"signer"]];}
static NSString *FWTeam(NSDictionary *app) {return [app[@"signer"] isEqual:@"apple"]?@"apple":([app[@"teamId"] isKindOfClass:NSString.class]?app[@"teamId"]:nil);}
static BOOL FWSameTeam(NSDictionary *app,NSDictionary *parent) {
    return app && parent && FWSigned(app) && FWSigned(parent) && FWTeam(app).length && [FWTeam(app) isEqual:FWTeam(parent)];
}
static BOOL FWInside(NSString *path,NSString *root) {
    return root.length && [path hasPrefix:[root stringByAppendingString:@"/"]];
}
static BOOL FWIsMachO(NSString *path) {
    int fd=open(path.fileSystemRepresentation,O_RDONLY|O_CLOEXEC|O_NOFOLLOW);if(fd<0) return NO;
    uint32_t magic=0;ssize_t size=read(fd,&magic,sizeof(magic));close(fd);
    return size==sizeof(magic) && (magic==MH_MAGIC || magic==MH_MAGIC_64 || magic==MH_CIGAM || magic==MH_CIGAM_64
        || magic==FAT_MAGIC || magic==FAT_MAGIC_64 || magic==FAT_CIGAM || magic==FAT_CIGAM_64);
}
static BOOL FWUnsignedOwner(NSDictionary *app,NSDictionary *parent) {
    return app && parent && [@[@"adhoc",@"unsigned"] containsObject:app[@"signer"]]
        && [app[@"signer"] isEqual:parent[@"signer"]] && !FWTeam(app).length && !FWTeam(parent).length;
}
// Cache only candidate paths; every ownership decision still validates executable
// signatures using the file-stamped cache. Never enumerate an App's resource tree.
static NSArray<NSString *> *FWInstalledAppPaths(void) {
    static NSLock *lock;static NSArray *paths;static uint64_t at;static dispatch_once_t once;
    dispatch_once(&once,^{lock=[NSLock new];});
    [lock lock];NSArray *cached=paths;uint64_t age=FWMonotonic()-at;[lock unlock];
    if(cached && age<30000) return cached;
    NSMutableArray *found=[NSMutableArray array];
    for(NSString *root in @[@"/Applications",@"/System/Applications"]) {
        NSDirectoryEnumerator *walk=[NSFileManager.defaultManager enumeratorAtURL:[NSURL fileURLWithPath:root]
            includingPropertiesForKeys:nil options:NSDirectoryEnumerationSkipsHiddenFiles errorHandler:nil];
        for(NSURL *url in walk) if([url.pathExtension isEqual:@"app"]) {
            [walk skipDescendants];[found addObject:url.path.stringByResolvingSymlinksInPath];
        }
    }
    [lock lock];paths=[found copy];at=FWMonotonic();cached=paths;[lock unlock];return cached;
}
static NSDictionary *FWExternalOwner(NSDictionary *app,NSArray<NSString *> *installedApps,NSString *systemRoot,NSString *helperRoot) {
    if(!FWSigned(app) || !FWTeam(app).length) return nil;
    NSString *path=app[@"path"],*identifier=app[@"signingId"];
    if(![identifier isKindOfClass:NSString.class] || !identifier.length) return nil;
    BOOL system=FWInside(path,systemRoot),helper=FWInside(path,helperRoot);
    if(!system && !helper) return nil;
    NSString *extensionID=nil,*helperID=nil;
    if(system) {
        NSString *cursor=path.stringByDeletingLastPathComponent;
        while(FWInside(cursor,systemRoot)) {
            if([cursor.pathExtension isEqual:@"systemextension"]) {extensionID=[NSBundle bundleWithPath:cursor].bundleIdentifier;break;}
            cursor=cursor.stringByDeletingLastPathComponent;
        }
        if(!extensionID.length || ![identifier isEqual:extensionID]) return nil;
    } else {
        // A privileged bundle's executable can have a short signing identifier
        // (e.g. service-daemon) distinct from its signed CFBundleIdentifier.
        NSString *cursor=path.stringByDeletingLastPathComponent;
        while(FWInside(cursor,helperRoot)) {
            if([cursor.pathExtension isEqual:@"bundle"]) {
                NSDictionary *container=FWBasicIdentity(cursor);
                if(FWSameTeam(app,container) && [container[@"signingId"] isEqual:identifier]
                    && [container[@"bundleId"] isKindOfClass:NSString.class]) helperID=container[@"bundleId"];
                break;
            }
            cursor=cursor.stringByDeletingLastPathComponent;
        }
    }
    for(NSString *bundlePath in installedApps) {
        NSBundle *bundle=[NSBundle bundleWithPath:bundlePath];
        if(system) {
            NSString *folder=[bundlePath stringByAppendingPathComponent:@"Contents/Library/SystemExtensions"];
            for(NSString *name in [NSFileManager.defaultManager contentsOfDirectoryAtPath:folder error:nil]) {
                if(![name.pathExtension isEqual:@"systemextension"]) continue;
                NSString *embedded=[folder stringByAppendingPathComponent:name].stringByResolvingSymlinksInPath;
                if(!FWInside(embedded,bundlePath) || ![[NSBundle bundleWithPath:embedded].bundleIdentifier isEqual:extensionID]) continue;
                NSDictionary *copy=FWBasicIdentity(embedded);
                if(!FWSameTeam(app,copy) || ![copy[@"signingId"] isEqual:identifier]) continue;
                NSDictionary *parent=FWBasicIdentity(bundlePath);
                if(FWSameTeam(app,parent)) return parent;
            }
        } else {
            // A name/prefix alone is not proof: also require the installed App's
            // valid signature and identical team. Declared helpers take this path too.
            NSDictionary *declared=[bundle objectForInfoDictionaryKey:@"SMPrivilegedExecutables"];
            BOOL named=[declared isKindOfClass:NSDictionary.class] && (declared[identifier]!=nil || (helperID && declared[helperID]!=nil));
            NSString *bundleID=bundle.bundleIdentifier;
            NSString *prefix=[bundleID stringByAppendingString:@"."];
            if(!named && !(bundleID.length && ([identifier hasPrefix:prefix] || [helperID hasPrefix:prefix]))) continue;
            NSDictionary *parent=FWBasicIdentity(bundlePath);
            if(FWSameTeam(app,parent)) return parent;
        }
    }
    return nil;
}
NSDictionary *FWIdentifyPath(NSString *input) {
    NSMutableDictionary *app=FWBasicIdentity(input);if (!app) return nil;
    NSString *executable=app[@"path"];
    if (FWSigned(app) || [@[@"adhoc",@"unsigned"] containsObject:app[@"signer"]]) {
        NSString *cursor=[executable stringByDeletingLastPathComponent];
        while (cursor.length>1) {
            if ([cursor.pathExtension isEqualToString:@"app"]) {
                NSMutableDictionary *parent=FWBasicIdentity(cursor);
                if (FWSameTeam(app,parent)) {
                    // Keep the observed executable, but attribute identity/name to the outermost same-team App.
                    app=parent;
                } else if(FWUnsignedOwner(app,parent) && FWInside(executable,cursor) && FWIsMachO(executable)) {
                    // Internal evidence for baseline matching only. The helper's
                    // signer, signing ID and executable-path rule key stay its own.
                    app[@"baselineOwner"]=parent;app[@"bundlePath"]=parent[@"bundlePath"];
                    app[@"bundleId"]=parent[@"bundleId"];app[@"name"]=parent[@"name"];
                }
            }
            cursor=cursor.stringByDeletingLastPathComponent;
        }
    }
    if(FWSigned(app) && (FWInside(executable,@"/Library/SystemExtensions") || FWInside(executable,@"/Library/PrivilegedHelperTools"))) {
        NSDictionary *parent=FWExternalOwner(app,FWInstalledAppPaths(),@"/Library/SystemExtensions",@"/Library/PrivilegedHelperTools");
        if(parent) app=[parent mutableCopy];
    }
    app[@"path"]=executable;
    if (FWSigned(app) && [app[@"signingId"] isKindOfClass:NSString.class] && FWTeam(app).length) {
        NSData *key=[NSJSONSerialization dataWithJSONObject:@[FWTeam(app),app[@"signingId"]] options:NSJSONWritingWithoutEscapingSlashes error:nil];
        app[@"key"]=[@"signed:" stringByAppendingString:[[NSString alloc] initWithData:key encoding:NSUTF8StringEncoding]];
    } else {app[@"key"]=[@"path:" stringByAppendingString:executable];}
    return app;
}
NSDictionary *FWIdentifyToken(NSData *token) {
    if (token.length!=sizeof(audit_token_t)) return nil;
    audit_token_t audit;[token getBytes:&audit length:sizeof(audit)];pid_t pid=audit_token_to_pid(audit);
    char path[PROC_PIDPATHINFO_MAXSIZE]={0};if (pid<=0 || proc_pidpath(pid,path,sizeof(path))<=0) return nil;
    NSString *executable=@(path);
    // A readable process path is enough to ask. Never grant signing identity,
    // helper attribution or automatic trust when live verification fails.
    NSDictionary *(^invalid)(void)=^NSDictionary *{
        return @{@"key":[@"path:" stringByAppendingString:executable],@"name":FWDisplayName(executable,nil,nil),
            @"path":executable,@"bundlePath":NSNull.null,@"bundleId":NSNull.null,
            @"signingId":NSNull.null,@"teamId":NSNull.null,@"teamName":NSNull.null,
            @"signer":@"invalid",@"iconPath":NSNull.null};
    };
    SecCodeRef running=NULL;
    NSDictionary *attributes=@{(__bridge NSString *)kSecGuestAttributeAudit:token};
    OSStatus status=SecCodeCopyGuestWithAttributes(NULL,(__bridge CFDictionaryRef)attributes,kSecCSDefaultFlags,&running);
    if (status!=errSecSuccess) return invalid();
    CFDictionaryRef raw=NULL;SecCodeCopySigningInformation(running,kSecCSSigningInformation,&raw);
    NSDictionary *live=raw?CFBridgingRelease(raw):nil;
    status=SecCodeCheckValidity(running,kSecCSStrictValidate,NULL);
    if (status!=errSecSuccess && status!=errSecCSUnsigned) {CFRelease(running);return invalid();}
    // Use the running code's static representation to select the same Mach-O
    // slice (including arm64e variants). A path-only lookup may pick another
    // valid slice and wrongly treat Apple system processes as modified code.
    SecStaticCodeRef disk=NULL;NSDictionary *diskInfo=nil;
    OSStatus diskStatus=SecCodeCopyStaticCode(running,kSecCSDefaultFlags,&disk);CFRelease(running);
    if (diskStatus!=errSecSuccess || !disk) return invalid();
    diskInfo=FWCodeInfo(disk);CFRelease(disk);
    NSData *liveHash=live[(__bridge NSString *)kSecCodeInfoUnique];
    NSData *diskHash=diskInfo[(__bridge NSString *)kSecCodeInfoUnique];
    if ((liveHash || diskHash) && ![liveHash isEqual:diskHash]) return invalid();
    if (status==errSecSuccess && !liveHash) return invalid();
    NSDictionary *app=FWIdentifyPath(executable);
    if (!app || [app[@"signer"] isEqual:@"invalid"]) return invalid();
    return app;
}
uint64_t FWProcessStart(pid_t pid) {
    struct proc_bsdinfo info={0};if (proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=sizeof(info)) return 0;
    return (uint64_t)info.pbi_start_tvsec*1000000+info.pbi_start_tvusec;
}
NSArray *FWArguments(pid_t pid) {
    int mib[]={CTL_KERN,KERN_PROCARGS2,pid};size_t size=0;
    if (sysctl(mib,3,NULL,&size,NULL,0)!=0 || size<sizeof(int) || size>1024*1024) return @[];
    NSMutableData *data=[NSMutableData dataWithLength:size];if (sysctl(mib,3,data.mutableBytes,&size,NULL,0)!=0) return @[];
    const char *cursor=data.bytes,*end=cursor+size;int argc=0;memcpy(&argc,cursor,sizeof(argc));cursor+=sizeof(argc);
    size_t n=strnlen(cursor,(size_t)(end-cursor));if (cursor+n>=end) return @[];cursor+=n+1;
    while (cursor<end && !*cursor) cursor++;
    NSMutableArray *args=[NSMutableArray array];
    for (int i=0;i<argc && i<20 && cursor<end;i++) {
        n=strnlen(cursor,(size_t)(end-cursor));if (cursor+n>=end) break;
        NSString *arg=[[NSString alloc] initWithBytes:cursor length:n encoding:NSUTF8StringEncoding];
        if (arg) [args addObject:arg.length>200?[arg substringToIndex:200]:arg];cursor+=n+1;
    }
    return args;
}
NSArray *FWAncestors(pid_t pid) {
    NSMutableArray *parents=[NSMutableArray array];NSMutableSet *seen=[NSMutableSet set];
    for (int i=0;i<32 && pid>1;i++) {
        struct proc_bsdinfo info={0};if (proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=sizeof(info)) break;
        pid=(pid_t)info.pbi_ppid;if (pid<=1 || [seen containsObject:@(pid)]) break;[seen addObject:@(pid)];
        char path[PROC_PIDPATHINFO_MAXSIZE]={0};NSString *p=proc_pidpath(pid,path,sizeof(path))>0?@(path):nil;
        [parents addObject:@{@"pid":@(pid),@"name":p.lastPathComponent?:@"?",@"path":p?:NSNull.null}];
    }
    return parents;
}
NSDictionary *FWProcessDetails(NSString *verdict,pid_t pid,uint64_t start) {
    // Rules, automatic decisions and bypasses must not walk argv or parents.
    if (![verdict isEqual:@"pause"] || !start || FWProcessStart(pid)!=start) return nil;
    NSArray *args=FWArguments(pid),*ancestors=FWAncestors(pid);
    if (FWProcessStart(pid)!=start) return nil;
    return @{@"args":args,@"ancestors":ancestors};
}
void FWScanInstalledApps(NSArray<NSString *> *roots,uint64_t deadline,void (^found)(NSDictionary *)) {
    for (NSString *root in roots) {
        NSDirectoryEnumerator *walk=[NSFileManager.defaultManager enumeratorAtURL:[NSURL fileURLWithPath:root] includingPropertiesForKeys:@[NSURLIsDirectoryKey] options:NSDirectoryEnumerationSkipsHiddenFiles errorHandler:^BOOL(NSURL *url,NSError *error){(void)url;(void)error;return YES;}];
        for (NSURL *url in walk) {
            if (FWMonotonic()>=deadline) return;
            if ([url.pathExtension isEqual:@"app"]) {
                [walk skipDescendants];
                @autoreleasepool {
                    NSDictionary *app=FWIdentifyPath(url.path);
                    if (FWMonotonic()>=deadline) return;
                    if (app) found(app);
                }
            }
        }
    }
}
NSArray *FWInstalledApps(void) {
    NSMutableArray *apps=[NSMutableArray array];
    FWScanInstalledApps(@[@"/Applications",@"/System/Applications"],FWMonotonic()+60000,^(NSDictionary *app){[apps addObject:app];});
    return apps;
}
uint64_t FWMonotonic(void) {static mach_timebase_info_data_t timebase;static dispatch_once_t once;dispatch_once(&once,^{mach_timebase_info(&timebase);});return (uint64_t)(((__uint128_t)mach_continuous_time()*timebase.numer/timebase.denom)/1000000);}
int64_t FWNow(void) {return (int64_t)(NSDate.date.timeIntervalSince1970*1000);}
int64_t FWDayStart(void) {return (int64_t)([NSCalendar.currentCalendar startOfDayForDate:NSDate.date].timeIntervalSince1970*1000);}
