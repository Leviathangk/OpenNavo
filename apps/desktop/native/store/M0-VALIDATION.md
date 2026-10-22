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
   package and receipt; it must not invoke root itself. If no callback ever
   carried an `installPath`, the helper fails closed with the original Apple
   domain/code.
   - Observed on macOS 27 (2026-10-22, a free update that was already owned):
     installd rejects storedownloadd's connection, so the install fails with
     `PKInstallErrorDomain` 201 ("无法开始安装。"). Two failed-status callbacks carry
     `installPath`; the removal callback that follows reports `nil`. The helper
     therefore remembers the last `.app` path it was given (`ObservedInstallPath`).
     Before that, the update failed with this raw Apple message and no password
     dialog, because the privileged request was never built.
   - The staged files are the package and a `receipt`, both hard-linked from
     Apple's download folder before it is removed.
4. Read-only package evidence: `pkgutil --check-signature PACKAGE`. Save the Apple
   chain and package metadata for the M2 verifier fixture. A human-readable
   substring match for “Apple” is insufficient for the privileged security gate.
   - The real package is a distribution archive: `Distribution`, `Resources/`
     and one component directory (`Bom`, `PackageInfo`, `Payload`), signed as
     "signed by Apple for the App Store" by "Apple Mac OS Installer Package
     Signing" under the Worldwide Developer Relations CA. The M2 gates in
     `updater/admin.rs` (`apple_package_signature`, `validate_store_package`)
     match that shape; `tests/store_install.rs` carries the signature text.
5. **Do not bypass the fixed-action requirement with manual sudo/installer.**
   Once M2's `mas_install_pkg` action passes its temporary-directory/object-change
   tests, Claude schedules the authorized end-to-end run: Apple signature gate,
   `Snapshot::same_object`, installer, receipt owned by root with mode 0644,
   metadata import and Launch Services registration. Verify App Store says Open.

M0 currently validates compilation, signing, protocol and read-only compatibility.
A successful probe is not evidence that a purchase, download, error-201 recovery
or notarization has succeeded.
