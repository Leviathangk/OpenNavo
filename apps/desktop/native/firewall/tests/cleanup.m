// SPDX-License-Identifier: GPL-3.0-only
// Exercise only the shell command against private fixtures, never request elevation.
#import <assert.h>
#import <sys/stat.h>
#import "../../../src-tauri/native/firewall_cleanup.h"
static BOOL Run(NSString *parent,NSString *identifier) {
    NSString *command=FWCleanupCommand(@[parent],identifier,geteuid());
    if (!command) return NO;
    NSTask *task=[NSTask new];task.executableURL=[NSURL fileURLWithPath:@"/bin/sh"];
    task.arguments=@[@"-c",command];assert([task launchAndReturnError:nil]);[task waitUntilExit];
    return task.terminationStatus==0;
}
int main(void) { @autoreleasepool {
    (void)FWCleanupData;
    NSFileManager *fm=NSFileManager.defaultManager;
    NSString *root=[NSTemporaryDirectory() stringByAppendingPathComponent:NSUUID.UUID.UUIDString];
    // A quoted path verifies shell escaping too.
    NSString *parent=[root stringByAppendingPathComponent:@"private ' data"];
    NSString *target=[parent stringByAppendingPathComponent:@"org.example.firewall"];
    NSString *sibling=[parent stringByAppendingPathComponent:@"keep"];
    assert([fm createDirectoryAtPath:target withIntermediateDirectories:YES attributes:@{NSFilePosixPermissions:@0700} error:nil]);
    assert([@"keep" writeToFile:sibling atomically:YES encoding:NSUTF8StringEncoding error:nil]);
    for (NSString *name in @[@"state.json",@"state.json.unreadable-backup"]) {
        assert([@"private" writeToFile:[target stringByAppendingPathComponent:name] atomically:YES encoding:NSUTF8StringEncoding error:nil]);
    }
    assert(Run(parent,@"org.example.firewall"));assert(![fm fileExistsAtPath:target]);
    assert([fm fileExistsAtPath:sibling]);assert(Run(parent,@"org.example.firewall"));
    assert(!Run(parent,@"../keep"));assert(!Run(parent,@".."));
    assert([fm createSymbolicLinkAtPath:target withDestinationPath:root error:nil]);
    assert(!Run(parent,@"org.example.firewall"));assert([fm fileExistsAtPath:sibling]);
    assert([fm removeItemAtPath:target error:nil]);
    NSString *link=[root stringByAppendingPathComponent:@"link"];
    assert([fm createSymbolicLinkAtPath:link withDestinationPath:parent error:nil]);
    assert(!Run(link,@"org.example.firewall"));
    assert(chmod(parent.fileSystemRepresentation,0777)==0);assert(!Run(parent,@"org.example.firewall"));
    assert(chmod(parent.fileSystemRepresentation,0700)==0);
    assert([fm removeItemAtPath:root error:nil]);
    puts("Cleanup tests passed without elevation.");return 0;
} }
