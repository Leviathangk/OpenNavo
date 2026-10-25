// SPDX-License-Identifier: GPL-3.0-only
#import <AppKit/AppKit.h>
#import <NetworkExtension/NetworkExtension.h>
#import <SystemExtensions/SystemExtensions.h>
#import <objc/message.h>
#import <xpc/xpc.h>
#import "firewall_cleanup.h"
#import "../../native/firewall/FWIdentity.h"
extern void xpc_dictionary_get_audit_token(xpc_object_t,audit_token_t *);

static NSString *FWExtensionID(void) {return [NSBundle.mainBundle.bundleIdentifier stringByAppendingString:@".firewall"];}
static NSString *FWExtensionPath(void) {return [NSBundle.mainBundle.bundlePath stringByAppendingPathComponent:[NSString stringWithFormat:@"Contents/Library/SystemExtensions/%@.systemextension",FWExtensionID()]];}
static NSString *FWAvailability(void) {
    NSFileManager *files=NSFileManager.defaultManager;
    NSString *bundle=NSBundle.mainBundle.bundlePath;
    if (![bundle.pathExtension isEqual:@"app"] || !FWOwnTeam().length
        || ![files fileExistsAtPath:[bundle stringByAppendingPathComponent:@"Contents/embedded.provisionprofile"]]
        || ![files fileExistsAtPath:[FWExtensionPath() stringByAppendingPathComponent:@"Contents/embedded.provisionprofile"]]) return @"not_bundled";
    if (@available(macOS 13.0,*)) {} else return @"unsupported_os";
    return [bundle.stringByStandardizingPath hasPrefix:@"/Applications/"]?@"available":@"not_in_applications";
}
static NSDictionary *FWFailure(NSString *code,NSString *detail) {return @{@"code":code,@"message":@"firewall_native",@"detail":detail?:NSNull.null};}
static NSDictionary *FWResult(id value,NSDictionary *error) {return @{@"value":value?:NSNull.null,@"error":error?:NSNull.null};}
void on_fw_disconnect(void);
static uint64_t FWDisconnectGeneration(void);
static NSString *const FWServiceMissing=@"firewall_service_missing";
typedef NS_ENUM(NSUInteger,FWPeerState) {FWPeerOther,FWPeerMissing,FWPeerReady};
static FWPeerState FWClassifyPeer(BOOL authenticated,BOOL invalid,uint64_t elapsed) {
    if(authenticated) return FWPeerReady;
    return invalid && elapsed<=250?FWPeerMissing:FWPeerOther;
}

@interface FWHost:NSObject<OSSystemExtensionRequestDelegate>
@property NSString *extensionState;
@property NSString *installedVersion;
@property NSString *installedBuildVersion;
@property BOOL activationNeedsReboot;
@property BOOL filterOperation;
@property NSString *filterState;
@property NSDictionary *failure;
@property OSSystemExtensionRequest *operation;
@property BOOL enableAfterActivation;
@property BOOL uninstalling;
@property BOOL removalConfirmed;
@property BOOL upgrading;
@property BOOL upgradeAttempted;
@property BOOL canceledSameVersion;
@property BOOL refreshing;
@property uint64_t lastRefresh;
@property uint64_t generation;
@property OSSystemExtensionRequest *propertiesRequest;
@property uint64_t propertiesGeneration;
@property BOOL bundledVersionConfirmed;
@property uint64_t missingSince;
@property uint64_t lastMissing;
@property NSUInteger missingCount;
@end
@implementation FWHost
-(instancetype)init {self=[super init];if(self) {_extensionState=@"not_installed";_filterState=@"off";}return self;}
-(NSString *)extensionIdentifier {return FWExtensionID();}
-(NEFilterManager *)filterManager {return NEFilterManager.sharedManager;}
-(void)loadFilter:(void (^)(NSError *))completion {
    [[self filterManager] loadFromPreferencesWithCompletionHandler:^(NSError *error){dispatch_async(dispatch_get_main_queue(),^{completion(error);});}];
}
-(void)saveFilter:(void (^)(NSError *))completion {
    [[self filterManager] saveToPreferencesWithCompletionHandler:^(NSError *error){dispatch_async(dispatch_get_main_queue(),^{completion(error);});}];
}
-(void)removeFilter:(void (^)(NSError *))completion {
    [[self filterManager] removeFromPreferencesWithCompletionHandler:^(NSError *error){dispatch_async(dispatch_get_main_queue(),^{completion(error);});}];
}
-(void)submitActivation {
    self.canceledSameVersion=NO;
    self.operation=[OSSystemExtensionRequest activationRequestForExtension:[self extensionIdentifier] queue:dispatch_get_main_queue()];
    self.operation.delegate=self;[OSSystemExtensionManager.sharedManager submitRequest:self.operation];
}
-(void)resetMissingWindow {self.missingSince=0;self.lastMissing=0;self.missingCount=0;}
-(void)observePeer:(FWPeerState)state at:(uint64_t)now generation:(uint64_t)generation disconnects:(uint64_t)disconnects {
    if(generation!=self.generation || disconnects!=FWDisconnectGeneration()) return;
    if(self.operation || self.filterOperation) {[self resetMissingWindow];return;}
    if(state==FWPeerReady) {
        [self resetMissingWindow];
        if([self.failure[@"detail"] isEqual:FWServiceMissing]) self.failure=nil;
        return;
    }
    BOOL eligible=self.bundledVersionConfirmed && [self hasBundledVersion]
        && [self.extensionState isEqual:@"installed"] && [self.filterState isEqual:@"on"]
        && !self.operation && !self.filterOperation && !self.uninstalling;
    if(!eligible || state!=FWPeerMissing) {[self resetMissingWindow];return;}
    if(!self.missingCount || now<self.lastMissing || now-self.lastMissing>3000) {
        self.missingSince=now;self.missingCount=0;
    }
    self.lastMissing=now;self.missingCount++;
    if(self.missingCount<3 || now-self.missingSince<15000) return;
    // A same-version activation is a no-op, and rebuilding NE preferences does
    // not re-register the launchd task. Report the problem without system writes.
    self.failure=FWFailure(@"E_FW_DISCONNECTED",FWServiceMissing);
    [self resetMissingWindow];
}
-(NSDictionary *)bundledExtensionInfo {return [NSBundle bundleWithPath:FWExtensionPath()].infoDictionary;}
-(BOOL)hasBundledVersion {
    NSDictionary *info=[self bundledExtensionInfo];
    return [self.installedVersion isEqual:info[@"CFBundleShortVersionString"]]
        && [self.installedBuildVersion isEqual:info[@"CFBundleVersion"]];
}
-(NSDictionary *)status {
    NSString *availability=FWAvailability();
    if (![availability isEqual:@"available"]) return @{@"availability":availability,@"extension":@"not_installed",@"extensionVersion":NSNull.null,@"filter":@"off",@"connected":@NO,@"preparing":@NO,@"mode":@"ask",@"ruleCount":@0,@"pendingAlerts":@0,@"blockedToday":@0,@"error":NSNull.null};
    [self refresh];
    return @{@"availability":availability,@"extension":self.extensionState,@"extensionVersion":[self.extensionState isEqual:@"installed"]?(self.installedVersion?:NSNull.null):NSNull.null,@"filter":self.filterState,@"connected":@NO,@"preparing":@NO,@"mode":@"ask",@"ruleCount":@0,@"pendingAlerts":@0,@"blockedToday":@0,@"error":self.failure?:NSNull.null};
}
-(void)fail:(NSError *)error activation:(BOOL)activation {
    BOOL removal=self.uninstalling,upgrade=self.upgrading;
    BOOL consent=[error.domain isEqual:NEFilterErrorDomain] && error.code==NEFilterManagerErrorConfigurationPermissionDenied;
    // Extension requests fail as an installation or a removal; the other failures are the NE filter configuration's.
    NSString *code=consent?@"E_FW_CONSENT_DENIED":!activation?@"E_FW_FILTER":removal?@"E_FW_REMOVAL":@"E_FW_ACTIVATION";
    self.failure=@{@"code":code,@"detail":error.localizedDescription?:NSNull.null};
    [self resetMissingWindow];
    if (activation && !removal && !upgrade) self.extensionState=@"failed";
    self.enableAfterActivation=NO;self.activationNeedsReboot=NO;self.operation=nil;self.uninstalling=NO;self.upgrading=NO;self.filterOperation=NO;
    self.filterState=consent?@"off":([self filterManager].enabled?@"on":@"off");
    // A failed removal (say, a dismissed password prompt) or update leaves the extension where it was; ask now, not at
    // the next poll.
    if (removal || upgrade) [self refreshProperties];
}
-(void)refresh {
    if (self.refreshing || self.operation || self.filterOperation || FWMonotonic()-self.lastRefresh<5000) return;
    self.refreshing=YES;self.lastRefresh=FWMonotonic();uint64_t generation=self.generation;
    [NEFilterManager.sharedManager loadFromPreferencesWithCompletionHandler:^(NSError *error){dispatch_async(dispatch_get_main_queue(),^{
        self.refreshing=NO;if(error || generation!=self.generation) return;
        self.filterState=NEFilterManager.sharedManager.enabled?@"on":@"off";
        if([self.filterState isEqual:@"off"]) {
            [self resetMissingWindow];
            if([self.failure[@"detail"] isEqual:FWServiceMissing]) self.failure=nil;
        }
    });}];
    [self refreshProperties];
}
-(void)refreshProperties {
    OSSystemExtensionRequest *request=[OSSystemExtensionRequest propertiesRequestForExtension:FWExtensionID() queue:dispatch_get_main_queue()];
    self.propertiesRequest=request;self.propertiesGeneration=self.generation;
    request.delegate=self;[OSSystemExtensionManager.sharedManager submitRequest:request];
}
-(void)enable {
    // An update in flight finishes the installation; only add the intent to turn filtering on afterwards.
    if (self.upgrading && self.operation) {self.enableAfterActivation=YES;self.failure=nil;return;}
    if (self.operation || self.filterOperation || [self.filterState isEqual:@"needs_consent"]) return;
    self.removalConfirmed=NO;
    self.generation++;self.failure=nil;self.enableAfterActivation=YES;self.activationNeedsReboot=NO;self.uninstalling=NO;
    self.bundledVersionConfirmed=NO;[self resetMissingWindow];
    if ([self.extensionState isEqual:@"installed"] && [self hasBundledVersion]) {[self enableFilter];return;}
    self.extensionState=@"activating";
    [self submitActivation];
}
-(void)enableFilter {
    self.filterOperation=YES;
    uint64_t generation=self.generation;
    [self loadFilter:^(NSError *error){
        if (generation!=self.generation) return;
        if (error) {[self fail:error activation:NO];return;}
        NEFilterManager *manager=[self filterManager];
        NEFilterProviderConfiguration *config=manager.providerConfiguration;
        // macOS asks for consent only when saving a new configuration; turning a saved one back on doesn't ask.
        if(!config) {
            config=[NEFilterProviderConfiguration new];config.filterSockets=YES;config.filterPackets=NO;
            config.filterDataProviderBundleIdentifier=[self extensionIdentifier];
            self.filterState=@"needs_consent";
        }
        manager.providerConfiguration=config;
        manager.localizedDescription=manager.localizedDescription?:@"OpenNavo";
        manager.enabled=YES;
        [self saveFilter:^(NSError *failure){
            if (generation!=self.generation) return;
            if (failure) {[self fail:failure activation:NO];return;}
            self.filterState=@"on";self.failure=nil;
            self.enableAfterActivation=NO;self.filterOperation=NO;[self refreshProperties];
        }];
    }];
}
-(void)disable:(BOOL)uninstall {
    self.generation++;uint64_t generation=self.generation;
    self.removalConfirmed=NO;
    self.bundledVersionConfirmed=NO;[self resetMissingWindow];
    self.enableAfterActivation=NO;self.activationNeedsReboot=NO;self.uninstalling=uninstall;self.upgrading=NO;self.failure=nil;self.operation=nil;self.filterOperation=YES;
    if(uninstall) self.extensionState=@"uninstalling";
    [self loadFilter:^(NSError *error){
        if (generation!=self.generation) return;
        if (uninstall) {
            // Removal can't depend on the filter configuration: consent may never have been given, or the user deleted
            // the filter in System Settings. Remove whatever exists, then deactivate either way.
            void (^deactivate)(NSError *)=^(NSError *ignored){
                (void)ignored;
                if (generation!=self.generation) return;
                self.filterState=@"off";self.filterOperation=NO;
                self.operation=[OSSystemExtensionRequest deactivationRequestForExtension:[self extensionIdentifier] queue:dispatch_get_main_queue()];
                self.operation.delegate=self;[OSSystemExtensionManager.sharedManager submitRequest:self.operation];
            };
            if (error || ![self filterManager].providerConfiguration) {deactivate(nil);return;}
            [self filterManager].enabled=NO;
            [self removeFilter:deactivate];
            return;
        }
        if (error) {[self fail:error activation:NO];return;}
        [self filterManager].enabled=NO;
        [self saveFilter:^(NSError *failure){
            if (generation!=self.generation) return;
            if(failure) {[self fail:failure activation:NO];return;}
            self.filterState=@"off";self.failure=nil;self.filterOperation=NO;
        }];
    }];
}
-(void)requestNeedsUserApproval:(OSSystemExtensionRequest *)request {if(request==self.operation) self.extensionState=@"needs_approval";}
-(OSSystemExtensionReplacementAction)request:(OSSystemExtensionRequest *)request actionForReplacingExtension:(OSSystemExtensionProperties *)existing withExtension:(OSSystemExtensionProperties *)replacement {
    if(request!=self.operation || ![existing.bundleIdentifier isEqual:[self extensionIdentifier]]
        || ![replacement.bundleIdentifier isEqual:[self extensionIdentifier]]) return OSSystemExtensionReplacementActionCancel;
    // A copy macOS deletes at the next restart must not block turning the firewall back on, even at the same build.
    if(existing.isUninstalling) return OSSystemExtensionReplacementActionReplace;
    BOOL same=[existing.bundleVersion isEqual:replacement.bundleVersion] && [existing.bundleShortVersion isEqual:replacement.bundleShortVersion];
    self.canceledSameVersion=same;
    return same?OSSystemExtensionReplacementActionCancel:OSSystemExtensionReplacementActionReplace;
}
-(void)request:(OSSystemExtensionRequest *)request didFinishWithResult:(OSSystemExtensionRequestResult)result {
    if (request!=self.operation) return;self.operation=nil;
    self.removalConfirmed=self.uninstalling;
    self.failure=nil;self.upgrading=NO;self.canceledSameVersion=NO;
    if (result==OSSystemExtensionRequestWillCompleteAfterReboot) {
        // A removed filter stops at once; macOS only deletes its files at the next restart.
        // Replacement can already be active while the old version awaits removal.
        // Preserve the enable intent and reconcile immediately, without the poll delay.
        self.extensionState=self.uninstalling?@"removal_pending":@"needs_reboot";self.activationNeedsReboot=!self.uninstalling;
        self.installedVersion=nil;self.installedBuildVersion=nil;self.uninstalling=NO;
        [self refreshProperties];return;
    }
    self.activationNeedsReboot=NO;
    NSDictionary *info=[self bundledExtensionInfo];
    self.installedVersion=self.uninstalling?nil:info[@"CFBundleShortVersionString"];
    self.installedBuildVersion=self.uninstalling?nil:info[@"CFBundleVersion"];
    self.extensionState=self.uninstalling?@"not_installed":@"installed";self.uninstalling=NO;
    if(self.enableAfterActivation) [self enableFilter];
}
-(void)request:(OSSystemExtensionRequest *)request didFailWithError:(NSError *)error {
    if(request!=self.operation) return;
    // Declining to replace the same build ends the request as canceled, but that build is the one already running.
    if(self.canceledSameVersion && [error.domain isEqual:OSSystemExtensionErrorDomain] && error.code==OSSystemExtensionErrorRequestCanceled) {
        [self request:request didFinishWithResult:OSSystemExtensionRequestCompleted];return;
    }
    self.canceledSameVersion=NO;
    [self fail:error activation:YES];
}
-(void)request:(OSSystemExtensionRequest *)request foundProperties:(NSArray<OSSystemExtensionProperties *> *)properties {
    if(request!=self.propertiesRequest || self.propertiesGeneration!=self.generation || self.operation || self.filterOperation) return;
    self.propertiesRequest=nil;
    OSSystemExtensionProperties *enabled=nil;
    BOOL awaitingApproval=NO,awaitingBundled=NO,removing=NO;
    NSDictionary *info=[self bundledExtensionInfo];
    for(OSSystemExtensionProperties *p in properties) {
        BOOL bundled=[p.bundleShortVersion isEqual:info[@"CFBundleShortVersionString"]] && [p.bundleVersion isEqual:info[@"CFBundleVersion"]];
        if(p.isUninstalling) {removing=YES;continue;}
        if(p.isAwaitingUserApproval) {awaitingApproval=YES;awaitingBundled=awaitingBundled||bundled;continue;}
        if(p.isEnabled && (!enabled || bundled)) enabled=p;
    }
    self.removalConfirmed=!enabled && !awaitingApproval;
    self.installedVersion=enabled.bundleShortVersion;self.installedBuildVersion=enabled.bundleVersion;
    BOOL targetEnabled=enabled && [self hasBundledVersion];
    self.bundledVersionConfirmed=targetEnabled;
    if(targetEnabled) self.activationNeedsReboot=NO;
    if(self.activationNeedsReboot) self.extensionState=@"needs_reboot";
    else if(targetEnabled) self.extensionState=@"installed";
    // An update waiting for approval sits behind the old build that still runs; ask rather than hide it.
    else if(awaitingBundled) self.extensionState=@"needs_approval";
    else if(enabled) self.extensionState=@"installed";
    else if(awaitingApproval) self.extensionState=@"needs_approval";
    // Versions awaiting deletion no longer run; only a reboot-bound activation is still installing.
    else self.extensionState=removing?@"removal_pending":@"not_installed";
    // Drop failures that macOS has since resolved, so they don't outlive their cause.
    NSString *stale=self.failure[@"code"];
    if(([stale isEqual:@"E_FW_ACTIVATION"] && targetEnabled) || ([stale isEqual:@"E_FW_REMOVAL"] && !enabled)) self.failure=nil;
    if(targetEnabled && self.enableAfterActivation && !self.filterOperation) [self enableFilter];
    // After an app update the previous build keeps running under its own versioned service name, which this app can't
    // reach, so connections without a rule pass unasked. macOS replaces it without asking (same team): do it once per launch.
    else if(enabled && !targetEnabled && !awaitingBundled && !self.upgradeAttempted && !self.filterOperation && !self.uninstalling) {
        self.upgradeAttempted=YES;self.upgrading=YES;self.extensionState=@"activating";
        [self submitActivation];
    }
}
@end

static FWHost *FWSharedHost(void) {static FWHost *host;static dispatch_once_t once;dispatch_once(&once,^{host=[FWHost new];});return host;}
static xpc_connection_t FWClient;
static uint64_t FWExplicitDisconnects;
static NSLock *FWClientLock(void) {static NSLock *lock;static dispatch_once_t once;dispatch_once(&once,^{lock=[NSLock new];});return lock;}
static uint64_t FWDisconnectGeneration(void) {
    NSLock *lock=FWClientLock();[lock lock];uint64_t value=FWExplicitDisconnects;[lock unlock];return value;
}
static void FWDiscardConnection(xpc_connection_t connection) {
    NSLock *lock=FWClientLock();[lock lock];if(FWClient==connection) FWClient=nil;[lock unlock];
    if(connection) xpc_connection_cancel(connection);
}
static xpc_connection_t FWConnection(void) {
    NSLock *lock=FWClientLock();[lock lock];
    if (!FWClient) {
        NSString *service=[NSBundle bundleWithPath:FWExtensionPath()].infoDictionary[@"NetworkExtension"][@"NEMachServiceName"];
        if (!service.length) {[lock unlock];return nil;}
        xpc_connection_t connection=xpc_connection_create_mach_service(service.UTF8String,NULL,XPC_CONNECTION_MACH_SERVICE_PRIVILEGED);
        xpc_connection_set_event_handler(connection,^(xpc_object_t event){
            if (xpc_get_type(event)==XPC_TYPE_ERROR) {[lock lock];if(FWClient==connection) FWClient=nil;[lock unlock];xpc_connection_cancel(connection);xpc_connection_set_event_handler(connection,^(xpc_object_t ignored){(void)ignored;});}
        });xpc_connection_resume(connection);FWClient=connection;
    }
    xpc_connection_t connection=FWClient;[lock unlock];return connection;
}
void on_fw_disconnect(void) {
    NSLock *lock=FWClientLock();[lock lock];xpc_connection_t connection=FWClient;FWClient=nil;FWExplicitDisconnects++;[lock unlock];
    if(connection) xpc_connection_cancel(connection);
}
static NSDictionary *FWExchange(NSDictionary *request) {
    NSData *data=[NSJSONSerialization dataWithJSONObject:request options:0 error:nil];
    if (!data || data.length>320*1024*1024) return FWResult(nil,FWFailure(@"E_FW_BLOCKLIST_TOO_LARGE",nil));
    if(NSThread.isMainThread) return FWResult(nil,FWFailure(@"E_FW_DISCONNECTED",nil));
    __block uint64_t generation,disconnects;
    __block xpc_connection_t connection;
    dispatch_sync(dispatch_get_main_queue(),^{
        generation=FWSharedHost().generation;disconnects=FWDisconnectGeneration();
        connection=FWConnection();
    });
    if(!connection) return FWResult(nil,FWFailure(@"E_FW_DISCONNECTED",nil));
    uint64_t started=FWMonotonic();
    xpc_object_t message=xpc_dictionary_create(NULL,NULL,0);xpc_dictionary_set_data(message,"request",data.bytes,data.length);
    dispatch_semaphore_t done=dispatch_semaphore_create(0);__block NSDictionary *output;__block NSString *authorizationDetail;
    xpc_connection_send_message_with_reply(connection,message,dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^(xpc_object_t reply){
        if (xpc_get_type(reply)==XPC_TYPE_DICTIONARY) {
            audit_token_t audit;xpc_dictionary_get_audit_token(reply,&audit);
            size_t length=0;const void *bytes=xpc_dictionary_get_data(reply,"response",&length);
            NSString *detail=nil;
            if (bytes && length<=64*1024*1024 && FWAuthorizeWithDetail(audit,FWExtensionID(),&detail)) {
                id value=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:bytes length:length] options:0 error:nil];
                if ([value isKindOfClass:NSDictionary.class]) output=value;
            }
            authorizationDetail=detail;
        }
        // Count only heartbeat probes. Settings/list requests, our own cancels,
        // signature failures, interruptions and ordinary timeouts aren't proof
        // that launchd has lost the Mach endpoint.
        if([request[@"op"] isEqual:@"snapshot"]) {
            uint64_t now=FWMonotonic();
            FWPeerState state=FWClassifyPeer(output!=nil,reply==XPC_ERROR_CONNECTION_INVALID,now-started);
            dispatch_async(dispatch_get_main_queue(),^{
                [FWSharedHost() observePeer:state at:now generation:generation disconnects:disconnects];
            });
        }
        dispatch_semaphore_signal(done);
    });
    BOOL slow=[@[@"blocklist_add",@"blocklist_refresh"] containsObject:request[@"op"]];
    BOOL timedOut=dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,(slow?40:3)*NSEC_PER_SEC))!=0;
    if (timedOut) {
        // A deadline is not evidence that XPC disconnected: the extension may be saving a large state. Dropping the
        // connection would make it release held connections and clear open questions as if the app had quit. Keep
        // it; a dead peer still ends with a connection error, and snapshots reconcile an accepted configuration ID.
        return FWResult(nil,FWFailure(slow?@"E_FW_BLOCKLIST_FETCH":@"E_FW_DISCONNECTED",@"firewall_operation_timeout"));
    }
    if (!output) {
        FWDiscardConnection(connection);return FWResult(nil,FWFailure(@"E_FW_DISCONNECTED",authorizationDetail));
    }
    return output;
}
char *on_fw_native_request(const char *json) {
    @autoreleasepool {
        if (!json || strlen(json)>320*1024*1024) return NULL;
        NSDictionary *input=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:json length:strlen(json)] options:0 error:nil];
        if (![input isKindOfClass:NSDictionary.class]) return NULL;
        NSString *op=input[@"op"];NSString *availability=FWAvailability();
        __block NSDictionary *output;
        if ([op isEqual:@"status"] && ![availability isEqual:@"available"]) output=FWResult([FWSharedHost() status],nil);
        else if (![op isEqual:@"status"] && ![availability isEqual:@"available"]) output=FWResult(nil,FWFailure([availability isEqual:@"not_in_applications"]?@"E_FW_NOT_IN_APPLICATIONS":@"E_FW_NOT_BUNDLED",nil));
        else if ([op isEqual:@"cleanup"]) {
            if (NSThread.isMainThread) return NULL;
            __block BOOL confirmed=NO;
            dispatch_sync(dispatch_get_main_queue(),^{
                FWHost *host=FWSharedHost();
                confirmed=host.removalConfirmed && !host.operation && !host.filterOperation
                    && ([@[@"not_installed",@"removal_pending"] containsObject:host.extensionState]);
            });
            // Unknown initial properties are not proof of removal. Rust retries on its next poll.
            output=!confirmed?FWResult(@NO,nil):FWCleanupData(FWExtensionID())?FWResult(@YES,nil):FWResult(nil,FWFailure(@"E_FW_REMOVAL",nil));
        }
        else if ([op isEqual:@"identify"]) {
            NSDictionary *app=[input[@"path"] isKindOfClass:NSString.class]?FWIdentifyPath(input[@"path"]):nil;
            output=app?FWResult(app,nil):FWResult(nil,FWFailure(@"E_FW_RULE_INVALID",nil));
        } else if ([@[@"status",@"enable",@"disable",@"uninstall",@"open_approval"] containsObject:op]) {
            if (NSThread.isMainThread) return NULL; // Rust must call on a blocking worker.
            dispatch_semaphore_t done=dispatch_semaphore_create(0);
            dispatch_async(dispatch_get_main_queue(),^{
                FWHost *host=FWSharedHost();
                if ([op isEqual:@"enable"]) [host enable];
                else if ([op isEqual:@"disable"]) [host disable:NO];
                else if ([op isEqual:@"uninstall"]) [host disable:YES];
                else if ([op isEqual:@"open_approval"]) {
                    NSString *url;
                    if (@available(macOS 15.0,*)) url=@"x-apple.systempreferences:com.apple.LoginItems-Settings.extension?ExtensionItems";
                    else url=@"x-apple.systempreferences:com.apple.preference.security?General";
                    BOOL opened=[NSWorkspace.sharedWorkspace openURL:[NSURL URLWithString:url]];
                    output=FWResult(nil,opened?nil:FWFailure(@"E_FW_ACTIVATION",nil));
                }
                if (!output) output=FWResult([host status],nil);
                dispatch_semaphore_signal(done);
            });
            if (dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,3*NSEC_PER_SEC))!=0) return NULL;
        } else output=FWExchange(input);
        NSData *result=[NSJSONSerialization dataWithJSONObject:output options:0 error:nil];
        return result?strdup([[NSString alloc] initWithData:result encoding:NSUTF8StringEncoding].UTF8String):NULL;
    }
}
void on_fw_native_free(char *value) {free(value);}

// Called only on AppKit's main thread. orderFrontRegardless never activates the app, and a click must not either:
// answering while typing elsewhere would otherwise bring OpenNavo (and its main window) to the front. Like the menu-bar
// panels, set the window-server tag non-activating panels use. ⌘H on OpenNavo must not hide an open question.
void on_fw_alert_present(void *pointer,int show) {
    NSWindow *window=(__bridge NSWindow *)pointer;
    SEL preventsActivation=NSSelectorFromString(@"_setPreventsActivation:");
    if ([window respondsToSelector:preventsActivation]) ((void (*)(id,SEL,BOOL))objc_msgSend)(window,preventsActivation,YES);
    window.canHide=NO;
    window.collectionBehavior=NSWindowCollectionBehaviorCanJoinAllSpaces|NSWindowCollectionBehaviorFullScreenAuxiliary;
    window.level=NSFloatingWindowLevel;window.hidesOnDeactivate=NO;
    NSScreen *screen=NSScreen.screens.firstObject;
    if (screen) {
        NSRect visible=screen.visibleFrame,frame=window.frame;
        [window setFrameOrigin:NSMakePoint(MAX(NSMinX(visible),NSMaxX(visible)-frame.size.width-12),MAX(NSMinY(visible),NSMaxY(visible)-frame.size.height-12))];
    }
    if(show) [window orderFrontRegardless];
}
