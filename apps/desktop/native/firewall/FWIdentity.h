// SPDX-License-Identifier: GPL-3.0-only
#import <Foundation/Foundation.h>
#import <bsm/libbsm.h>
NSString *FWOwnTeam(void);
BOOL FWAuthorize(audit_token_t token, NSString *identifier);
BOOL FWAuthorizeWithDetail(audit_token_t token, NSString *identifier, NSString **detail);
NSDictionary *FWIdentifyPath(NSString *path);
NSDictionary *FWIdentifyToken(NSData *token);
uint64_t FWProcessStart(pid_t pid);
NSArray *FWArguments(pid_t pid);
NSArray *FWAncestors(pid_t pid);
NSDictionary *FWProcessDetails(NSString *verdict,pid_t pid,uint64_t start);
NSArray *FWInstalledApps(void);
// One synchronous walk, invoked on a dedicated worker. Deadline is monotonic ms.
void FWScanInstalledApps(NSArray<NSString *> *roots,uint64_t deadline,void (^found)(NSDictionary *));
uint64_t FWMonotonic(void);
int64_t FWNow(void);
int64_t FWDayStart(void);
