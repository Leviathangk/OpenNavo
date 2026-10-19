# Application firewall backend

This is OpenNavo's macOS outbound application firewall. The Network Extension
bootstrap, outbound filter setup, paused-flow handling and Apple tool graylist
are adapted from Objective-See / Patrick Wardle's LuLu v4.5.1, GPL-3.0-only.
OpenNavo modifications dated **2026-10-18** are recorded in `upstream.json`.
`LICENSE.LuLu` retains the upstream license verbatim. No upstream UI, images,
installer, build scripts or binaries are included or executed.

The Objective-C provider wraps an independently testable Rust policy library.
The host and extension share the Rust wire types. A persistent, authenticated XPC
connection carries bounded JSON requests; both sides validate the sender's audit
token against the expected bundle identifier and their own runtime signing team.
The control service lives independently of active filtering, including when the
filter is off. The extension owns settings, rules, the installed-app baseline,
blocklists and the 1,000-entry / seven-day connection ring.

State lives exclusively at
`/Library/Application Support/OpenNavo/Firewall/<extension-bundle-id>/state.json`.
Its directory is private to the extension user and writes are atomic. State is
saved on mutations and every 30 seconds (the latest activity/statistics can be
lost on a crash). No extension state is stored in a user's home directory.
Corrupt/unwritable state and internal errors cause connections to be allowed;
they never create blocking rules. The client reports an unavailable connection.

Rule precedence is app+destination, global+destination, blocklist, app+any,
automatic Apple/installed allowance, then ask/allow/block mode. At equal
specificity, block wins. Host+port outranks host, which outranks port. Saving an
identical app+destination+port replaces that rule and keeps its ID. A global rule
must specify a destination or port. Signed identities use team+signing ID;
ad-hoc/unsigned identities use executable path. Same-team helpers are attributed
to their outer app. Temporary answers bind PID and process-start generation and
are never restored after an extension restart.

## Failure and hostname boundaries

New-flow callbacks return immediately; identity and policy run asynchronously.
An independent watchdog releases a flow if policy evaluation has not finished
within one second, the client heartbeat is missing for five seconds, or a prompt
expires (TCP: 120 seconds; UDP: five seconds). The core expires pending flows
before every RPC. Timed-out connections record `timeout` and pass, while their
questions remain answerable and can still create rules. A missing client records
`no_client`; internal protective bypasses do not masquerade as user timeouts.
Disconnect and disable release all waiting flows and clear unanswered questions. The pending queue is bounded to
4,096 flows; excess flows pass immediately. Normal rule-allowed traffic is not
recorded. Incoming traffic and non-TCP/UDP socket flows pass unchanged.

Apple documents that TCP pauses may last indefinitely, but UDP pauses exceeding
ten seconds are dropped by the OS. The shorter deadlines here deliberately allow
unanswered connections. This is not a strict fail-closed security boundary.
The OS and remote peer may have their own shorter connection timeouts.

Domain entries require a hostname supplied by `remoteHostname` or the flow URL.
There is no DNS interception, reverse lookup, TLS inspection, or domain-to-shared-IP
expansion. Connections with no hostname can match only IP/CIDR/port/app rules;
a domain blocklist cannot reliably stop them. Wildcards match subdomains, not the
apex. Input domains use ASCII/punycode, case-insensitive, with a trailing dot
removed. HTTPS blocklists refresh every 24 hours; failures retain the last good
entries. Local files are read by the host only when explicitly added/refreshed.
Each list is limited to 50 MiB and one million input entries, with at most 32 lists.

References:

- [Apple pause verdict](https://developer.apple.com/documentation/networkextension/nefilternewflowverdict/pause%28%29)
- [Apple macOS App Groups](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.application-groups)
- [Upstream v4.5.1](https://github.com/objective-see/LuLu/tree/v4.5.1)

## Build and checks

Without both profiles and a signing identity, `scripts/prepare-firewall.py`
skips extension compilation entirely and clears only the generated empty staging
directory. Ordinary development builds retain their existing entitlements.
Neither compilation mode installs, runs, opens, registers or activates a bundle.
Direct clang/Cargo compilation avoids Xcode's LaunchServices registration step.

```sh
cargo test --locked --manifest-path apps/desktop/native/firewall/core/Cargo.toml
cargo clippy --locked --manifest-path apps/desktop/native/firewall/core/Cargo.toml --all-targets -- -D warnings
python3 -m unittest discover -s apps/desktop/scripts -p test_prepare_firewall.py
python3 apps/desktop/scripts/prepare-firewall.py --compile-only apps/desktop/src-tauri/target/firewall-link-check --target universal-apple-darwin
cargo run --manifest-path apps/desktop/src-tauri/Cargo.toml --example firewall_probe
cargo run --manifest-path apps/desktop/src-tauri/Cargo.toml --example firewall_probe -- --bundle "/path/to/OpenNavo.app"
python3 apps/desktop/scripts/prepare-firewall.py --verify-bundle-services "/path/to/OpenNavo.app"
```

`--compile-only` produces a plain executable for link validation. **Do not run it.**
`firewall_probe` runs only the host's read-only `not_bundled` status branch; it
creates no Tauri application, database, content filter or extension request. With
`--bundle`, it reads the embedded extension Info.plist and checks the versioned
Mach service name instead; it never connects to XPC. The final build verifier also
checks the prefix against the signed extension App Group entitlement.

Signed builds require these external inputs:

- `APPLE_SIGNING_IDENTITY`: installed Developer ID Application identity name or SHA-1 fingerprint.
- `OPENNAVO_FIREWALL_APP_PROFILE`: absolute path to the host Developer ID profile.
- `OPENNAVO_FIREWALL_EXTENSION_PROFILE`: absolute path to the extension Developer ID profile.
- Alternatively, the two profile variables suffixed `_B64` contain their base64
  bytes. CI uses secrets `OPENNAVO_FIREWALL_APP_PROFILE_B64` and
  `OPENNAVO_FIREWALL_EXTENSION_PROFILE_B64`. Never write profiles into this repo.
- `OPENNAVO_FIREWALL_STAGE`: private build directory outside the repository.
- `OPENNAVO_FIREWALL_APP_ID`: host ID, default `com.opennavo.desktop`; extension ID
  is `<host-id>.firewall`. Profiles must match those exact identifiers.
- `VERSION`: matching app/extension short version.
- `OPENNAVO_FIREWALL_BUNDLE_VERSION` (or `--bundle-version`): optional explicit
  CFBundleVersion for both bundles, defaulting to the numeric VERSION prefix.
  Every distributed extension update must use a new CFBundleVersion, including
  prereleases sharing a short-version numeric prefix; reusing it also reuses the
  Mach service. Keep it compatible with Apple bundle version requirements.
- `OPENNAVO_FIREWALL_CONFIG`: path of the prepared Tauri overlay, also passed with
  `tauri build --config`. The bundle hook validates prepared inputs and signatures.

Both profiles need Network Extensions (`content-filter-provider-systemextension`);
the host additionally needs System Extension installation. Profiles must be
Developer ID distribution profiles for all devices, unexpired, and include the
selected signing certificate. Team is derived from the validated profiles; it is
never hardcoded. Both signatures get App Group `<TEAM>.<host-id>`, with Mach service
`<TEAM>.<host-id>.firewall.<CFBundleVersion>`. The preparer rejects build-version
characters outside `[0-9A-Za-z.-]`, checks the exact service/group/version mapping
in the bundle hook, and release CI checks the final signed App. Both the host
connection and extension listener read this name from the extension Info.plist.
New versions use distinct launchd endpoints even if old tasks have not exited.
Apple permits these macOS team-prefixed groups without
a separately registered portal App Group; do not substitute an iOS-style `group.`
identifier without changing the build configuration deliberately.

The preparer signs the nested `.systemextension` using its own entitlements, and
verifies it with `codesign --verify --deep --strict`. The overlay embeds it at
`Contents/Library/SystemExtensions/<extension-id>.systemextension`, embeds the
host profile and merges the host's entitlements. Tauri signs/notarizes the outer
app. Release CI verifies the whole app and staples/notarizes through the existing
release workflow. Missing optional profiles do not fail release. Invalid supplied
profiles do fail preparation, instead of distributing an unusable signed feature.

For device validation, use separate host ID `com.opennavo.desktop.dev`,
extension ID `com.opennavo.desktop.dev.firewall`, matching profiles, and
product name `OpenNavo Firewall Test` (the overlay changes the name and deep-link
scheme automatically and disables production updater endpoints). This separates app data, extension state and bundle paths
from the installed production app. It costs two extra App IDs/profiles and a new
pair of system approvals. Other native helpers retain their existing identities;
do not enable or install those unrelated features during this firewall test.
Installing either app or approving filtering requires explicit user authorization.

## Missing service reporting

When properties confirm that the bundled version is enabled and filtering is on,
15 seconds of continuous fast XPC invalid responses report `E_FW_DISCONNECTED`
with detail `firewall_service_missing`: the service did not start correctly;
restart the Mac to restore it, and connections remain allowed until then.
Detection requires at least three snapshot failures within 250 ms each; gaps
longer than three seconds, ordinary timeouts, failed peer verification and stale
or explicitly cancelled requests do not accumulate. A successful authenticated
snapshot clears the diagnostic. Turning filtering off and on resets detection.

The detector never activates the extension or removes/recreates NE preferences.
Same-version activation does not replace the installed task in normal mode, and
real-device testing showed that preference recreation reused the task without
restoring its missing endpoint. There is no service-repair retry action.
