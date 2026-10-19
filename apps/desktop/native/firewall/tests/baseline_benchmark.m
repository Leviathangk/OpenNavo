// SPDX-License-Identifier: GPL-3.0-only
// Read-only host benchmark. Does not load, register, or contact an extension.
#import "../FWIdentity.h"
#include <stdio.h>

int main(void) { @autoreleasepool {
    for (int pass=0;pass<2;pass++) {
        uint64_t start=FWMonotonic();__block NSUInteger count=0,invalid=0;
        FWScanInstalledApps(@[@"/Applications",@"/System/Applications"],start+60000,^(NSDictionary *app){
            count++;if ([app[@"signer"] isEqual:@"invalid"]) invalid++;
        });
        printf("%s apps=%lu invalid=%lu elapsed_ms=%llu\n",pass?"warm":"cold",
            (unsigned long)count,(unsigned long)invalid,(unsigned long long)(FWMonotonic()-start));
    }
    return 0;
} }
