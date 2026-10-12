// Original OpenNavo read-only boundary. SDK interfaces are included, not vendored or transplanted.
#import <Foundation/Foundation.h>
#import <IOKit/IOKitLib.h>
#import <IOKit/IOCFPlugIn.h>
#import <IOKit/storage/ata/ATASMARTLib.h>
#import <IOKit/storage/nvme/NVMeSMARTLibExternal.h>
#include <dlfcn.h>
#include <sys/mount.h>
#include <math.h>

typedef CFDictionaryRef (*CopyChannels)(CFStringRef,CFStringRef,uint64_t,uint64_t,uint64_t);
typedef CFTypeRef (*Subscribe)(CFAllocatorRef,CFMutableDictionaryRef,CFMutableDictionaryRef *,uint64_t,CFTypeRef);
typedef CFDictionaryRef (*Samples)(CFTypeRef,CFMutableDictionaryRef,CFTypeRef);
typedef CFDictionaryRef (*Delta)(CFDictionaryRef,CFDictionaryRef,CFTypeRef);
typedef int (*Iterate)(CFDictionaryRef,int (^)(CFDictionaryRef));
typedef CFStringRef (*ChannelString)(CFDictionaryRef);
typedef int64_t (*Integer)(CFDictionaryRef,int);
typedef struct { void *library; CFTypeRef subscription; CFMutableDictionaryRef channels; CFDictionaryRef previous; Samples samples; Delta delta; Iterate iterate; ChannelString name,unit; Integer integer; } Power;
void *on_monitor_power_create(void) {
    void *lib=dlopen("/usr/lib/libIOReport.dylib",RTLD_LOCAL|RTLD_LAZY);
    if(!lib) return NULL;
    CopyChannels copy=dlsym(lib,"IOReportCopyChannelsInGroup");
    Subscribe subscribe=dlsym(lib,"IOReportCreateSubscription");
    Power *p=calloc(1,sizeof(*p)); if(!p) { dlclose(lib); return NULL; }
    p->library=lib; p->samples=dlsym(lib,"IOReportCreateSamples"); p->delta=dlsym(lib,"IOReportCreateSamplesDelta"); p->iterate=dlsym(lib,"IOReportIterate"); p->name=dlsym(lib,"IOReportChannelGetChannelName"); p->unit=dlsym(lib,"IOReportChannelGetUnitLabel"); p->integer=dlsym(lib,"IOReportSimpleGetIntegerValue");
    if(!copy||!subscribe||!p->samples||!p->delta||!p->iterate||!p->name||!p->unit||!p->integer) { dlclose(lib); free(p); return NULL; }
    CFDictionaryRef channels=copy(CFSTR("Energy Model"),NULL,0,0,0);
    if(channels) { p->subscription=subscribe(NULL,(CFMutableDictionaryRef)channels,&p->channels,0,NULL); CFRelease(channels); }
    if(!p->subscription||!p->channels) { if(p->subscription) CFRelease(p->subscription); if(p->channels) CFRelease(p->channels); dlclose(lib); free(p); return NULL; }
    return p;
}
void on_monitor_power_free(void *context) { Power *p=context; if(!p) return; if(p->previous) CFRelease(p->previous); CFRelease(p->channels); CFRelease(p->subscription); dlclose(p->library); free(p); }
void on_monitor_power_reset(void *context) { Power *p=context; if(p&&p->previous) { CFRelease(p->previous); p->previous=NULL; } }
char *on_monitor_power_read(void *context,double seconds) {
    @autoreleasepool {
        Power *p=context; if(!p) return NULL;
        CFDictionaryRef current=p->samples(p->subscription,p->channels,NULL); if(!current) { on_monitor_power_reset(p); return NULL; }
        CFDictionaryRef delta=p->previous&&seconds>0 ? p->delta(p->previous,current,NULL):NULL;
        if(p->previous) CFRelease(p->previous); p->previous=current;
        if(!delta) return NULL;
        NSMutableArray *out=[NSMutableArray array];
        p->iterate(delta,^int(CFDictionaryRef channel) {
            NSString *name=(__bridge NSString *)p->name(channel), *unit=(__bridge NSString *)p->unit(channel);
            if(name&&unit) [out addObject:@{@"name":name,@"unit":unit,@"energy":@(p->integer(channel,0))}];
            return 0;
        });
        CFRelease(delta);
        NSData *json=[NSJSONSerialization dataWithJSONObject:out options:0 error:nil];
        return json?strdup([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding].UTF8String):NULL;
    }
}
char *on_monitor_power_probe(void) {
    @autoreleasepool {
        Power *p=on_monitor_power_create(); if(!p) return NULL;
        NSMutableArray *out=[NSMutableArray array];
        CFDictionaryRef sample=p->samples(p->subscription,p->channels,NULL);
        if(!sample) { on_monitor_power_free(p); return NULL; }
        p->iterate(sample,^int(CFDictionaryRef channel) {
            NSString *name=(__bridge NSString *)p->name(channel), *unit=(__bridge NSString *)p->unit(channel);
            [out addObject:@{@"name":name?:@"",@"unit":unit?:@""}]; return 0;
        });
        CFRelease(sample); on_monitor_power_free(p);
        NSData *json=[NSJSONSerialization dataWithJSONObject:out options:0 error:nil];
        return json?strdup([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding].UTF8String):NULL;
    }
}
static NSDictionary *props(io_registry_entry_t entry) {
    NSMutableDictionary *out=[NSMutableDictionary dictionary];
    // Never copy complete NVMe driver dictionaries: they can contain large unrelated diagnostic blobs.
    for(NSString *key in @[@"Whole",@"BSD Name",@"NVMe SMART Capable",@"SMART Capable",@"Device Characteristics",@"Protocol Characteristics",@"Model",@"Virtual",@"Content",@"Role"]) {
        CFTypeRef value=IORegistryEntryCreateCFProperty(entry,(__bridge CFStringRef)key,kCFAllocatorDefault,0);
        if(value) out[key]=CFBridgingRelease(value);
    }
    return out;
}
static NSString *string(id value) { return [value isKindOfClass:NSString.class]?value:nil; }
// Inspect only bounded ancestry and selected properties; no diskutil or disk-image attachment.
static void inspect_node(NSDictionary *p,NSString *cls,BOOL storage,NSMutableDictionary *out) {
    id protocol=p[@"Protocol Characteristics"];
    if(![protocol isKindOfClass:NSDictionary.class]) protocol=@{};
    NSString *kind=string(protocol[@"Physical Interconnect"]);
    NSString *lower=cls.lowercaseString;
    if([p[@"Virtual"] boolValue] || [lower containsString:@"iohdix"] || [lower containsString:@"diskimage"]
       || [kind.lowercaseString containsString:@"virtual"] || [kind.lowercaseString containsString:@"disk image"])
        out[@"virtual"]=@YES;
    if([p[@"NVMe SMART Capable"] boolValue] || [p[@"SMART Capable"] boolValue] || storage
       || [@[@"usb",@"sata",@"ata",@"pci-express",@"pci",@"firewire",@"secure digital"] containsObject:kind.lowercaseString])
        out[@"physical"]=@YES;
    id roles=p[@"Role"];
    if([roles isKindOfClass:NSString.class]) roles=@[roles];
    if([roles isKindOfClass:NSArray.class]) for(id role in roles) if(string(role)&&![out[@"roles"] containsObject:role]) [out[@"roles"] addObject:role];
}
static NSDictionary *media_info(io_registry_entry_t media) {
    NSMutableDictionary *out=[@{@"physical":@NO,@"virtual":@NO,@"roles":[NSMutableArray array]} mutableCopy];
    io_registry_entry_t node=media; IOObjectRetain(node);
    for(int n=0;n<64;n++) {
        io_name_t cls={0}; IOObjectGetClass(node,cls);
        inspect_node(props(node),@(cls),IOObjectConformsTo(node,"IOBlockStorageDevice"),out);
        io_registry_entry_t next=0;
        if(IORegistryEntryGetParentEntry(node,kIOServicePlane,&next)!=KERN_SUCCESS) break;
        IOObjectRelease(node);node=next;
    }
    IOObjectRelease(node);out[@"physical"]=[out[@"physical"] boolValue]&&![out[@"virtual"] boolValue]?@YES:@NO;
    return out;
}
static BOOL visible_mount(NSString *path,NSArray *roles) {
    if([path isEqualToString:@"/"]) return YES;
    if(![path hasPrefix:@"/Volumes/"] || [path hasPrefix:@"/Volumes/."]) return NO;
    for(NSString *role in roles) if([@[@"recovery",@"preboot",@"vm",@"update",@"xart",@"hardware"] containsObject:role.lowercaseString]) return NO;
    return YES;
}
static char *json_string(id value) {
    NSData *json=[NSJSONSerialization dataWithJSONObject:value options:0 error:nil];
    return json?strdup([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding].UTF8String):NULL;
}
char *on_monitor_media_info(const char *bsd) {
    @autoreleasepool {
        io_service_t service=IOServiceGetMatchingService(kIOMainPortDefault,IOBSDNameMatching(kIOMainPortDefault,0,bsd));
        if(!service) return NULL;
        NSDictionary *out=media_info(service);IOObjectRelease(service);return json_string(out);
    }
}
// Pure fixture boundary: exercises the same ancestor/role policy without touching hardware.
char *on_monitor_media_fixture(const char *json) {
    @autoreleasepool {
        id nodes=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:json length:strlen(json)] options:0 error:nil];
        if(![nodes isKindOfClass:NSArray.class] || [nodes count]>64) return NULL;
        NSMutableDictionary *out=[@{@"physical":@NO,@"virtual":@NO,@"roles":[NSMutableArray array]} mutableCopy];
        for(NSDictionary *node in nodes) {
            if(![node isKindOfClass:NSDictionary.class]) return NULL;
            inspect_node(node,string(node[@"class"])?:@"",[node[@"storage"] boolValue],out);
        }
        out[@"physical"]=[out[@"physical"] boolValue]&&![out[@"virtual"] boolValue]?@YES:@NO;return json_string(out);
    }
}
int on_monitor_mount_fixture(const char *path,const char *json) {
    @autoreleasepool {
        id roles=[NSJSONSerialization JSONObjectWithData:[NSData dataWithBytes:json length:strlen(json)] options:0 error:nil];
        return [roles isKindOfClass:NSArray.class]&&visible_mount(@(path),roles);
    }
}
// Only SMARTReadData / SMARTReturnStatus are called. No enable, autosave, test, reset or writes.
static NSDictionary *smart(io_service_t service,BOOL nvme) {
    IOCFPlugInInterface **plugin=NULL; SInt32 score=0; NSMutableDictionary *out=[NSMutableDictionary dictionary];
    CFUUIDRef type=nvme?kIONVMeSMARTUserClientTypeID:kIOATASMARTUserClientTypeID;
    if(IOCreatePlugInInterfaceForService(service,type,kIOCFPlugInInterfaceID,&plugin,&score)!=kIOReturnSuccess||!plugin) return out;
    if(nvme) {
        IONVMeSMARTInterface **iface=NULL;
        HRESULT result=(*plugin)->QueryInterface(plugin,CFUUIDGetUUIDBytes(kIONVMeSMARTInterfaceID),(LPVOID *)&iface);
        if(result==S_OK&&iface) {
            NVMeSMARTData data={0};
            if((*iface)->SMARTReadData(iface,&data)==kIOReturnSuccess) {
                out[@"smart"]=data.CRITICAL_WARNING?@"failing":@"verified";
                out[@"percentUsed"]=@(fmin(1.0,data.PERCENTAGE_USED/100.0));
                uint16_t kelvin=CFSwapInt16LittleToHost(data.TEMPERATURE);
                if(kelvin>=200&&kelvin<=500) out[@"temperatureC"]=@(kelvin-273.15);
                if(data.POWER_ON_HOURS[1]==0) out[@"powerOnHours"]=@(CFSwapInt64LittleToHost(data.POWER_ON_HOURS[0]));
                uint64_t units=CFSwapInt64LittleToHost(data.DATA_UNITS_WRITTEN[0]);
                if(data.DATA_UNITS_WRITTEN[1]==0 && units<=UINT64_MAX/512000) out[@"dataWrittenBytes"]=@(units*512000);
            }
            (*iface)->Release(iface);
        }
    } else {
        IOATASMARTInterface **iface=NULL;
        HRESULT result=(*plugin)->QueryInterface(plugin,CFUUIDGetUUIDBytes(kIOATASMARTInterfaceID),(LPVOID *)&iface);
        if(result==S_OK&&iface) { Boolean failing=false; if((*iface)->SMARTReturnStatus(iface,&failing)==kIOReturnSuccess) out[@"smart"]=failing?@"failing":@"verified"; (*iface)->Release(iface); }
    }
    IODestroyPlugInInterface(plugin); return out;
}
char *on_monitor_smart_read(void) {
    @autoreleasepool {
        NSMutableArray *devices=[NSMutableArray array]; io_iterator_t it=0;
        if(IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOMedia"),&it)!=KERN_SUCCESS) return NULL;
        io_service_t media;
        while((media=IOIteratorNext(it))) {
            NSDictionary *m=props(media);
            if(![m[@"Whole"] boolValue] || !string(m[@"BSD Name"]) || IOObjectConformsTo(media,"AppleAPFSMedia")) { IOObjectRelease(media); continue; }
            NSDictionary *meta=media_info(media);
            if(![meta[@"physical"] boolValue]) { IOObjectRelease(media); continue; }
            io_registry_entry_t parent=media; IOObjectRetain(parent); NSMutableDictionary *read=[NSMutableDictionary dictionary]; NSDictionary *details=@{}; uint64_t deviceID=0;
            for(int n=0;n<32;n++) {
                NSDictionary *p=props(parent);
                BOOL nvme=[p[@"NVMe SMART Capable"] boolValue], ata=[p[@"SMART Capable"] boolValue];
                if(nvme||ata) {
                    IORegistryEntryGetRegistryEntryID(parent,&deviceID); details=p;
                    BOOL known=NO; for(NSDictionary *d in devices) if([d[@"id"] isEqualToString:[NSString stringWithFormat:@"ioreg-%llu",(unsigned long long)deviceID]]) known=YES;
                    if(!known) read=[smart(parent,nvme) mutableCopy]; break;
                }
                io_registry_entry_t next=0; if(IORegistryEntryGetParentEntry(parent,kIOServicePlane,&next)!=KERN_SUCCESS) break;
                IOObjectRelease(parent); parent=next;
            }
            // APFS synthesized whole media are not physical devices; their controller has no SMART capability.
            NSDictionary *characteristics=details[@"Device Characteristics"];
            NSString *name=string(characteristics[@"Product Name"])?:string(details[@"Model"])?:string(m[@"BSD Name"]);
            NSDictionary *protocol=details[@"Protocol Characteristics"];
            BOOL internal=[string(protocol[@"Physical Interconnect Location"]) isEqualToString:@"Internal"];
            if(deviceID==0) { IORegistryEntryGetRegistryEntryID(media,&deviceID); if([m[@"Virtual"] boolValue] || [string(m[@"Content"]) hasPrefix:@"Apple_APFS"]) { IOObjectRelease(parent); IOObjectRelease(media); continue; } }
            NSString *identity=[NSString stringWithFormat:@"ioreg-%llu",(unsigned long long)deviceID];
            BOOL duplicate=NO; for(NSDictionary *d in devices) if([d[@"id"] isEqualToString:identity]) duplicate=YES;
            if(!duplicate) {
                NSMutableArray *mounts=[NSMutableArray array]; struct statfs *fs=NULL; int count=getmntinfo(&fs,MNT_NOWAIT);
                for(int j=0;j<count;j++) { const char *bsd=fs[j].f_mntfromname; if(strncmp(bsd,"/dev/",5)!=0) continue; io_service_t mounted=IOServiceGetMatchingService(kIOMainPortDefault,IOBSDNameMatching(kIOMainPortDefault,0,bsd+5)); if(!mounted) continue;
                    NSDictionary *mountMeta=media_info(mounted); io_registry_entry_t e=mounted; for(int n=0;n<64;n++) { uint64_t id=0; IORegistryEntryGetRegistryEntryID(e,&id); if(id==deviceID) { NSString *path=@(fs[j].f_mntonname); if(visible_mount(path,mountMeta[@"roles"])&&![mounts containsObject:path]) [mounts addObject:path]; break; } io_registry_entry_t next=0; if(IORegistryEntryGetParentEntry(e,kIOServicePlane,&next)!=KERN_SUCCESS) break; IOObjectRelease(e); e=next; } IOObjectRelease(e);
                }
                NSMutableDictionary *d=[@{@"id":identity,@"name":name,@"internal":@(internal),@"smart":read[@"smart"]?:@"unsupported",@"percentUsed":NSNull.null,@"temperatureC":NSNull.null,@"powerOnHours":NSNull.null,@"dataWrittenBytes":NSNull.null,@"mountPoints":mounts} mutableCopy]; [d addEntriesFromDictionary:read]; [devices addObject:d];
            }
            IOObjectRelease(parent); IOObjectRelease(media);
        }
        IOObjectRelease(it); NSData *json=[NSJSONSerialization dataWithJSONObject:devices options:0 error:nil]; return json?strdup([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding].UTF8String):NULL;
    }
}
char *on_monitor_format(const char *locale,double value,uint32_t kind) {
    @autoreleasepool {
        NSLocale *loc=[[NSLocale alloc] initWithLocaleIdentifier:@(locale)]; NSNumberFormatter *f=[NSNumberFormatter new]; f.locale=loc; f.numberStyle=kind==1?NSNumberFormatterPercentStyle:NSNumberFormatterDecimalStyle; f.maximumFractionDigits=0;
        NSString *out;
        if(kind==0) { NSMeasurementFormatter *m=[NSMeasurementFormatter new]; m.locale=loc; m.unitStyle=NSFormattingUnitStyleShort; m.unitOptions=NSMeasurementFormatterUnitOptionsProvidedUnit; m.numberFormatter=f; double n=fmax(0,value); NSUnitInformationStorage *unit=NSUnitInformationStorage.bytes; if(n>=1e9) { n/=1e9; unit=NSUnitInformationStorage.gigabytes; } else if(n>=1e6) { n/=1e6; unit=NSUnitInformationStorage.megabytes; } else if(n>=1e3) { n/=1e3; unit=NSUnitInformationStorage.kilobytes; } f.maximumFractionDigits=1; out=[m stringFromMeasurement:[[NSMeasurement alloc] initWithDoubleValue:n unit:unit]]; }
        else if(kind==1) out=[f stringFromNumber:@(value)];
        else out=[[f stringFromNumber:@(value)] stringByAppendingString:@"\u00a0°C"];
        return out?strdup(out.UTF8String):NULL;
    }
}
