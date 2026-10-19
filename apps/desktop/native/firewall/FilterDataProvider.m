// SPDX-License-Identifier: GPL-3.0-only
// Adapted from LuLu v4.5.1, LuLu/Extension/FilterDataProvider.m.
// Copyright (c) 2020 Objective-See / Patrick Wardle. All rights reserved.
// OpenNavo changes: Rust policy engine, bounded pause lifetime, authenticated JSON
// IPC, durable ring buffer, independent watchdog, and no rules on failure.
#import "FilterDataProvider.h"
#import "FWIdentity.h"
#import <xpc/xpc.h>
#import <sys/stat.h>
#import <unistd.h>
#import <netinet/in.h>
extern void xpc_connection_get_audit_token(xpc_connection_t,audit_token_t *);
extern void xpc_dictionary_get_audit_token(xpc_object_t,audit_token_t *);
extern char *on_fw_core_call(const char *);
extern void on_fw_core_free(char *);

static NSArray *FWArray(id value) {return [value isKindOfClass:NSArray.class]?value:@[];}
static NSDictionary *FWDictionary(id value) {return [value isKindOfClass:NSDictionary.class]?value:@{};}
static const NSUInteger FWMaxBytes=50*1024*1024;
static NSDictionary *FWError(NSString *code) {return @{@"value":NSNull.null,@"error":@{@"code":code,@"message":@"firewall_request",@"detail":NSNull.null}};}

@interface FWListDownload:NSObject<NSURLSessionDataDelegate,NSURLSessionTaskDelegate>
@property NSMutableData *data;
@property NSURLSession *session;
@property(copy) void (^completion)(NSData *,NSString *);
@property NSString *failure;
+(void)fetch:(NSString *)url completion:(void (^)(NSData *,NSString *))completion;
@end
@implementation FWListDownload
+(void)fetch:(NSString *)text completion:(void (^)(NSData *,NSString *))completion {
    NSURL *url=[NSURL URLWithString:text];
    if (![url.scheme isEqual:@"https"] || !url.host.length || url.user || url.password) {completion(nil,@"E_FW_BLOCKLIST_URL");return;}
    FWListDownload *task=[FWListDownload new];task.data=[NSMutableData data];task.completion=completion;
    NSURLSessionConfiguration *config=NSURLSessionConfiguration.ephemeralSessionConfiguration;
    config.timeoutIntervalForRequest=20;config.timeoutIntervalForResource=30;config.URLCache=nil;
    task.session=[NSURLSession sessionWithConfiguration:config delegate:task delegateQueue:nil];
    [[task.session dataTaskWithURL:url] resume];
}
-(void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completion {
    (void)session;(void)task;(void)response;
    if (![request.URL.scheme isEqual:@"https"] || request.URL.user || request.URL.password) {self.failure=@"E_FW_BLOCKLIST_URL";completion(nil);} else completion(request);
}
-(void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)response completionHandler:(void (^)(NSURLSessionResponseDisposition))completion {
    (void)session;(void)task;
    if (response.expectedContentLength>(int64_t)FWMaxBytes) self.failure=@"E_FW_BLOCKLIST_TOO_LARGE";
    else if (![response isKindOfClass:NSHTTPURLResponse.class] || ((NSHTTPURLResponse *)response).statusCode!=200) self.failure=@"E_FW_BLOCKLIST_FETCH";
    completion(self.failure?NSURLSessionResponseCancel:NSURLSessionResponseAllow);
}
-(void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
    (void)session;
    if (self.data.length+data.length>FWMaxBytes) {self.failure=@"E_FW_BLOCKLIST_TOO_LARGE";[task cancel];} else [self.data appendData:data];
}
-(void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
    (void)task;
    self.completion(self.failure||error?nil:self.data,self.failure?:(error?@"E_FW_BLOCKLIST_FETCH":nil));
    self.completion=nil;[session finishTasksAndInvalidate];self.session=nil;
}
@end

@interface FWService:NSObject
@property(weak) NEFilterDataProvider *provider;
@property BOOL filtering;
@property dispatch_queue_t queue;
@property dispatch_source_t timer;
@property dispatch_source_t watchdog;
@property xpc_connection_t listener;
@property xpc_connection_t client;
@property NSLock *pausedLock;
@property NSMutableDictionary<NSString *,NSDictionary *> *paused;
@property NSMutableSet<NSString *> *refreshing;
@property NSMutableDictionary<NSData *,NSDictionary *> *identities;
@property NSString *statePath;
@property uint64_t lastHeartbeat;
@property BOOL healthy;
@property BOOL stopping;
@property uint64_t lastPersist;
@end

@implementation FWService
-(instancetype)init {
    self=[super init];if (self) {
        _queue=dispatch_queue_create("com.opennavo.firewall.policy",DISPATCH_QUEUE_SERIAL);
        _pausedLock=[NSLock new];_paused=[NSMutableDictionary dictionary];_refreshing=[NSMutableSet set];_identities=[NSMutableDictionary dictionary];
        dispatch_async(_queue,^{self.healthy=[self prepareStorage];[self startListener];[self startTimers];});
    }return self;
}
-(NSDictionary *)call:(NSDictionary *)request {
    @try {
    NSMutableDictionary *input=[request mutableCopy];input[@"now"]=@(FWMonotonic());input[@"at"]=@(FWNow());input[@"dayStart"]=@(FWDayStart());
    NSData *data=[NSJSONSerialization dataWithJSONObject:input options:0 error:nil];
    if (!data) return FWError(@"E_FW_RULE_INVALID");
    NSString *json=[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    char *raw=on_fw_core_call(json.UTF8String);
    if (!raw) {self.healthy=NO;[self releaseAll];return FWError(@"E_FW_DISCONNECTED");}
    NSDictionary *result=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:raw length:strlen(raw)] options:0 error:nil];on_fw_core_free(raw);
    if (![result isKindOfClass:NSDictionary.class]) {self.healthy=NO;[self releaseAll];return FWError(@"E_FW_DISCONNECTED");}
    for (NSDictionary *resolved in FWArray(result[@"resolutions"])) [self resume:resolved[@"id"] action:resolved[@"action"]];
    return result;
    } @catch (NSException *exception) {(void)exception;self.healthy=NO;[self releaseAll];return FWError(@"E_FW_DISCONNECTED");}
}
-(void)resume:(NSString *)identifier action:(NSString *)action {
    [self.pausedLock lock];NEFilterFlow *flow=self.paused[identifier][@"flow"];[self.paused removeObjectForKey:identifier];[self.pausedLock unlock];
    if (flow) [self.provider resumeFlow:flow withVerdict:[action isEqual:@"block"]?[NEFilterNewFlowVerdict dropVerdict]:[NEFilterNewFlowVerdict allowVerdict]];
}
-(void)releaseAll {
    [self.pausedLock lock];NSArray *keys=self.paused.allKeys;[self.pausedLock unlock];
    for (NSString *key in keys) [self resume:key action:@"allow"];
}
-(BOOL)prepareStorage {
    NSString *identifier=NSBundle.mainBundle.bundleIdentifier;
    if (!identifier.length || [identifier containsString:@"/"]) return NO;
    NSString *path=[@"/Library/Application Support/OpenNavo/Firewall" stringByAppendingPathComponent:identifier];
    // Never traverse a writable or symlinked ancestor for privileged state.
    for (NSString *component in @[@"/Library",@"/Library/Application Support",@"/Library/Application Support/OpenNavo",@"/Library/Application Support/OpenNavo/Firewall"]) {
        struct stat parent;
        if (lstat(component.fileSystemRepresentation,&parent)!=0) {
            if (mkdir(component.fileSystemRepresentation,0700)!=0) return NO;
            if (lstat(component.fileSystemRepresentation,&parent)!=0) return NO;
        }
        if (!S_ISDIR(parent.st_mode) || parent.st_uid!=0 || (parent.st_mode&0022)) return NO;
    }
    if (![NSFileManager.defaultManager createDirectoryAtPath:path withIntermediateDirectories:YES attributes:@{NSFilePosixPermissions:@0700} error:nil]) return NO;
    struct stat st;
    if (lstat(path.fileSystemRepresentation,&st)!=0 || !S_ISDIR(st.st_mode) || st.st_uid!=geteuid() || (st.st_mode&0077)) return NO;
    self.statePath=[path stringByAppendingPathComponent:@"state.json"];
    if ([NSFileManager.defaultManager fileExistsAtPath:self.statePath]) {
        if (lstat(self.statePath.fileSystemRepresentation,&st)!=0 || !S_ISREG(st.st_mode) || st.st_uid!=geteuid() || st.st_size>512*1024*1024) return NO;
        NSData *data=[NSData dataWithContentsOfFile:self.statePath];id state=data?[NSJSONSerialization JSONObjectWithData:data options:0 error:nil]:nil;
        if (!state) return NO;
        NSDictionary *result=[self call:@{@"op":@"restore",@"state":state}];
        if (result[@"error"]!=NSNull.null) return NO;
    }
    return YES;
}
-(void)persist {
    if (!self.healthy || !self.statePath) return;
    NSDictionary *result=[self call:@{@"op":@"persist"}];if (result[@"error"]!=NSNull.null) return;
    NSData *data=[NSJSONSerialization dataWithJSONObject:result[@"value"] options:0 error:nil];
    if (![data writeToFile:self.statePath options:NSDataWritingAtomic error:nil]) {self.healthy=NO;[self call:@{@"op":@"disconnect"}];[self releaseAll];}
    else chmod(self.statePath.fileSystemRepresentation,0600);
}
-(NEFilterNewFlowVerdict *)handleNewFlow:(NEFilterFlow *)flow {
    // LuLu also checks direction here because inbound callbacks can still arrive.
    if (flow.direction!=NETrafficDirectionOutbound || ![flow isKindOfClass:NEFilterSocketFlow.class] || self.stopping || !self.filtering || !self.healthy) return [NEFilterNewFlowVerdict allowVerdict];
    NEFilterSocketFlow *socket=(NEFilterSocketFlow *)flow;
    if (![socket.remoteEndpoint isKindOfClass:NWHostEndpoint.class] || (socket.socketProtocol!=IPPROTO_TCP && socket.socketProtocol!=IPPROTO_UDP)) return [NEFilterNewFlowVerdict allowVerdict];
    // The provider's own downloads must stay independent of App attribution and
    // baseline settings. This audit token comes from NetworkExtension, not XPC.
    audit_token_t source={0};
    if(flow.sourceAppAuditToken.length==sizeof(source)) {
        [flow.sourceAppAuditToken getBytes:&source length:sizeof(source)];
        if(audit_token_to_pid(source)==getpid()) return [NEFilterNewFlowVerdict allowVerdict];
    }
    uint64_t now=FWMonotonic();BOOL udp=socket.socketProtocol==IPPROTO_UDP;
    NSString *identifier=flow.identifier.UUIDString;
    [self.pausedLock lock];
    if (self.paused.count>=4096) {[self.pausedLock unlock];return [NEFilterNewFlowVerdict allowVerdict];}
    BOOL clientBound=self.lastHeartbeat && (now<self.lastHeartbeat || now-self.lastHeartbeat<5000);
    self.paused[identifier]=@{@"flow":flow,@"deadline":@(now+(udp?5000:120000)),@"policyDeadline":@(now+1000),@"evaluated":@NO,@"clientBound":@(clientBound)};
    [self.pausedLock unlock];
    // Return promptly. Signing, policy, storage and baseline work never block this callback.
    // resumeFlow is the NE asynchronous counterpart to this pause verdict.
    dispatch_async(self.queue,^{ @autoreleasepool { @try {
        // The watchdog may already have released this socket while it waited
        // behind signing work. Discard it before touching any process identity.
        [self.pausedLock lock];NSDictionary *queued=self.paused[identifier];[self.pausedLock unlock];
        if (!queued) return;
        if (self.stopping || !self.filtering || !self.healthy || FWMonotonic()>=[queued[@"policyDeadline"] unsignedLongLongValue]) {
            [self resume:identifier action:@"allow"];return;
        }
        NWHostEndpoint *remote=(NWHostEndpoint *)socket.remoteEndpoint;
        // Audit tokens include the process identity/version. Reuse a recently
        // verified identity across that process's connections; never cache PID alone.
        NSData *token=flow.sourceAppAuditToken;
        NSDictionary *cached=token?self.identities[token]:nil;
        NSDictionary *app=cached && FWMonotonic()-[cached[@"at"] unsignedLongLongValue]<30000?cached[@"app"]:FWIdentifyToken(token);
        if (app && token) {
            if (self.identities.count>=4096) [self.identities removeAllObjects];
            if (!cached || FWMonotonic()-[cached[@"at"] unsignedLongLongValue]>=30000) self.identities[token]=@{@"app":app,@"at":@(FWMonotonic())};
        }
        // A slow Security call itself may outlive the watchdog too.
        [self.pausedLock lock];BOOL stillPaused=self.paused[identifier]!=nil;[self.pausedLock unlock];
        if (!stillPaused) return;
        BOOL identified=app!=nil;
        audit_token_t audit={0};pid_t pid=0;
        if (flow.sourceAppAuditToken.length==sizeof(audit)) {[flow.sourceAppAuditToken getBytes:&audit length:sizeof(audit)];pid=audit_token_to_pid(audit);}
        if (!app) app=@{@"key":@"",@"name":@"?",@"path":[NSString stringWithFormat:@"/unidentified/%d",pid],@"bundlePath":NSNull.null,@"bundleId":NSNull.null,@"signingId":NSNull.null,@"teamId":NSNull.null,@"teamName":NSNull.null,@"signer":@"invalid",@"iconPath":NSNull.null};
        if ([app[@"signer"] isEqual:@"developer_id"] && [app[@"signingId"] isEqual:NSBundle.mainBundle.bundleIdentifier] && [app[@"teamId"] isEqual:FWOwnTeam()]) {[self resume:identifier action:@"allow"];return;}
        NSString *host=socket.remoteHostname?:flow.URL.host;
        uint64_t processStart=FWProcessStart(pid);
        NSDictionary *item=@{@"id":identifier,@"app":app,@"baselineOwner":app[@"baselineOwner"]?:NSNull.null,@"pid":@(MAX(pid,0)),@"processStart":@(processStart),@"args":@[],@"ancestors":@[],@"remoteAddress":remote.hostname?:@"",@"remoteHost":host?:NSNull.null,@"port":@(remote.port.intValue),@"protocol":udp?@"udp":@"tcp",@"at":@(FWNow()-(int64_t)(FWMonotonic()-now)),@"monotonicMs":@(now)};
        [self.pausedLock lock];BOOL active=self.paused[identifier]!=nil;[self.pausedLock unlock];
        if (!active || !identified || !self.healthy || self.stopping || FWMonotonic()-now>=1000) {
            [self call:@{@"op":@"bypass",@"flow":item}];[self resume:identifier action:@"allow"];return;
        }
        NSDictionary *result=[self call:@{@"op":@"flow",@"flow":item}];
        NSString *verdict=FWDictionary(result[@"value"])[@"verdict"]?:@"allow";
        [self.pausedLock lock];NSDictionary *entry=self.paused[identifier];
        if (entry) {
            NSMutableDictionary *updated=[entry mutableCopy];updated[@"evaluated"]=@YES;
            // A client may have reconnected while this flow was queued.
            if ([verdict isEqual:@"pause"]) updated[@"clientBound"]=@YES;
            self.paused[identifier]=updated;
        }
        [self.pausedLock unlock];
        if (!entry) [self call:@{@"op":@"cancel",@"id":identifier}];
        else if (![verdict isEqual:@"pause"]) [self resume:identifier action:verdict];
        // Read expensive process context only after policy asks; mark evaluated
        // first so argv collection cannot trigger the one-second policy watchdog.
        NSDictionary *details=FWProcessDetails(verdict,pid,processStart);
        if (details) [self call:@{@"op":@"flow_details",@"id":identifier,@"args":details[@"args"],@"ancestors":details[@"ancestors"]}];
    } @catch (NSException *exception) {(void)exception;self.healthy=NO;[self releaseAll];[self call:@{@"op":@"disconnect"}];} }});
    return [NEFilterNewFlowVerdict pauseVerdict];
}

-(void)watchdogAt:(uint64_t)now {
    [self.pausedLock lock];NSDictionary *paused=[self.paused copy];uint64_t heartbeat=self.lastHeartbeat;[self.pausedLock unlock];
    BOOL disconnected=!heartbeat || (now>=heartbeat && now-heartbeat>=5000);
    if (disconnected) dispatch_async(self.queue,^{
        // Clear even released questions, including when no socket is paused.
        [self.pausedLock lock];BOOL stale=self.lastHeartbeat==heartbeat;[self.pausedLock unlock];
        if (stale) [self call:@{@"op":@"disconnect"}];
    });
    for (NSString *key in paused) {
        // Existing client-bound pauses still release on disconnect. Flows that
        // arrived offline get the same bounded policy budget to enforce rules;
        // they never wait for an answer from an absent UI.
        BOOL lostClient=disconnected && [paused[key][@"clientBound"] boolValue];
        if (lostClient || now>=[paused[key][@"deadline"] unsignedLongLongValue] || (![paused[key][@"evaluated"] boolValue] && now>=[paused[key][@"policyDeadline"] unsignedLongLongValue])) {
            [self resume:key action:@"allow"];
            NSString *op=!disconnected && [paused[key][@"evaluated"] boolValue] && now>=[paused[key][@"deadline"] unsignedLongLongValue]?@"timeout":@"cancel";
            dispatch_async(self.queue,^{[self call:@{@"op":op,@"id":key}];});
        }
    }
}

-(void)startTimers {
    self.timer=dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER,0,0,self.queue);
    dispatch_source_set_timer(self.timer,dispatch_time(DISPATCH_TIME_NOW,NSEC_PER_SEC),NSEC_PER_SEC,50*NSEC_PER_MSEC);
    __weak typeof(self) weak=self;
    dispatch_source_set_event_handler(self.timer,^{
        typeof(self) strong=weak;if (!strong || strong.stopping) return;
        NSMutableArray *alive=[NSMutableArray array];
        for (NSArray *process in FWArray([strong call:@{@"op":@"processes"}][@"value"])) if ([process[1] unsignedLongLongValue]>0 && FWProcessStart([process[0] intValue])==[process[1] unsignedLongLongValue]) [alive addObject:process];
        [strong call:@{@"op":@"tick",@"now":@(FWMonotonic()),@"alive":alive}];
        [strong refreshDueLists];if (FWMonotonic()-strong.lastPersist>=30000) {strong.lastPersist=FWMonotonic();[strong persist];}
    });dispatch_resume(self.timer);
    // Separate queue: oversized list parsing or disk stalls cannot hold UDP beyond its OS deadline.
    self.watchdog=dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER,0,0,dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0));
    dispatch_source_set_timer(self.watchdog,dispatch_time(DISPATCH_TIME_NOW,250*NSEC_PER_MSEC),250*NSEC_PER_MSEC,10*NSEC_PER_MSEC);
    dispatch_source_set_event_handler(self.watchdog,^{[weak watchdogAt:FWMonotonic()];});dispatch_resume(self.watchdog);
}
-(void)send:(NSDictionary *)response message:(xpc_object_t)message peer:(xpc_connection_t)peer {
    xpc_object_t reply=xpc_dictionary_create_reply(message);if (!reply) return;
    NSData *data=[NSJSONSerialization dataWithJSONObject:response options:0 error:nil];
    xpc_dictionary_set_data(reply,"response",data.bytes,data.length);xpc_connection_send_message(peer,reply);
}
-(void)startListener {
    NSString *name=NSBundle.mainBundle.infoDictionary[@"NetworkExtension"][@"NEMachServiceName"];
    NSString *appID=NSBundle.mainBundle.infoDictionary[@"OpenNavoAppIdentifier"];
    if (!name.length || !appID.length) {self.healthy=NO;return;}
    self.listener=xpc_connection_create_mach_service(name.UTF8String,self.queue,XPC_CONNECTION_MACH_SERVICE_LISTENER);
    __weak typeof(self) weak=self;
    xpc_connection_set_event_handler(self.listener,^(xpc_object_t object){
        typeof(self) strong=weak;if (!strong || xpc_get_type(object)!=XPC_TYPE_CONNECTION) return;
        xpc_connection_t peer=(xpc_connection_t)object;audit_token_t token;xpc_connection_get_audit_token(peer,&token);
        if (!FWAuthorize(token,appID)) {xpc_connection_cancel(peer);return;}
        xpc_connection_set_target_queue(peer,strong.queue);
        xpc_connection_set_event_handler(peer,^(xpc_object_t message){
            typeof(self) provider=weak;if (!provider) return;
            if (xpc_get_type(message)==XPC_TYPE_ERROR) {
                if (provider.client==peer) {provider.client=nil;[provider.pausedLock lock];provider.lastHeartbeat=0;[provider.pausedLock unlock];[provider releaseAll];[provider call:@{@"op":@"disconnect"}];}
                xpc_connection_cancel(peer);xpc_connection_set_event_handler(peer,^(xpc_object_t ignored){(void)ignored;});return;
            }
            if (xpc_get_type(message)!=XPC_TYPE_DICTIONARY) return;
            audit_token_t audit;xpc_dictionary_get_audit_token(message,&audit);
            if (!FWAuthorize(audit,appID)) {xpc_connection_cancel(peer);return;}
            size_t length=0;const void *bytes=xpc_dictionary_get_data(message,"request",&length);
            if (!bytes || length>320*1024*1024) {xpc_connection_cancel(peer);return;}
            NSDictionary *request=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:bytes length:length] options:0 error:nil];
            if (![request isKindOfClass:NSDictionary.class]) { [provider send:FWError(@"E_FW_RULE_INVALID") message:message peer:peer];return;}
            if (provider.client!=peer) {if (provider.client) xpc_connection_cancel(provider.client);provider.client=peer;}
            [provider.pausedLock lock];provider.lastHeartbeat=FWMonotonic();[provider.pausedLock unlock];
            [provider call:@{@"op":@"heartbeat",@"now":@(FWMonotonic())}];
            @try {[provider handleRequest:request completion:^(NSDictionary *response){[provider send:response message:message peer:peer];}];}
            @catch (NSException *exception) {(void)exception;[provider call:@{@"op":@"disconnect"}];[provider releaseAll];[provider send:FWError(@"E_FW_DISCONNECTED") message:message peer:peer];}
        });xpc_connection_resume(peer);
    });xpc_connection_resume(self.listener);
}
-(void)handleRequest:(NSDictionary *)request completion:(void (^)(NSDictionary *))completion {
    NSString *op=request[@"op"];
    if (![op isKindOfClass:NSString.class] || !self.healthy) {completion(FWError(@"E_FW_DISCONNECTED"));return;}
    if ([op isEqual:@"blocklist_add"] || [op isEqual:@"blocklist_refresh"]) {[self loadList:request completion:completion];return;}
    NSMutableDictionary *input=[request mutableCopy];
    if ([op isEqual:@"configure"]) {
        // Core owns idempotency, the single scan and its 60s deadline. Policy is
        // active throughout preparation; disk work never delays this acceptance.
        NSDictionary *result=[self call:request];
        completion(result);
        if ([FWDictionary(result[@"value"])[@"scan"] boolValue]) {
            uint64_t deadline=FWMonotonic()+60000;
            dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
                @try {
                    FWScanInstalledApps(@[@"/Applications",@"/System/Applications"],deadline,^(NSDictionary *app){
                        dispatch_async(self.queue,^{[self call:@{@"op":@"baseline_add",@"apps":@[app]}];});
                    });
                } @catch (NSException *exception) {(void)exception;} @finally {
                    dispatch_async(self.queue,^{[self call:@{@"op":@"baseline_finish"}];[self persist];});
                }
            });
        }
        [self persist];return;
    }
    if ([op isEqual:@"rule_save"]) {
        [input removeObjectForKey:@"identifiedApp"];
        NSString *path=[request[@"input"] isKindOfClass:NSDictionary.class]?request[@"input"][@"appPath"]:nil;
        if ([path isKindOfClass:NSString.class]) {NSDictionary *app=FWIdentifyPath(path);if (!app) {completion(FWError(@"E_FW_RULE_INVALID"));return;}input[@"identifiedApp"]=app;}
    }
    if ([op isEqual:@"flush"]) {completion([self call:@{@"op":@"suspend"}]);return;}
    NSArray *allowed=@[@"snapshot",@"rules",@"rule_save",@"rule_delete",@"alerts",@"alert_reply",@"events",@"events_clear",@"settings",@"settings_set",@"blocklists",@"blocklist_set",@"blocklist_remove"];
    if (![allowed containsObject:op]) {completion(FWError(@"E_FW_RULE_INVALID"));return;}
    completion([self call:input]);
    if (![ @[@"snapshot",@"rules",@"alerts",@"events",@"settings",@"blocklists"] containsObject:op]) [self persist];
}
-(void)loadList:(NSDictionary *)request completion:(void (^)(NSDictionary *))completion {
    NSString *identifier=[request[@"id"] isKindOfClass:NSString.class]?request[@"id"]:nil;
    NSString *source=[request[@"source"] isKindOfClass:NSString.class]?request[@"source"]:nil;
    if (identifier) for (NSDictionary *list in FWArray([self call:@{@"op":@"blocklists"}][@"value"])) if ([list[@"id"] isEqual:identifier]) source=list[@"source"];
    if (!source.length) {completion(FWError(@"E_FW_BLOCKLIST_URL"));return;}
    NSString *key=identifier?:source;
    if ([self.refreshing containsObject:key]) {completion(FWError(@"E_FW_BLOCKLIST_FETCH"));return;}[self.refreshing addObject:key];
    void (^finish)(NSData *,NSString *)=^(NSData *data,NSString *error){
        dispatch_async(self.queue,^{
            [self.refreshing removeObject:key];
            NSString *text=data?[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]:nil;
            NSString *failure=error?:(!text?@"E_FW_BLOCKLIST_FORMAT":nil);
            NSDictionary *result;
            if (failure) result=FWError(failure);
            else result=[self call:@{@"op":@"blocklist_load",@"id":identifier?:NSNull.null,@"source":source,@"name":[NSURL URLWithString:source].host?:source.lastPathComponent,@"text":text}];
            if (identifier && result[@"error"]!=NSNull.null) [self call:@{@"op":@"blocklist_error",@"id":identifier,@"error":result[@"error"][@"code"]}];
            [self persist];completion(result);
        });
    };
    if ([source hasPrefix:@"https://"]) [FWListDownload fetch:source completion:finish];
    else if (source.isAbsolutePath && [request[@"text"] isKindOfClass:NSString.class]) {
        NSData *data=[request[@"text"] dataUsingEncoding:NSUTF8StringEncoding];finish(data.length<=FWMaxBytes?data:nil,data.length>FWMaxBytes?@"E_FW_BLOCKLIST_TOO_LARGE":nil);
    } else finish(nil,@"E_FW_BLOCKLIST_URL");
}
-(void)refreshDueLists {
    // Failed loads keep the last usable entries; retry at most once per 24 hours.
    static NSMutableDictionary *attempts; if (!attempts) attempts=[NSMutableDictionary dictionary];
    for (NSDictionary *list in FWArray([self call:@{@"op":@"blocklists"}][@"value"])) {
        if (![list[@"source"] hasPrefix:@"https://"] || ![list[@"enabled"] boolValue]) continue;
        int64_t updated=[list[@"updatedAt"] isKindOfClass:NSNumber.class]?[list[@"updatedAt"] longLongValue]:0;
        int64_t last=MAX(updated,[attempts[list[@"id"]] longLongValue]);
        if (FWNow()-last>=24*60*60*1000LL) {attempts[list[@"id"]]=@(FWNow());[self loadList:@{@"op":@"blocklist_refresh",@"id":list[@"id"]} completion:^(NSDictionary *result){(void)result;}];}
    }
}
@end


static FWService *FWSharedService(void) {
    static FWService *service;static dispatch_once_t once;
    dispatch_once(&once,^{service=[FWService new];});return service;
}
void FWStartService(void) {(void)FWSharedService();}

@implementation OpenNavoFilterDataProvider
-(void)startFilterWithCompletionHandler:(void (^)(NSError *))completion {
    FWService *service=FWSharedService();service.provider=self;
    dispatch_async(service.queue,^{
        service.filtering=YES;[service call:@{@"op":@"resume"}];
        // Adapted from LuLu: filter only outbound sockets; default verdict is allow.
        NENetworkRule *outbound=[[NENetworkRule alloc] initWithRemoteNetwork:nil remotePrefix:0 localNetwork:nil localPrefix:0 protocol:NENetworkRuleProtocolAny direction:NETrafficDirectionOutbound];
        NEFilterRule *rule=[[NEFilterRule alloc] initWithNetworkRule:outbound action:NEFilterActionFilterData];
        [self applySettings:[[NEFilterSettings alloc] initWithRules:@[rule] defaultAction:NEFilterActionAllow] completionHandler:completion];
    });
}
-(void)stopFilterWithReason:(NEProviderStopReason)reason completionHandler:(void (^)(void))completion {
    (void)reason;FWService *service=FWSharedService();service.filtering=NO;[service releaseAll];
    dispatch_async(service.queue,^{
        [service call:@{@"op":@"suspend"}];[service persist];completion();
        // Keep control IPC alive while filtering is off, so installed-extension
        // settings and rules remain owned/read by the extension (never a host cache).
    });
}
-(NEFilterNewFlowVerdict *)handleNewFlow:(NEFilterFlow *)flow {return [FWSharedService() handleNewFlow:flow];}
@end
