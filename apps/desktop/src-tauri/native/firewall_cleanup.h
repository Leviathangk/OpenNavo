// SPDX-License-Identifier: GPL-3.0-only
#import <Foundation/Foundation.h>
#import <unistd.h>

static NSString *FWCleanupQuote(NSString *value) {
    return [NSString stringWithFormat:@"'%@'",[value stringByReplacingOccurrencesOfString:@"'" withString:@"'\\''"]];
}
// parents and owner are fixed by the caller, never supplied over IPC. Parameterized
// only so tests can exercise deletion in a private directory without elevation.
static NSString *FWCleanupCommand(NSArray<NSString *> *parents,NSString *identifier,uid_t owner) {
    NSCharacterSet *allowed=[NSCharacterSet characterSetWithCharactersInString:@"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-"];
    if (!identifier.length || [identifier isEqual:@"."] || [identifier isEqual:@".."]
        || [identifier rangeOfCharacterFromSet:allowed.invertedSet].location!=NSNotFound || !parents.count) return nil;
    NSString *target=[parents.lastObject stringByAppendingPathComponent:identifier];
    NSMutableString *command=[NSMutableString stringWithString:@"set -eu; "];
    // Fail closed on symlinks, foreign owners and writable ancestors. Never follow
    // a caller-controlled path to a privileged recursive deletion.
    for (NSString *path in [parents arrayByAddingObject:target]) {
        NSString *quoted=FWCleanupQuote(path);
        [command appendFormat:@"[ ! -L %@ ] || exit 1; if [ -e %@ ]; then [ -d %@ ] || exit 1; [ \"$(/usr/bin/stat -f %%u %@)\" = '%u' ] || exit 1; mode=$(/usr/bin/stat -f %%Lp %@); [ $((0$mode & 0022)) -eq 0 ] || exit 1; fi; ",quoted,quoted,quoted,quoted,owner,quoted];
    }
    [command appendFormat:@"/bin/rm -rf -- %@",FWCleanupQuote(target)];
    return command;
}

static BOOL FWCleanupData(NSString *identifier) {
    NSString *command=FWCleanupCommand(@[@"/Library",@"/Library/Application Support",@"/Library/Application Support/OpenNavo",@"/Library/Application Support/OpenNavo/Firewall"],identifier,0);
    if (!command) return NO;
    NSString *escaped=[[command stringByReplacingOccurrencesOfString:@"\\" withString:@"\\\\"] stringByReplacingOccurrencesOfString:@"\"" withString:@"\\\""];
    NSString *script=[NSString stringWithFormat:@"do shell script \"%@\" with administrator privileges",escaped];
    NSTask *task=[NSTask new];task.executableURL=[NSURL fileURLWithPath:@"/usr/bin/osascript"];
    task.arguments=@[@"-e",script];
    task.standardOutput=NSFileHandle.fileHandleWithNullDevice;
    task.standardError=NSFileHandle.fileHandleWithNullDevice;
    if (![task launchAndReturnError:nil]) return NO;
    [task waitUntilExit];
    return task.terminationReason==NSTaskTerminationReasonExit && task.terminationStatus==0;
}
