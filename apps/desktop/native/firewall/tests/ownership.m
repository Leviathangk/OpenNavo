// SPDX-License-Identifier: GPL-3.0-only
// Real filesystem/bundle traversal with deterministic signing identities. All
// executable fixtures are private copies; no installed App or extension is touched.
#import "../FWIdentity.h"
#import <Security/Security.h>
#import <assert.h>
static NSMutableDictionary *identities;
static OSStatus TestCreate(CFURLRef url,SecCSFlags flags,SecStaticCodeRef *code) {
    (void)flags;NSString *path=[(__bridge NSURL *)url path];
    if(!identities[path]) return errSecCSUnsigned;
    *code=(SecStaticCodeRef)CFBridgingRetain(path);return errSecSuccess;
}
static OSStatus TestInfo(SecStaticCodeRef code,SecCSFlags flags,CFDictionaryRef *raw) {
    (void)flags;NSDictionary *identity=identities[(__bridge NSString *)code];
    NSMutableDictionary *info=[NSMutableDictionary dictionary];
    if(identity[@"id"]) info[(__bridge NSString *)kSecCodeInfoIdentifier]=identity[@"id"];
    if(identity[@"team"]) info[(__bridge NSString *)kSecCodeInfoTeamIdentifier]=identity[@"team"];
    info[(__bridge NSString *)kSecCodeInfoFlags]=[identity[@"signer"] isEqual:@"adhoc"]?@(kSecCodeSignatureAdhoc):@0;
    *raw=CFBridgingRetain(info);return errSecSuccess;
}
static OSStatus TestValidity(SecStaticCodeRef code,SecCSFlags flags,SecRequirementRef requirement) {
    assert((flags&kSecCSDoNotValidateResources) && (flags&kSecCSStrictValidate));
    NSString *signer=identities[(__bridge NSString *)code][@"signer"];
    if([signer isEqual:@"invalid"]) return errSecCSReqFailed;
    if([signer isEqual:@"unsigned"]) return errSecCSUnsigned;
    if(!requirement) return errSecSuccess;
    CFStringRef raw=NULL;assert(SecRequirementCopyString(requirement,0,&raw)==errSecSuccess);
    NSString *text=CFBridgingRelease(raw);
    return [signer isEqual:@"developer_id"] && [text containsString:@"1.2.840.113635.100.6.1.13"]?errSecSuccess:errSecCSReqFailed;
}
#define SecStaticCodeCreateWithPath TestCreate
#define SecCodeCopySigningInformation TestInfo
#define SecStaticCodeCheckValidity TestValidity
#include "../FWIdentity.m"

static NSString *Executable(NSString *path,NSString *signer,NSString *team,NSString *identifier) {
    NSFileManager *fm=NSFileManager.defaultManager;
    assert([fm createDirectoryAtPath:path.stringByDeletingLastPathComponent withIntermediateDirectories:YES attributes:nil error:nil]);
    assert([fm copyItemAtPath:@"/bin/echo" toPath:path error:nil]);
    NSMutableDictionary *identity=[@{@"signer":signer} mutableCopy];
    if(team) identity[@"team"]=team;if(identifier) identity[@"id"]=identifier;
    identities[path.stringByResolvingSymlinksInPath]=identity;return path;
}
static NSString *Bundle(NSString *path,NSString *signer,NSString *team,NSString *identifier) {
    Executable([path stringByAppendingPathComponent:@"Contents/MacOS/main"],signer,team,identifier);
    assert(([@{@"CFBundleExecutable":@"main",@"CFBundleIdentifier":identifier,@"CFBundleName":@"Example Network App"}
        writeToFile:[path stringByAppendingPathComponent:@"Contents/Info.plist"] atomically:YES]));
    return path;
}
int main(void) { @autoreleasepool {
    identities=[NSMutableDictionary dictionary];
    NSString *root=[NSTemporaryDirectory() stringByAppendingPathComponent:NSUUID.UUID.UUIDString].stringByResolvingSymlinksInPath;
    NSFileManager *fm=NSFileManager.defaultManager;
    for(NSString *signer in @[@"adhoc",@"unsigned"]) {
        NSString *bundle=Bundle([root stringByAppendingPathComponent:[signer stringByAppendingString:@".app"]],signer,nil,@"org.example.proxy");
        NSString *path=Executable([bundle stringByAppendingPathComponent:@"Contents/MacOS/ProxyCore"],signer,nil,@"org.example.proxy.core");
        NSDictionary *helper=FWIdentifyPath(path),*owner=helper[@"baselineOwner"];
        assert([helper[@"key"] isEqual:[@"path:" stringByAppendingString:path]]);
        assert([helper[@"signer"] isEqual:signer] && [helper[@"signingId"] isEqual:@"org.example.proxy.core"]);
        assert([owner[@"path"] isEqual:FWIdentifyPath(bundle)[@"path"]]);
        assert([helper[@"name"] isEqual:@"Example Network App"]);
        NSString *bad=Executable([bundle stringByAppendingPathComponent:@"Contents/MacOS/BadCore"],@"invalid",nil,@"org.example.proxy.bad");
        assert(FWIdentifyPath(bad)[@"baselineOwner"]==nil);
        NSString *signedChild=Executable([bundle stringByAppendingPathComponent:@"Contents/MacOS/SignedCore"],@"developer_id",@"OTHERTEAM",@"org.example.proxy.signed");
        assert(FWIdentifyPath(signedChild)[@"baselineOwner"]==nil);
        NSString *escape=[root stringByAppendingPathComponent:[signer stringByAppendingString:@"-outside"]];
        Executable(escape,signer,nil,@"org.example.outside");
        NSString *link=[bundle stringByAppendingPathComponent:@"Contents/MacOS/link"];
        assert([fm createSymbolicLinkAtPath:link withDestinationPath:escape error:nil]);
        assert(FWIdentifyPath(link)[@"baselineOwner"]==nil);
        NSString *script=[bundle stringByAppendingPathComponent:@"Contents/MacOS/script"];
        assert([@"#!/bin/sh\nexit 0\n" writeToFile:script atomically:YES encoding:NSUTF8StringEncoding error:nil]);
        assert([fm setAttributes:@{NSFilePosixPermissions:@0755} ofItemAtPath:script error:nil]);
        identities[script]=@{@"signer":signer};assert(FWIdentifyPath(script)[@"baselineOwner"]==nil);
    }
    NSString *app=Bundle([root stringByAppendingPathComponent:@"Network.app"],@"developer_id",@"EXAMPLETEAM",@"org.example.network");
    NSString *different=Executable([app stringByAppendingPathComponent:@"Contents/MacOS/ForeignCore"],@"developer_id",@"OTHERTEAM",@"org.example.foreign");
    NSDictionary *foreign=FWIdentifyPath(different);
    assert(foreign[@"bundlePath"]==NSNull.null && foreign[@"baselineOwner"]==nil);
    assert([foreign[@"signingId"] isEqual:@"org.example.foreign"]);
    NSString *systemRoot=[root stringByAppendingPathComponent:@"SystemExtensions"];
    NSString *helperRoot=[root stringByAppendingPathComponent:@"PrivilegedHelperTools"];
    NSString *installed=Bundle([systemRoot stringByAppendingPathComponent:@"UUID/Network.systemextension"],@"developer_id",@"EXAMPLETEAM",@"org.example.network.extension");
    NSString *embedded=Bundle([app stringByAppendingPathComponent:@"Contents/Library/SystemExtensions/Network.systemextension"],@"developer_id",@"EXAMPLETEAM",@"org.example.network.extension");
    NSDictionary *running=FWBasicIdentity(installed);
    NSDictionary *owner=FWExternalOwner(running,@[app],systemRoot,helperRoot);
    assert([owner[@"bundlePath"] isEqual:app] && [owner[@"name"] isEqual:@"Example Network App"]);
    assert([owner[@"signingId"] isEqual:@"org.example.network"]);
    NSMutableDictionary *tampered=[running mutableCopy];tampered[@"teamId"]=@"OTHERTEAM";
    assert(!FWExternalOwner(tampered,@[app],systemRoot,helperRoot));
    tampered=[running mutableCopy];tampered[@"signer"]=@"invalid";
    assert(!FWExternalOwner(tampered,@[app],systemRoot,helperRoot));
    tampered=[running mutableCopy];tampered[@"signingId"]=@"org.example.other";
    assert(!FWExternalOwner(tampered,@[app],systemRoot,helperRoot));
    // The embedded copy itself must be valid, not just have matching Info.plist metadata.
    identities[[embedded stringByAppendingPathComponent:@"Contents/MacOS/main"]][@"signer"]=@"invalid";
    [FWStaticCache() removeAllObjects];assert(!FWExternalOwner(running,@[app],systemRoot,helperRoot));
    identities[[embedded stringByAppendingPathComponent:@"Contents/MacOS/main"]][@"signer"]=@"developer_id";
    [FWStaticCache() removeAllObjects];
    // Some helpers are plain Mach-O files whose names end in .bundle.
    NSString *privileged=Executable([helperRoot stringByAppendingPathComponent:@"service.bundle"],@"developer_id",@"EXAMPLETEAM",@"org.example.network.service");
    assert([fm setAttributes:@{NSFilePosixPermissions:@0544} ofItemAtPath:privileged error:nil]);
    NSDictionary *service=FWBasicIdentity(privileged);
    assert([FWExternalOwner(service,@[app],systemRoot,helperRoot)[@"bundlePath"] isEqual:app]);
    tampered=[service mutableCopy];tampered[@"teamId"]=@"OTHERTEAM";
    assert(!FWExternalOwner(tampered,@[app],systemRoot,helperRoot));
    tampered=[service mutableCopy];tampered[@"signingId"]=@"org.example.networkevil.service";
    assert(!FWExternalOwner(tampered,@[app],systemRoot,helperRoot));
    NSString *helperBundle=Bundle([helperRoot stringByAppendingPathComponent:@"NetworkService.bundle"],@"developer_id",@"EXAMPLETEAM",@"org.example.network.service");
    NSString *helperExecutable=[helperBundle stringByAppendingPathComponent:@"Contents/MacOS/main"];
    identities[helperExecutable][@"id"]=@"service-daemon";
    service=FWBasicIdentity(helperExecutable);
    assert([FWExternalOwner(service,@[app],systemRoot,helperRoot)[@"bundlePath"] isEqual:app]);
    assert([fm removeItemAtPath:root error:nil]);
    puts("Native baseline/helper/system-extension ownership tests passed.");return 0;
} }
