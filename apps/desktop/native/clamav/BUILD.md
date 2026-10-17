# Standalone ClamAV build

Run `build.sh --work /absolute/scratch/directory --source-bundle` on macOS with
CMake 4.1.2, Ninja 1.13.0, Xcode and Rust 1.96.0 (arm64 and x86_64 targets).
The script creates per-architecture static dependencies and merges only the
three final executables with lipo. Deployment target is 13.0. It never installs
into Homebrew, `/usr/local` or an application bundle, and never downloads a
signature database. `sources.lock.json` pins the complete original archives.

Only clamd, freshclam and clamscan are built. Only Apple system libraries may
remain dynamically linked; the script checks `otool -L` and `vtool` on every
output. UnRAR is OFF. The small CMake patch exposes its interface declarations
to static-library consumers because `others.h` includes that header even when
UnRAR is disabled; it does not build or link the UnRAR implementation.

Rust's release build-time proc-macro stripping is disabled to avoid macOS 27
dyld failures. curl's `HAVE_PIPE2` detection is overridden because SDK 27 declares
pipe2 even though macOS 13 does not provide it.

Sign only the standalone executables using `prepare-native-helpers.py` and the
same Developer ID as OpenNavo. Never link the generated libraries into Rust.
For M0 notarization use `notarize-native-helpers.py`; the manual CI workflow
uploads the binaries and corresponding-source archive together. Production
release integration is implemented in release-desktop.yml and
prepare-security-helpers.py. The release asset builder requires the matching
source archive in the same Release, not just in CI.

The source archive includes original ClamAV sources (including its exact offline
Cargo vendor tree), all seven C dependency archives, the source lock, patches,
and these build scripts. Its inputs and original license texts are registered
in `docs/legal/native/manifest.json`. There are no patches to detection logic.
`keeptmp-unstorable-names.patch` only changes temporary file naming: with
LeaveTemporaryFiles, ClamAV names extracted ZIP members and per-file scan
directories after the original names, which APFS refuses when they are not
UTF-8 (common in archives made on Chinese Windows). Those now fall back to a
random name, as upstream `cli_gentempfd_with_prefix()` already does, instead of
leaving the archive unscanned.

M0 cached database measurements use `examples/clamav_probe.rs`. It sets both
CVDCertsDirectory and CVD_CERTS_DIR, starts a private 0600 socket, sends file
contents only after checking symlink/dataless flags, and shuts down the daemon.
It never invokes freshclam or RELOAD. The production FILDES engine (one request per connection),
five-minute idle policy and persistent CDN backoff state machine are in
`src-tauri/src/security/av/`; see README.md for M3 measurements.
