# ClamAV process integration

OpenNavo runs ClamAV 1.5.4 as independent GPL-2.0 processes. It never links
libclamav into Rust. See BUILD.md for locked source builds and source bundles.

The release workflow caches builds by engine version, target architecture and
recipe hash. Tauri stages clamd, freshclam, clamscan and clamav.crt at
Contents/Library/Helpers/clamav/. The matching corresponding-source archive is
mandatory in the same GitHub Release. Native executables are signed before the
outer app is signed and notarized. No signature database ships in the app.

The engine starts on demand for scans; the default idle deadline is five minutes.
`avKeepResident` retains an already started engine. `avConsent` is required before
freshclam runs. Update checks default to four hours with persistent cooldown and
jitter. Both `CVDCertsDirectory` and `CVD_CERTS_DIR` are always supplied. Updated
databases are loaded by restarting the idle daemon at the next scan, without
RELOAD or concurrent database reload.

The private Unix socket is mode 0600. Rust opens verified regular files using
component-by-component O_NOFOLLOW, rejects SF_DATALESS before reads/descents,
and passes descriptors with one FILDES request per connection. A disconnected
request is retried once with the same descriptor rewound; no pathname is reopened.
INSTREAM is used only when that
command is unavailable. Ordinary files above 100 MiB and installer containers above 1 GiB are skipped,
along with symlinks and configured exclusions. Progress events are limited to one per 250 ms.

## macOS 27 measurements (2026-10-16)

M0 cached official database; no CDN downloads. Release helper, debug Rust driver,
local temporary fixture directory; no real files quarantined or removed.

| Measurement                                | Result                         |
| ------------------------------------------ | ------------------------------ |
| Engine startup plus EICAR scan             | 7.406 s                        |
| Resident memory after loading              | 1,126,858,752 bytes / 1.05 GiB |
| Loaded engine, 16 MiB repeated-byte corpus | 0.221 s / 72.45 MiB/s          |
| Default five-minute idle exit              | 300.137 s                      |
| EICAR detect → quarantine → restore        | Passed                         |
| Official signature database downloads      | 0                              |

These are a small synthetic corpus and one local machine, not a full-disk
throughput estimate. M0's earlier result was 7.85 seconds and 1.05 GiB. macOS
13/14/15 are not verified on this host.

Reproduce without changing the installed application profile:

```sh
cargo run --manifest-path apps/desktop/src-tauri/Cargo.toml \
  --example security_av_probe -- \
  --cached-temp-only /absolute/helpers/clamav /absolute/cached-db
```

The probe constructs EICAR only inside its temporary directory, verifies the
restore, deletes the temporary test file, and waits for the actual 300-second
idle deadline. It never starts freshclam.

## S2 pre-download checks (2026-10-16)

Homebrew installation checks are limited to files fetched before installation.
When consent and a ready database are present, OpenNavo fetches the target and
its dependencies into a task-private cache, scans eligible files, rechecks their
identity and hash, then runs the original installation command with that cache
and automatic updates disabled. It does not wrap Homebrew in another sandbox.
Downloads made by build or installation hooks are outside this check: their
Downloading/Fetching headings and new cache paths are recorded in the task log,
without failing the installation. The private cache is removed on success,
failure and cancellation. Disabled checks or an unavailable database keep the
original installation path and create no private cache.

The input limits are **100 MiB for ordinary files** and **1 GiB for installer
containers** (DMG, PKG/XAR, ZIP, tar and compressed tar/bottles, recognized by
extension or bounded header/trailer reads). All scan kinds and download checks
share this Rust policy. Clamd uses `MaxFileSize 1073741824`,
`MaxScanSize 2147483648`, `MaxFiles 50000`, `MaxRecursion 17`,
`MaxScanTime 300000` and `ScanArchive yes`. The locked source stores MaxScanSize
as uint64_t; its INT_MAX-2 restriction applies to MaxFileSize only, so these
settings need no reduction.
Supported containers are recursively parsed within these limits; DMG, XAR/PKG
and ZIP are supported by the locked ClamAV source. This does not guarantee every
filesystem, compression or encrypted variant can be inspected. No installer is executed for scanning. Only pre-install checks may use the
read-only mount fallback below; other scan entry points never mount images. `MaxScanSize` is a cumulative
scan budget, so a small compressed download may still exceed it during unpacking.

`AlertExceedsMax yes`, `AlertEncrypted yes` and `HeuristicScanPrecedence no` are
explicit. All `Heuristics.Limits.Exceeded.*` results mean an incomplete scan
(`too_large`), and `Heuristics.Encrypted.*` means `unreadable`; neither is a virus
finding or a reason to quarantine a file. A real signature reported by ClamAV
remains a threat. Quick/full/custom scans count these as skipped; download and
installation checks expose the same reason. Installation `clean` requires every
download to have a complete result without threats. `filesScanned` counts actual
completed file scans; a skipped file contributes zero.

Initial 100 MiB measurements of the existing Homebrew cache, with the M0 database:

| Cached installer    | File size (MiB) | Result                                 | Scan time (s) |
| ------------------- | --------------: | -------------------------------------- | ------------: |
| Rectangle 2.0.3 DMG |             4.5 | Complete, no threat found              |          3.25 |
| CaskHub 0.9.2 ZIP   |             7.5 | Complete, no threat found              |          5.46 |
| SwiftVM 1.3.0 DMG   |             5.4 | Initially OK; coverage later disproved |          0.28 |
| Keka 1.6.8 DMG      |            32.8 | Limit reached while unpacking          |          4.30 |
| Zedis DMG           |            16.3 | Limit reached while unpacking          |          3.11 |
| Telegram ZIP        |           143.5 | Input exceeds 100 MiB                  |             — |
| GitHub Desktop ZIP  |           459.4 | Input exceeds 100 MiB                  |             — |
| Docker DMG          |           562.6 | Input exceeds 100 MiB                  |             — |
| Mesh 3.0.4 DMG      |           367.4 | Input exceeds 100 MiB                  |             — |
| VS Code ZIP         |           313.6 | Input exceeds 100 MiB                  |             — |

Seven of ten cached App installers could not be completely scanned at the
initial limits. Two separate research-cache ClamAV PKGs (135.9/159.0 MiB) also
exceed the input limit. This convenience sample is not an estimate for the whole
catalog. The coordinator subsequently approved the two tiers above; see the updated measurements below.

Temporary container fixtures verified real engine behavior: a 122,504-byte ZIP
expanding to 120 MiB was skipped; an encrypted ZIP was unreadable; a 22-level
nested ZIP was skipped, while the same nested branch beside EICAR returned
`Eicar-Test-Signature`. EICAR in an ordinary ZIP was also detected. This confirms
signature precedence for inspected members; it cannot detect an uninspected
member beyond the scan budget. No real installation, quarantine or CDN request
was performed. Cached engine load took 5.90–6.27 seconds.

Reproduce read-only corpus scans (canonical absolute file paths are required):

```sh
cargo run --manifest-path apps/desktop/src-tauri/Cargo.toml \
  --example security_s2_probe -- \
  --cached-read-only /absolute/helpers/clamav /absolute/cached-db /absolute/file.zip
```

The one clamd worker is serialized in Rust (`MaxThreads 1`), including small
compressed files that could expand substantially. Requests waiting for the
worker remain cancelable. Cancellation or a protocol deadline kills and waits
for the owned clamd child, then removes extraction leftovers; the next request
restarts it on demand. ClamAV writes to a mode-0700 `scan-tmp` directory beneath
the application's private AV runtime. It is cleared after each scan and before
a new daemon starts. Scans require the 2 GiB budget plus 128 MiB of free-space
headroom; insufficient space is logged and returned as `too_large`.

Installation tasks report phase `scanning` during this work. The feature remains
a pre-download check: extra downloads made during installation are logged but
are not covered by its verdict.

## Container coverage and DMG fallback

Clamd can return OK after omitting unsupported container content. Rust inspects
bounded ZIP central-directory metadata and DMG block metadata in addition to the
engine result. Unknown nonempty DMG stripes (including LZFSE/LZMA), APFS and a
nonempty image with no actual DMG extraction are incomplete (`unreadable`). ZIP
members above the file or cumulative budget are `too_large`. These checks never
replace a genuine signature finding with a skipped result. ClamAV extraction
files are retained only until Rust completes this coverage check, then removed
before the next request.

Only the pre-install path can fall back to `hdiutil attach -plist -nobrowse
-readonly -noautoopen -mountrandom` in a private mount directory with stdin `qn\n`.
There is no UDTO conversion or acceptance of license prompts. Mounted regular
files are scanned through FILDES with a 1 GiB per-file limit, 2 GiB total input
budget and 50,000-object cap. Symlinks and device files are not followed/read.
SLA, encryption, password or unsupported-format failures become incomplete scans.

The mount guard records preexisting devices, detaches only devices acquired by
this scan, retries a failed detach with `-force`, and verifies `hdiutil info`
after cleanup. An unconfirmed detach stops installation with `E_INSTALL_SCAN`
and retains the private directory for diagnosis. Attach/detach operations are
logged immediately, including cancellation. Existing mounted images are never
unmounted by this check. Normal download verification and quick/full/custom
scans retain the incomplete result instead of mounting.

## Final S2 corpus measurements (macOS 27)

The same ten cached Homebrew installers and two cached research PKGs were read
with the M0 database; there were no CDN requests. `complete` means the check
finished without a threat. The container path completed 6/10 App installers;
the pre-install path with read-only mounting completed 8/10. Keka remained
partially unreadable; its 732 completed files are retained in the result.
The initial 100 MiB SwiftVM OK above was a false coverage indication from clamd,
not a complete scan. The coverage checks now reject that result.

| Installer        | Container (s / result) | Pre-install (s / result) | Completed files | Peak RSS (GiB, container / install) | Peak temp (MiB, container / install) |
| ---------------- | ---------------------- | ------------------------ | --------------: | ----------------------------------: | -----------------------------------: |
| Rectangle        | 3.36 / complete        | 3.41 / complete          |               1 |                         1.07 / 1.07 |                        45.55 / 45.55 |
| Telegram         | 24.76 / complete       | 24.16 / complete         |               1 |                         1.27 / 1.23 |                      382.74 / 382.74 |
| CaskHub          | 5.55 / complete        | 5.52 / complete          |               1 |                         1.07 / 1.08 |                        18.53 / 18.53 |
| Keka             | 9.39 / unreadable      | 37.21 / unreadable       |             732 |                         1.08 / 1.13 |                        99.60 / 99.60 |
| GitHub Desktop   | 66.32 / complete       | 65.44 / complete         |               1 |                         1.66 / 1.67 |                    1039.42 / 1039.42 |
| Zedis            | 3.91 / complete        | 4.01 / complete          |               1 |                         1.11 / 1.24 |                        90.17 / 90.17 |
| Docker           | 12.81 / unreadable     | 175.67 / too_large       |             128 |                         1.23 / 2.37 |                       0.03 / 1370.73 |
| Mesh             | 16.98 / unreadable     | 71.60 / complete         |             597 |                         1.22 / 1.76 |                         0.00 / 26.21 |
| VS Code          | 81.76 / complete       | 80.74 / complete         |               1 |                         1.51 / 2.28 |                    1208.39 / 1208.39 |
| SwiftVM          | 0.28 / unreadable      | 2.49 / complete          |             116 |                         1.21 / 1.98 |                          0.02 / 0.00 |
| ClamAV 1.4.6 PKG | 66.42 / complete       | 67.31 / complete         |               1 |                         1.35 / 2.12 |                      616.60 / 616.60 |
| ClamAV 1.5.4 PKG | 75.10 / too_large      | 76.48 / too_large        |               0 |                         1.17 / 1.94 |                      706.71 / 706.71 |

These are individual samples, excluding engine loading (about 6 seconds).
RSS and temporary sizes were sampled every 250 ms, so they are observed peaks,
not guaranteed absolute maxima. The same daemon is reused within each run;
allocator retention affects later RSS values. Temp sizes sum logical bytes in
private `scan-tmp`, excluding the cached source image and read-only mounted
volume contents, which are not copied into private storage. All requests ended
with zero extraction bytes. Rectangle and Zedis container measurements were
repeated after correcting an overstrict comparison against zero-filled DMG
sectors; the remaining container measurements and the mount run are unchanged.

The table records the comparison **before** the final metadata preflight.
After preflight was added, Docker's blkx metadata reported 2,838,495,232 expanded
bytes, exceeding 2 GiB. A cold probe returned `skipped/too_large`, zero scanned
files, in **0.003716 seconds**, with no clamd startup, no mount and zero temp
bytes. ZIP/ZIP64 central directories and DMG blkx metadata are counted before
scanning; mounted volumes are enumerated using stat before any file-content
scan. A known oversized workload is skipped immediately. Unknown formats keep
runtime accounting. This install-only early skip does not claim that oversized
content was inspected. Ordinary scan entry points retain real-signature
precedence when ClamAV actually inspects a mixed container.

GitHub Desktop, Mesh and VS Code exceed the original 60-second target. The
coordinator accepted the measured times and chose no additional whole-task
timeout; MaxScanTime remains 300 seconds per object. All long work is cancelable.
A real in-flight VS Code scan canceled in **1.103 seconds** (including a deliberate
1-second delay before cancellation), stopped the owned clamd and left zero temp
bytes. The temporary EICAR + deep-recursion fixture still reports a threat;
deep recursion and a 2.4 GiB expanded ZIP/ZIP64 fixture report `too_large`, and an
encrypted ZIP reports `unreadable`.

Task scanning progress has a 250 ms heartbeat. Opaque container requests expose
null percent/bytesDone/bytesTotal/speedBps; mounted volumes expose only completed
file bytes and their stat-derived total. A single file taking at least one
second switches to all-null progress while it is in flight, then resumes the
actual completion ratio. Fetch occupies task percent 0–60, scanning 60–70, and
existing installation starts at 70. Each input reserves a stable slice of the
scanning interval, so moving between mounted volumes cannot regress percent.
Extraction bytes are never presented as scanned bytes. Contract fields are
unchanged.

Four comparison mounts (Keka, Docker, Mesh, SwiftVM) were detached and verified
by `hdiutil info`. A final global info snapshot contained no private scan mount.
Before/after `lsregister -dump` path sets had **no new registration paths** and
no private mount/runtime paths; no unregister command was run. Neither an App
nor an installer was opened or installed. Source logs are under the temporary
`/tmp/opennavo-m3/m5-*-final.log` and `m5-final-*-results.log` probe artifacts.

## Quick-scan acceptance follow-up (2026-10-16)

On macOS 27 arm64, a read-only quick scan with the cached M0 database and 1,126
cached startup records completed in **513.33 seconds**. It scanned 5,233 files
(2,611,272,726 bytes); observed peak clamd RSS was **1,395,572,736 bytes**
(1.30 GiB), sampled every 500 ms. The only finding was the coordinator's
68-byte EICAR fixture in Downloads (`Eicar-Test-Signature`). Nothing was
quarantined, restored, deleted or downloaded. The owned daemon exited with the
probe; results and runtime lived in a temporary fixture.

Downloads is now visited first. Quick mode visits only current-user top-level
files in the temporary roots; recent applications must be actual `.app`
directories. Code candidates use executable/archive magic and script/code
extensions, not executable permissions alone. Both `.opennavo-updates` and
`.opennavo-updates.noindex` are excluded from discovered quick roots and startup
plugin discovery. Full and custom scans retain their requested scope.

The earlier scoped probe took 456.67 seconds for 5,743 files. The final sample
processed fewer files but was slower, so this is not evidence of a repeatable
timing improvement. Downloads/Desktop containers and top-level temporary files
dominate this workload. A HarmonyOS archive and an APK produced progress-event
gaps of about 49 and 74 seconds; these are not exact per-file timers. Quick mode
cannot promise completion in a few minutes with such inputs. The existing
1 GiB outer / 2 GiB expanded budgets remain unchanged.

The final result was `done`, with no denied root and no engine EOF/retry. Skips
were 7 over budget, 12 unreadable, 4,105 excluded and 0 dataless. This does not
prove the cause of the earlier acceptance EOF or represent the packaged App's
TCC permissions. Each file now uses a standalone FILDES request; a disconnected
request retries the same descriptor once, then fails explicitly if needed.

One unreadable sample, UURemote's main executable, reproduced a ClamAV ZIP
temporary-member creation error with `LeaveTemporaryFiles yes`. A diagnostic
temporary config with that option disabled completed the same file. Production
keeps the option enabled because DMG coverage verification relies on extraction
evidence; disabling it globally would remove that protection. The error remains
an incomplete scan pending a separate temporary-name/coverage solution.

Logs: `/tmp/opennavo-m3/quick-readonly-final.{jsonl,log}` and
`/tmp/opennavo-m3/clamd-diagnostic-as7sxvb9/`.
