// OpenNavo-owned native boundary. Only this translation unit uses XPC audit tokens / SMAppService.
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <ServiceManagement/ServiceManagement.h>
#import <Security/Security.h>
#import <xpc/xpc.h>
#import <bsm/libbsm.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#import <IOKit/IOMessage.h>
#import <os/log.h>
#import <stdlib.h>
#import "../../native/firewall/FWIdentity.h"
extern void xpc_connection_get_audit_token(xpc_connection_t, audit_token_t *);
extern void xpc_dictionary_get_audit_token(xpc_object_t, audit_token_t *);
static const char *SERVICE="org.opennavo.desktop.FanHelper";
static SMAppService *service(void) {return [SMAppService daemonServiceWithPlistName:@"org.opennavo.desktop.FanHelper.plist"];}
int on_service_status(void) {@autoreleasepool {return (int)service().status;}}
// 0, or the SMAppService error code for the App's log (the first registration fails while approval is pending).
int on_service_change(int install) {@autoreleasepool {NSError *error=nil; BOOL done=install ? [service() registerAndReturnError:&error] : [service() unregisterAndReturnError:&error]; return done ? 0 : (error ? (int)error.code : -1);}}
void on_service_approval(void) {[SMAppService openSystemSettingsLoginItems];}
int on_thermal_serious(void) {return NSProcessInfo.processInfo.thermalState >= NSProcessInfoThermalStateSerious;}
// Get our actual signing team, never a user-supplied PID, pathname or Team ID. The running
// image's team is fixed until exec: FWOwnTeam reads it once instead of re-validating our own
// signature twice per heartbeat exchange (and per message in the helper). Peers are still
// verified from their audit token every time.
static NSString *ownTeam(void) {return FWOwnTeam();}
// Only a developer-signed OpenNavo.app carries a daemon it can register; `tauri dev` runs a bare, ad-hoc binary.
int on_helper_bundled(void) {
    @autoreleasepool {
        NSString *app=NSBundle.mainBundle.bundlePath;
        if (![app.pathExtension isEqualToString:@"app"] || !ownTeam().length) return 0;
        NSFileManager *files=NSFileManager.defaultManager;
        return [files fileExistsAtPath:[app stringByAppendingPathComponent:@"Contents/Library/LaunchDaemons/org.opennavo.desktop.FanHelper.plist"]]
            && [files isExecutableFileAtPath:[app stringByAppendingPathComponent:@"Contents/Library/Helpers/opennavo-fan-helper"]];
    }
}
// The root daemon has no log file; its messages go to the unified log (`log show --process opennavo-fan-helper`).
void on_helper_log(const char *message) {os_log(OS_LOG_DEFAULT,"%{public}s",message);}
static BOOL authorize(audit_token_t token, NSString *identifier) {
    NSString *team=ownTeam(); if (!team.length) return NO; // Ad-hoc signatures never enable production IPC.
    NSData *data=[NSData dataWithBytes:&token length:sizeof(token)];
    NSDictionary *attributes=@{(__bridge NSString *)kSecGuestAttributeAudit:data};
    SecCodeRef code=NULL; SecRequirementRef requirement=NULL;
    if (SecCodeCopyGuestWithAttributes(NULL,(__bridge CFDictionaryRef)attributes,kSecCSDefaultFlags,&code)!=errSecSuccess) return NO;
    NSString *rule=[NSString stringWithFormat:@"anchor apple generic and identifier \"%@\" and certificate leaf[subject.OU] = \"%@\"",identifier,team];
    OSStatus result=SecRequirementCreateWithString((__bridge CFStringRef)rule,kSecCSDefaultFlags,&requirement);
    if (result==errSecSuccess) result=SecCodeCheckValidity(code,kSecCSStrictValidate,requirement);
    if (requirement) CFRelease(requirement); CFRelease(code);return result==errSecSuccess;
}
char *on_xpc_exchange(const char *json) {
    @autoreleasepool {
        if (!json || strlen(json)>65536 || !ownTeam().length) return NULL;
        xpc_connection_t connection=xpc_connection_create_mach_service(SERVICE,NULL,XPC_CONNECTION_MACH_SERVICE_PRIVILEGED);
        if (!connection) return NULL;
        xpc_connection_set_event_handler(connection,^(xpc_object_t event){(void)event;}); xpc_connection_resume(connection);
        xpc_object_t message=xpc_dictionary_create(NULL,NULL,0);xpc_dictionary_set_string(message,"request",json);
        dispatch_semaphore_t done=dispatch_semaphore_create(0);
        __block NSString *output=nil;
        xpc_connection_send_message_with_reply(connection,message,dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^(xpc_object_t reply){
            if (xpc_get_type(reply)==XPC_TYPE_DICTIONARY) {
                audit_token_t token; xpc_dictionary_get_audit_token(reply,&token);
                const char *text=xpc_dictionary_get_string(reply,"response");
                if (text && strlen(text)<=65536 && authorize(token,@"org.opennavo.desktop.FanHelper")) output=[NSString stringWithUTF8String:text];
            }
            dispatch_semaphore_signal(done);
        });
        long result=dispatch_semaphore_wait(done,dispatch_time(DISPATCH_TIME_NOW,8*NSEC_PER_SEC));
        // Cancellation can race the reply. The block owns its captured state until it completes.
        xpc_connection_cancel(connection);
        return result==0 && output ? strdup(output.UTF8String) : NULL;
    }
}
void on_bridge_free(char *value) {free(value);}
typedef char *(*handler_t)(const char *,int);
static handler_t handler;
static void (^receive)(xpc_object_t);
// Each peer and each message is authorized from its audit token; no arbitrary SMC/file/process API.
void on_xpc_serve(handler_t callback) {
    handler=callback;
    xpc_connection_t listener=xpc_connection_create_mach_service(SERVICE,dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),XPC_CONNECTION_MACH_SERVICE_LISTENER);
    xpc_connection_set_event_handler(listener,^(xpc_object_t object){
        if (xpc_get_type(object)!=XPC_TYPE_CONNECTION) return;
        xpc_connection_t peer=(xpc_connection_t)object;
        audit_token_t token;xpc_connection_get_audit_token(peer,&token);
        if (!authorize(token,@"com.opennavo.desktop")) {xpc_connection_cancel(peer);return;}
        xpc_connection_set_event_handler(peer,^(xpc_object_t message){
            if (xpc_get_type(message)==XPC_TYPE_ERROR) {xpc_connection_cancel(peer);xpc_connection_set_event_handler(peer,^(xpc_object_t ignored){(void)ignored;});return;}
            if (xpc_get_type(message)!=XPC_TYPE_DICTIONARY) return;
            audit_token_t audit;xpc_dictionary_get_audit_token(message,&audit);
            const char *request=xpc_dictionary_get_string(message,"request");
            if (!request || strlen(request)>65536) return;
            BOOL allowed=authorize(audit,@"com.opennavo.desktop");
            char *response=handler(request,allowed);
            xpc_object_t reply=xpc_dictionary_create_reply(message);
            if (reply && response) {xpc_dictionary_set_string(reply,"response",response);xpc_connection_send_message(peer,reply);}
            if (response) free(response);
        });xpc_connection_resume(peer);
    });xpc_connection_resume(listener);
    // Retain the listener for daemon lifetime.
    receive=^(xpc_object_t unused){(void)unused;(void)listener;};
}
static io_connect_t powerPort;
static IONotificationPortRef powerNotifications;
static io_object_t powerNotifier;
static void (*powerCallback)(void);
static void powerMessage(void *context,io_service_t service,uint32_t type,void *argument) {
    (void)context;(void)service;
    if (type==kIOMessageSystemWillSleep || type==kIOMessageSystemWillPowerOff) powerCallback();
    if (type==kIOMessageSystemWillSleep || type==kIOMessageCanSystemSleep) IOAllowPowerChange(powerPort,(long)argument);
}
int on_helper_sleep_observer(void (*callback)(void)) {
    powerCallback=callback;
    // Kernel power notifications reach a root launch daemon without a GUI login session.
    powerPort=IORegisterForSystemPower(NULL,&powerNotifications,powerMessage,&powerNotifier);
    if (!powerPort || !powerNotifications) return -1;
    CFRunLoopSourceRef source=IONotificationPortGetRunLoopSource(powerNotifications);
    if (!source) return -1;
    CFRunLoopAddSource(CFRunLoopGetCurrent(),source,kCFRunLoopCommonModes);
    return 0;
}
void on_run_loop(void) {@autoreleasepool {[NSRunLoop.mainRunLoop run];}}
int on_helper_identity(void) {
    @autoreleasepool {
        NSString *team=ownTeam(); if (!team.length) return 0;
        SecCodeRef code=NULL; SecRequirementRef requirement=NULL;
        NSString *rule=[NSString stringWithFormat:@"anchor apple generic and identifier \"org.opennavo.desktop.FanHelper\" and certificate leaf[subject.OU] = \"%@\"",team];
        if (SecCodeCopySelf(kSecCSDefaultFlags,&code)!=errSecSuccess) return 0;
        OSStatus result=SecRequirementCreateWithString((__bridge CFStringRef)rule,kSecCSDefaultFlags,&requirement);
        if (result==errSecSuccess) result=SecCodeCheckValidity(code,kSecCSStrictValidate,requirement);
        if (requirement) CFRelease(requirement); CFRelease(code); return result==errSecSuccess;
    }
}
