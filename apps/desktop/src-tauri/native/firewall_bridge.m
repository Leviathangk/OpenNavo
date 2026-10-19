// SPDX-License-Identifier: GPL-3.0-only
#import <AppKit/AppKit.h>
#import <NetworkExtension/NetworkExtension.h>
#import <SystemExtensions/SystemExtensions.h>
#import <xpc/xpc.h>
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
    NSString *code=([error.domain isEqual:NEFilterErrorDomain] && error.code==NEFilterManagerErrorConfigurationPermissionDenied)?@"E_FW_CONSENT_DENIED":@"E_FW_ACTIVATION";
    self.failure=@{@"code":code,@"detail":error.localizedDescription?:NSNull.null};
    [self resetMissingWindow];
    if (activation) self.extensionState=@"failed";
    self.enableAfterActivation=NO;self.activationNeedsReboot=NO;self.operation=nil;self.uninstalling=NO;self.filterOperation=NO;
    self.filterState=[code isEqual:@"E_FW_CONSENT_DENIED"]?@"off":([self filterManager].enabled?@"on":@"off");
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
    if (self.operation || self.filterOperation || [self.filterState isEqual:@"needs_consent"]) return;
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
        if(!config) {
            config=[NEFilterProviderConfiguration new];config.filterSockets=YES;config.filterPackets=NO;
            config.filterDataProviderBundleIdentifier=[self extensionIdentifier];
        }
        manager.providerConfiguration=config;
        manager.localizedDescription=manager.localizedDescription?:@"OpenNavo";
        manager.enabled=YES;self.filterState=@"needs_consent";
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
    self.bundledVersionConfirmed=NO;[self resetMissingWindow];
    self.enableAfterActivation=NO;self.activationNeedsReboot=NO;self.uninstalling=uninstall;self.failure=nil;self.operation=nil;self.filterOperation=YES;
    if(uninstall) self.extensionState=@"uninstalling";
    [self loadFilter:^(NSError *error){
        if (generation!=self.generation) return;
        if (error) {[self fail:error activation:NO];return;}
        [self filterManager].enabled=NO;
        void (^saved)(NSError *)=^(NSError *failure){
            if (generation!=self.generation) return;
            if(failure) {[self fail:failure activation:NO];return;}
            self.filterState=@"off";self.failure=nil;self.filterOperation=NO;
            if (uninstall) {
                self.operation=[OSSystemExtensionRequest deactivationRequestForExtension:[self extensionIdentifier] queue:dispatch_get_main_queue()];
                self.operation.delegate=self;[OSSystemExtensionManager.sharedManager submitRequest:self.operation];
            }
        };
        if (uninstall) [self removeFilter:saved];
        else [self saveFilter:saved];
    }];
}
-(void)requestNeedsUserApproval:(OSSystemExtensionRequest *)request {if(request==self.operation) self.extensionState=@"needs_approval";}
-(OSSystemExtensionReplacementAction)request:(OSSystemExtensionRequest *)request actionForReplacingExtension:(OSSystemExtensionProperties *)existing withExtension:(OSSystemExtensionProperties *)replacement {
    if(request!=self.operation || ![existing.bundleIdentifier isEqual:[self extensionIdentifier]]
        || ![replacement.bundleIdentifier isEqual:[self extensionIdentifier]]) return OSSystemExtensionReplacementActionCancel;
    BOOL same=[existing.bundleVersion isEqual:replacement.bundleVersion] && [existing.bundleShortVersion isEqual:replacement.bundleShortVersion];
    return same?OSSystemExtensionReplacementActionCancel:OSSystemExtensionReplacementActionReplace;
}
-(void)request:(OSSystemExtensionRequest *)request didFinishWithResult:(OSSystemExtensionRequestResult)result {
    if (request!=self.operation) return;self.operation=nil;
    self.failure=nil;
    if (result==OSSystemExtensionRequestWillCompleteAfterReboot) {
        // Replacement can already be active while the old version awaits removal.
        // Preserve the enable intent and reconcile immediately, without the poll delay.
        self.extensionState=@"needs_reboot";self.activationNeedsReboot=!self.uninstalling;
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
-(void)request:(OSSystemExtensionRequest *)request didFailWithError:(NSError *)error {if(request==self.operation) [self fail:error activation:YES];}
-(void)request:(OSSystemExtensionRequest *)request foundProperties:(NSArray<OSSystemExtensionProperties *> *)properties {
    if(request!=self.propertiesRequest || self.propertiesGeneration!=self.generation || self.operation) return;
    self.propertiesRequest=nil;
    OSSystemExtensionProperties *enabled=nil;
    BOOL awaitingApproval=NO,removing=NO;
    NSDictionary *info=[self bundledExtensionInfo];
    for(OSSystemExtensionProperties *p in properties) {
        if(p.isUninstalling) {removing=YES;continue;}
        if(p.isAwaitingUserApproval) {awaitingApproval=YES;continue;}
        if(p.isEnabled && (!enabled || ([p.bundleShortVersion isEqual:info[@"CFBundleShortVersionString"]]
            && [p.bundleVersion isEqual:info[@"CFBundleVersion"]]))) enabled=p;
    }
    self.installedVersion=enabled.bundleShortVersion;self.installedBuildVersion=enabled.bundleVersion;
    BOOL targetEnabled=enabled && [self hasBundledVersion];
    self.bundledVersionConfirmed=targetEnabled;
    if(targetEnabled) self.activationNeedsReboot=NO;
    if(self.activationNeedsReboot) self.extensionState=@"needs_reboot";
    else if(enabled) self.extensionState=@"installed";
    else if(awaitingApproval) self.extensionState=@"needs_approval";
    else self.extensionState=removing?@"needs_reboot":@"not_installed";
    if(targetEnabled && self.enableAfterActivation && !self.filterOperation) [self enableFilter];
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
    if (timedOut && (slow || [request[@"op"] isEqual:@"configure"])) {
        // An operation deadline is not evidence that XPC disconnected. Keep the
        // connection and let snapshots reconcile an accepted configuration ID.
        return FWResult(nil,FWFailure(slow?@"E_FW_BLOCKLIST_FETCH":@"E_FW_ACTIVATION",@"firewall_operation_timeout"));
    }
    if (timedOut || !output) {
        FWDiscardConnection(connection);return FWResult(nil,FWFailure(@"E_FW_DISCONNECTED",timedOut?nil:authorizationDetail));
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

// Called only on AppKit's main thread. orderFrontRegardless never activates the app.
void on_fw_alert_present(void *pointer,int show) {
    NSWindow *window=(__bridge NSWindow *)pointer;
    window.collectionBehavior=NSWindowCollectionBehaviorCanJoinAllSpaces|NSWindowCollectionBehaviorFullScreenAuxiliary;
    window.level=NSFloatingWindowLevel;window.hidesOnDeactivate=NO;
    NSScreen *screen=NSScreen.screens.firstObject;
    if (screen) {
        NSRect visible=screen.visibleFrame,frame=window.frame;
        [window setFrameOrigin:NSMakePoint(MAX(NSMinX(visible),NSMaxX(visible)-frame.size.width-12),MAX(NSMinY(visible),NSMaxY(visible)-frame.size.height-12))];
    }
    if(show) [window orderFrontRegardless];
}
