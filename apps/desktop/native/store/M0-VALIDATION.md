# M0 validation

The source port is pinned in `upstream.json`. No Objective-See code is included.
The helper has no root mode, installer invocation, receipt copying, or app
registration. M2 will implement the single `admin_batch::mas_install_pkg` action.

Safe local checks (no purchase, no download, no installed app changes):

```sh
swift test --package-path apps/desktop/native/store
python3 apps/desktop/scripts/prepare-native-helpers.py --work /tmp/on-native-build --out /tmp/on-native-stage
/tmp/on-native-stage/opennavo-store probe --real-read-only
codesign --verify --strict /tmp/on-native-stage/opennavo-store
```

The staging script uses `APPLE_SIGNING_IDENTITY`. `--allow-unsigned` instead
applies an ad-hoc signature for build tests. Notarization is a separate explicit
step with `notarize-native-helpers.py`; no credentials are created by these scripts.

## Pending real installation test — Claude must obtain user consent first

Do not run the following during autonomous development. Even starting a download
can install on older systems; it is a real App Store write, not a read-only probe.

1. User is present and signed into App Store. Choose an **already owned free Mac
   app**; confirm its Adam ID with the user. Record the old app version and path.
2. Create a fresh user-owned 0700 staging directory on the same volume as Apple's
   download cache. Keep stdin open while running:
   `opennavo-store install --adam-ids ADAM_ID --staging STAGING_DIRECTORY`.
3. Capture JSONL and the exit code. Closing stdin cancels the active operation.
   A 201 result must emit `needsPrivilegedInstall` with an existing hard-linked
   package and receipt; it must not invoke root itself. If Apple's error-201
   callback has no `installPath`, this M0 version fails closed with the original
   Apple domain/code. That case needs a verified package destination resolver
   before M2 can enable installation.
4. Read-only package evidence: `pkgutil --check-signature PACKAGE`. Save the Apple
   chain and package metadata for the M2 verifier fixture. A human-readable
   substring match for “Apple” is insufficient for the privileged security gate.
5. **Do not bypass the fixed-action requirement with manual sudo/installer.**
   Once M2's `mas_install_pkg` action passes its temporary-directory/object-change
   tests, Claude schedules the authorized end-to-end run: Apple signature gate,
   `Snapshot::same_object`, installer, receipt owned by root with mode 0644,
   metadata import and Launch Services registration. Verify App Store says Open.

M0 currently validates compilation, signing, protocol and read-only compatibility.
A successful probe is not evidence that a purchase, download, error-201 recovery
or notarization has succeeded.
