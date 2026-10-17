use base64::{Engine, engine::general_purpose::STANDARD};
use opennavo_desktop_lib::{
    AppError,
    model::OutdatedItem,
    uninstall::rules::Snapshot,
    updater::{
        UpdateChannel, archive, download, feed, gate, macho, package, service::Candidate, swap,
    },
};
use ring::signature::{Ed25519KeyPair, KeyPair};
use std::{
    fs,
    io::{Cursor, Write},
    path::{Path, PathBuf},
    sync::{Arc, atomic::AtomicBool},
};
use tokio::io::{AsyncBufReadExt, BufReader};
fn private_directory(path: &Path) -> std::io::Result<()> {
    use std::os::unix::fs::DirBuilderExt;
    fs::DirBuilder::new().mode(0o700).create(path)
}

fn bundle(path: &Path, version: &str) {
    fs::create_dir_all(path.join("Contents/MacOS")).unwrap();
    let info = plist::Value::Dictionary(
        [
            (
                "CFBundleIdentifier".to_string(),
                plist::Value::String("test.opennavo.Fixture".into()),
            ),
            (
                "CFBundleVersion".to_string(),
                plist::Value::String(version.into()),
            ),
            (
                "CFBundleExecutable".to_string(),
                plist::Value::String("fixture".into()),
            ),
        ]
        .into_iter()
        .collect(),
    );
    info.to_file_xml(path.join("Contents/Info.plist")).unwrap();
    fs::write(path.join("Contents/MacOS/fixture"), version).unwrap();
}
fn zip_bytes(app: &Path) -> Vec<u8> {
    let mut zip = zip::ZipWriter::new(Cursor::new(vec![]));
    for name in ["Contents/Info.plist", "Contents/MacOS/fixture"] {
        zip.start_file(
            format!("Fixture.app/{name}"),
            zip::write::SimpleFileOptions::default(),
        )
        .unwrap();
        zip.write_all(&fs::read(app.join(name)).unwrap()).unwrap();
    }
    zip.finish().unwrap().into_inner()
}
struct Observations;
impl gate::Inspector for Observations {
    fn inspect(&self, path: &Path) -> Result<gate::Identity, AppError> {
        let info = plist::Value::from_file(path.join("Contents/Info.plist")).unwrap();
        let version = info.as_dictionary().unwrap()["CFBundleVersion"]
            .as_string()
            .unwrap();
        Ok(gate::Identity {
            valid: true,
            team_id: "TESTTEAM01".into(),
            identifier: "test.opennavo.Fixture".into(),
            requirement: "fixture requirement".into(),
            notarized: true,
            architectures: ["arm64".into(), "x86_64".into()].into(),
            minimum_os: "13.0".into(),
            minimum_os_by_arch: Default::default(),
            version: version.into(),
            marketing_version: info
                .as_dictionary()
                .unwrap()
                .get("CFBundleShortVersionString")
                .and_then(|v| v.as_string())
                .unwrap_or("")
                .into(),
        })
    }
    fn satisfies(&self, _: &Path, _: &str) -> Result<bool, AppError> {
        Ok(true)
    }
}
struct TempFiles {
    fail_register: bool,
}
impl swap::Files for TempFiles {
    fn copy_bundle(&self, from: &Path, to: &Path) -> Result<(), AppError> {
        swap::NativeFiles.copy_bundle(from, to)
    }
    fn exchange(&self, a: &Path, b: &Path) -> Result<(), AppError> {
        swap::NativeFiles.exchange(a, b)
    }
    fn register(&self, path: &Path) -> Result<(), AppError> {
        assert!(path.ancestors().all(|p| {
            !p.file_name()
                .is_some_and(|name| name.to_string_lossy().starts_with(".opennavo-updates"))
        }));
        if self.fail_register {
            Err(AppError::new("E_UNKNOWN", "fixture_registration_failure"))
        } else {
            Ok(())
        }
    }
}

#[test]
fn recovery_is_noindex_keeps_one_version_and_retires_only_this_apps_legacy_backup() {
    use opennavo_desktop_lib::updater::backup;
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().canonicalize().unwrap();
    let installed = root.join("Installed.app");
    let prepared = root.join("Prepared.app");
    bundle(&installed, "100");
    let [current, legacy, _] = backup::directories(&installed).unwrap();
    private_directory(legacy.parent().unwrap()).unwrap();
    private_directory(&legacy).unwrap();
    bundle(&legacy.join("previous.app"), "99");
    let sibling = backup::directories(&root.join("Other.app")).unwrap()[1].clone();
    private_directory(&sibling).unwrap();
    bundle(&sibling.join("previous.app"), "keep");
    for (old, next) in [("100", "101"), ("101", "102")] {
        bundle(&prepared, next);
        let request = swap::SwapRequest {
            source: Default::default(),
            installed: installed.clone(),
            prepared: prepared.clone(),
            installed_snapshot: Snapshot::capture(&installed).unwrap(),
            prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
            expected_team: None,
            package: None,
        };
        let saved = swap::apply(
            &request,
            &swap::Context {
                inspector: &Observations,
                files: &TempFiles {
                    fail_register: false,
                },
                os: "15.0",
                arch: "arm64",
            },
        )
        .unwrap();
        assert_eq!(saved, current.join("previous.app"));
        assert_eq!(
            current.parent().unwrap().file_name().unwrap(),
            format!(".opennavo-updates-{}.noindex", unsafe {
                nix::libc::geteuid()
            })
            .as_str()
        );
        assert_eq!(
            fs::read(saved.join("Contents/MacOS/fixture")).unwrap(),
            old.as_bytes()
        );
        assert_eq!(
            fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
            next.as_bytes()
        );
        assert_eq!(fs::read_dir(&current).unwrap().count(), 1);
        assert!(!legacy.exists());
        assert_eq!(
            fs::read(sibling.join("previous.app/Contents/MacOS/fixture")).unwrap(),
            b"keep"
        );
    }
    // A failed new installation restores the earlier recovery slot as well as
    // the application. It must not leave older.app/UUID copies accumulating.
    bundle(&prepared, "103");
    let request = swap::SwapRequest {
        source: Default::default(),
        installed: installed.clone(),
        prepared: prepared.clone(),
        installed_snapshot: Snapshot::capture(&installed).unwrap(),
        prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
        expected_team: None,
        package: None,
    };
    assert_eq!(
        swap::apply(
            &request,
            &swap::Context {
                inspector: &Observations,
                files: &TempFiles {
                    fail_register: true
                },
                os: "15.0",
                arch: "arm64",
            }
        )
        .unwrap_err()
        .code,
        "E_UPDATE_SWAP"
    );
    assert_eq!(
        fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
        b"102"
    );
    assert_eq!(
        fs::read(current.join("previous.app/Contents/MacOS/fixture")).unwrap(),
        b"101"
    );
    assert_eq!(fs::read_dir(&current).unwrap().count(), 1);
}

#[test]
fn recovery_refuses_symlink_slot_without_replacing_app_or_touching_link_target() {
    use opennavo_desktop_lib::updater::backup;
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().canonicalize().unwrap();
    let installed = root.join("Installed.app");
    let prepared = root.join("Prepared.app");
    let other = root.join("Other.app");
    bundle(&installed, "100");
    bundle(&prepared, "101");
    bundle(&other, "keep");
    let current = backup::directories(&installed).unwrap()[0].clone();
    private_directory(current.parent().unwrap()).unwrap();
    private_directory(&current).unwrap();
    std::os::unix::fs::symlink(&other, current.join("previous.app")).unwrap();
    let request = swap::SwapRequest {
        source: Default::default(),
        installed: installed.clone(),
        prepared: prepared.clone(),
        installed_snapshot: Snapshot::capture(&installed).unwrap(),
        prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
        expected_team: None,
        package: None,
    };
    assert_eq!(
        swap::apply(
            &request,
            &swap::Context {
                inspector: &Observations,
                files: &TempFiles {
                    fail_register: false
                },
                os: "15.0",
                arch: "arm64",
            }
        )
        .unwrap_err()
        .code,
        "E_PROTECTED"
    );
    assert_eq!(
        fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
        b"100"
    );
    assert_eq!(
        fs::read(other.join("Contents/MacOS/fixture")).unwrap(),
        b"keep"
    );
    assert_eq!(fs::read_dir(current).unwrap().count(), 1);
}
async fn server(path: &Path, mode: &str) -> (tokio::process::Child, String, reqwest::Client) {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/updater");
    let mut process = tokio::process::Command::new("/usr/bin/python3")
        .args(["-I", "-u"])
        .arg(root.join("https.py"))
        .arg(path)
        .arg(mode)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::null())
        .kill_on_drop(true)
        .spawn()
        .unwrap();
    let mut line = String::new();
    tokio::time::timeout(
        std::time::Duration::from_secs(5),
        BufReader::new(process.stdout.take().unwrap()).read_line(&mut line),
    )
    .await
    .unwrap()
    .unwrap();
    let port: u16 = line.trim().parse().unwrap();
    let cert = reqwest::Certificate::from_pem(
        &fs::read(PathBuf::from(format!("{}.cert.pem", path.display()))).unwrap(),
    )
    .unwrap();
    let client = reqwest::Client::builder()
        .no_proxy()
        .tls_certs_only([cert])
        .timeout(std::time::Duration::from_secs(5))
        .build()
        .unwrap();
    (
        process,
        format!("https://127.0.0.1:{port}/Fixture.zip"),
        client,
    )
}
#[tokio::test]
async fn local_tls_eddsa_download_gate_backup_atomic_swap_and_automatic_rollback() {
    for rollback in [false, true] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let source = root.join("Source.app");
        bundle(&installed, "100");
        bundle(&source, "101");
        let bytes = zip_bytes(&source);
        let file = root.join("Fixture.zip");
        fs::write(&file, &bytes).unwrap();
        let (mut server, url, client) = server(&file, "normal").await;
        let key = Ed25519KeyPair::from_seed_unchecked(&[23; 32]).unwrap();
        let signature = STANDARD.encode(key.sign(&bytes).as_ref());
        let xml = format!(
            r#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>101</s:version><s:shortVersionString>2.0</s:shortVersionString><enclosure url="{url}" length="{}" s:edSignature="{signature}"/></item></channel></rss>"#,
            bytes.len()
        );
        let items = feed::parse(xml.as_bytes()).unwrap();
        let selected = feed::select(
            &items,
            &feed::Selection {
                installed: "100",
                os: "15.0",
                arch: "arm64",
                channel: UpdateChannel::Stable,
                skipped: None,
                manual: true,
                now: 0,
                rollout_group: 0,
            },
        )
        .unwrap()
        .clone();
        let item:OutdatedItem=serde_json::from_value(serde_json::json!({"kind":"cask","token":"","name":"Fixture","source":"sparkle","appId":"fixture","installedVersion":"1.0","currentVersion":"2.0","autoUpdates":false,"pinned":false,"ignored":false,"dependents":0,"major":false,"requiresAdmin":false,"running":false,"relaunchPending":false,"criticalUpdate":false})).unwrap();
        let candidate = Candidate {
            artifact: None,
            item,
            app_path: installed.clone(),
            snapshot: Snapshot::capture(&installed).unwrap(),
            feed_item: Some(selected),
            public_key: Some(STANDARD.encode(key.public_key().as_ref())),
            expected_team: Some("TESTTEAM01".into()),
            checked_at: 0,
        };
        let stage = download::Stage::create(&root.join("staging")).unwrap();
        let request = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap();
        let files = TempFiles {
            fail_register: rollback,
        };
        let result = swap::apply(
            &request,
            &swap::Context {
                inspector: &Observations,
                files: &files,
                os: "15.0",
                arch: "arm64",
            },
        );
        if rollback {
            assert_eq!(result.unwrap_err().code, "E_UPDATE_SWAP");
            assert!(candidate.snapshot.still(&installed));
            assert_eq!(
                fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
                b"100"
            );
        } else {
            let backup = result.unwrap();
            assert_eq!(
                fs::read(backup.join("Contents/MacOS/fixture")).unwrap(),
                b"100"
            );
            assert_eq!(
                fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
                b"101"
            );
        }
        // Mutating the signed bytes makes the same download fail before replacement.
        let mut bad = candidate.clone();
        bad.feed_item.as_mut().unwrap().ed_signature = Some(STANDARD.encode([0; 64]));
        let stage = download::Stage::create(&root.join("staging")).unwrap();
        assert_eq!(
            download::prepare_with_client(
                &bad,
                &stage,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_| Ok(())),
                &client
            )
            .await
            .unwrap_err()
            .code,
            "E_UPDATE_INTEGRITY"
        );
        server.kill().await.unwrap();
        server.wait().await.unwrap();
    }
}
#[test]
fn zip_path_alias_symlink_chain_and_special_entry_guards() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("bad.zip");
    for entries in [
        vec![("../escape", None)],
        vec![("App/file", None), ("app/other", None)],
        vec![("é/file", None), ("e\u{301}/other", None)],
        vec![("App/a", Some("../../escape"))],
        vec![("App/a", Some("b")), ("App/b", Some("a"))],
        vec![("App/a", Some("..")), ("App/b", Some("a/../escape"))],
        vec![("App/a", Some("dir")), ("App/a/file", None)],
    ] {
        let mut zip = zip::ZipWriter::new(fs::File::create(&path).unwrap());
        for (name, target) in entries {
            if let Some(target) = target {
                zip.add_symlink(name, target, zip::write::SimpleFileOptions::default())
                    .unwrap();
            } else {
                zip.start_file(name, zip::write::SimpleFileOptions::default())
                    .unwrap();
                zip.write_all(b"fixture").unwrap();
            }
        }
        zip.finish().unwrap();
        assert!(archive::validate_zip(&path).is_err());
    }
    let mut zip = zip::ZipWriter::new(fs::File::create(&path).unwrap());
    zip.add_symlink(
        "App/Framework/Versions/Current",
        "A",
        zip::write::SimpleFileOptions::default(),
    )
    .unwrap();
    zip.start_file(
        "App/Framework/Versions/A/binary",
        zip::write::SimpleFileOptions::default(),
    )
    .unwrap();
    zip.write_all(b"fixture").unwrap();
    zip.finish().unwrap();
    archive::validate_zip(&path).unwrap();
    assert!(archive::dmg_has_license(&plist::Value::Dictionary(
        [(
            "Properties".to_string(),
            plist::Value::Dictionary(
                [(
                    "Software License Agreement".to_string(),
                    plist::Value::Boolean(true)
                )]
                .into_iter()
                .collect()
            )
        )]
        .into_iter()
        .collect()
    )));
}
#[test]
fn macho_reader_checks_real_header_bounds_architecture_and_minimum_os() {
    let dir = tempfile::tempdir().unwrap();
    let file = dir.path().join("binary");
    let mut bytes = vec![0u8; 56];
    bytes[..4].copy_from_slice(&[0xcf, 0xfa, 0xed, 0xfe]);
    for (offset, value) in [
        (4, 0x0100000cu32),
        (16, 1),
        (20, 24),
        (32, 0x32),
        (36, 24),
        (40, 1),
        (44, 13 << 16),
    ] {
        bytes[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
    }
    fs::write(&file, &bytes).unwrap();
    assert_eq!(macho::inspect(&file).unwrap()["arm64"], "13.0.0");
    for (offset, value) in [(20, u32::MAX), (36, 4), (40, 2), (44, 0)] {
        let mut bad = bytes.clone();
        bad[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
        fs::write(&file, bad).unwrap();
        assert!(macho::inspect(&file).is_err());
    }
}

#[tokio::test]
async fn interrupted_transfer_resumes_with_validator_or_restarts_when_changed() {
    for mode in ["interrupt", "changed", "bad_range"] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let source = root.join("Source.app");
        bundle(&installed, "100");
        bundle(&source, "101");
        let bytes = zip_bytes(&source);
        let file = root.join("Fixture.zip");
        fs::write(&file, &bytes).unwrap();
        let (mut server, url, client) = server(&file, mode).await;
        let key = Ed25519KeyPair::from_seed_unchecked(&[23; 32]).unwrap();
        let signature = STANDARD.encode(key.sign(&bytes).as_ref());
        let xml = format!(
            r#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>101</s:version><enclosure url="{url}" length="{}" s:edSignature="{signature}"/></item></channel></rss>"#,
            bytes.len()
        );
        let item = serde_json::from_value(serde_json::json!({"kind":"cask","token":"","name":"Fixture","source":"sparkle","appId":"fixture","installedVersion":"1.0","currentVersion":"2.0","autoUpdates":false,"pinned":false,"ignored":false,"dependents":0,"major":false,"requiresAdmin":false,"running":false,"relaunchPending":false,"criticalUpdate":false})).unwrap();
        let candidate = Candidate {
            artifact: None,
            item,
            app_path: installed.clone(),
            snapshot: Snapshot::capture(&installed).unwrap(),
            feed_item: Some(feed::parse(xml.as_bytes()).unwrap().remove(0)),
            public_key: Some(STANDARD.encode(key.public_key().as_ref())),
            expected_team: Some("TESTTEAM01".into()),
            checked_at: 0,
        };
        let stage = download::Stage::create(&root.join("staging")).unwrap();
        assert!(
            download::prepare_with_client(
                &candidate,
                &stage,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_| Ok(())),
                &client
            )
            .await
            .is_err()
        );
        drop(stage); // Retry has a different task stage, like a task/process restart.
        let stage = download::Stage::create(&root.join("staging")).unwrap();
        let result = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await;
        if mode == "bad_range" {
            assert_eq!(result.unwrap_err().message, "download_resume_range");
        } else {
            result.unwrap();
        }
        let requests: serde_json::Value =
            serde_json::from_slice(&fs::read(file.with_extension("zip.requests")).unwrap())
                .unwrap();
        assert_eq!(requests[1]["range"], format!("bytes={}-", bytes.len() / 2));
        assert_eq!(requests[1]["ifRange"], "\"fixture\"");
        assert!(candidate.snapshot.still(&installed));
        server.kill().await.unwrap();
        server.wait().await.unwrap();
    }
}

#[test]
fn package_trust_requires_developer_id_installer_and_matching_team() {
    let good = "Status: signed by a certificate trusted by macOS\nCertificate Chain:\n1. Developer ID Installer: Fixture (TESTTEAM01)\n2. Developer ID Certification Authority\n3. Apple Root CA";
    assert!(package::installer_signature(good, "TESTTEAM01"));
    for bad in [
        good.replace("TESTTEAM01", "OTHERTEAM1"),
        good.replace("Installer:", "Application:"),
        good.replace("trusted by macOS", "untrusted"),
        good.replace("1. Developer ID Installer:", "2. Developer ID Installer:"),
    ] {
        assert!(!package::installer_signature(&bad, "TESTTEAM01"));
    }
}
#[test]
fn package_payload_is_read_only_and_limited_to_the_expected_bundle() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().canonicalize().unwrap();
    let files = root.join("payload");
    let app = files.join("Fixture.app");
    bundle(&app, "101");
    let mut info = plist::Value::from_file(app.join("Contents/Info.plist")).unwrap();
    info.as_dictionary_mut().unwrap().insert(
        "CFBundlePackageType".into(),
        plist::Value::String("APPL".into()),
    );
    info.to_file_xml(app.join("Contents/Info.plist")).unwrap();
    let pkg = root.join("fixture.pkg");
    let output = std::process::Command::new("/usr/bin/pkgbuild")
        .args(["--root"])
        .arg(&files)
        .args(["--install-location"])
        .arg(&root)
        .args(["--identifier", "test.opennavo.Fixture", "--version", "101"])
        .arg(&pkg)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let installed = root.join("Fixture.app");
    let prepared = package::payload(
        &pkg,
        &root.join("expanded"),
        &installed,
        "test.opennavo.Fixture",
    )
    .unwrap();
    assert!(prepared.join("Contents/Info.plist").is_file());
    assert!(!installed.exists());
    assert!(
        package::payload(
            &pkg,
            &root.join("wrong-bundle"),
            &installed,
            "test.opennavo.Other"
        )
        .is_err()
    );
    assert!(
        package::payload(
            &pkg,
            &root.join("wrong-destination"),
            &root.join("Other.app"),
            "test.opennavo.Fixture"
        )
        .is_err()
    );
    // Unsigned fixture can exercise extraction only; it never passes the production gate.
    assert!(package::verify_signature(&pkg, "TESTTEAM01").is_err());
}
struct TempInstaller {
    installed: PathBuf,
    prepared: PathBuf,
    fail: bool,
}
impl package::Installer for TempInstaller {
    fn install(&self, _: &Path) -> Result<(), AppError> {
        use swap::Files;
        // Simulate an installer that already changed part of the bundle before failing.
        fs::remove_dir_all(&self.installed)?;
        swap::NativeFiles.copy_bundle(&self.prepared, &self.installed)?;
        if self.fail {
            Err(AppError::new("E_UNKNOWN", "fixture_installer_exit"))
        } else {
            Ok(())
        }
    }
}
#[test]
fn package_transaction_backs_up_and_restores_even_after_partial_installer_failure() {
    for fail in [false, true] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let prepared = root.join("Prepared.app");
        bundle(&installed, "100");
        bundle(&prepared, "101");
        let request = swap::SwapRequest {
            source: Default::default(),
            installed_snapshot: Snapshot::capture(&installed).unwrap(),
            prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
            installed: installed.clone(),
            prepared: prepared.clone(),
            expected_team: Some("TESTTEAM01".into()),
            package: None,
        };
        let files = TempFiles {
            fail_register: false,
        };
        let result = package::apply(
            &request,
            &root.join("fixture.pkg"),
            &swap::Context {
                inspector: &Observations,
                files: &files,
                os: "15.0",
                arch: "arm64",
            },
            &TempInstaller {
                installed: installed.clone(),
                prepared,
                fail,
            },
        );
        if fail {
            assert_eq!(result.unwrap_err().code, "E_UPDATE_SWAP");
        } else {
            assert_eq!(
                fs::read(result.unwrap().join("Contents/MacOS/fixture")).unwrap(),
                b"100"
            );
        }
        assert_eq!(
            fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
            if fail { b"100" } else { b"101" }
        );
    }
}

#[test]
fn package_cannot_relocate_or_request_logout() {
    assert!(
        package::fixed_location_metadata(
            br#"<pkg-info relocatable="false" postinstall-action="none"><relocate/></pkg-info>"#
        )
        .is_ok()
    );
    for xml in [
        r#"<pkg-info relocatable="true"/>"#,
        r#"<pkg-info relocatable="false" postinstall-action="restart"/>"#,
        r#"<pkg-info relocatable="false"><relocate><bundle id="other"/></relocate></pkg-info>"#,
    ] {
        assert!(package::fixed_location_metadata(xml.as_bytes()).is_err());
    }
}

#[tokio::test]
async fn electron_github_vendor_download_hash_gate_swap_and_rollback_share_the_safe_path() {
    use opennavo_desktop_lib::updater::{
        UpdateSource,
        release::{Artifact, File},
    };
    use sha2::{Digest, Sha256, Sha512};
    for source_kind in [
        UpdateSource::Electron,
        UpdateSource::Github,
        UpdateSource::Vendor,
    ] {
        for rollback in [false, true] {
            let directory = tempfile::tempdir().unwrap();
            let root = directory.path().canonicalize().unwrap();
            let installed = root.join("Installed.app");
            let source = root.join("Source.app");
            bundle(&installed, "100");
            bundle(&source, "101");
            if source_kind == UpdateSource::Electron {
                for (path, version) in [(&installed, "2.0.0-beta.1"), (&source, "2.0.0-beta.2")] {
                    let info_path = path.join("Contents/Info.plist");
                    let mut info = plist::Value::from_file(&info_path).unwrap();
                    info.as_dictionary_mut()
                        .unwrap()
                        .insert("CFBundleShortVersionString".into(), version.into());
                    info.to_file_xml(info_path).unwrap();
                }
            }
            let bytes = zip_bytes(&source);
            let file = root.join("Fixture.zip");
            fs::write(&file, &bytes).unwrap();
            let (mut server, url, client) = server(&file, "normal").await;
            let item:OutdatedItem=serde_json::from_value(serde_json::json!({"kind":"link","token":"navigation-only","name":"Fixture","source":source_kind,"appId":"fixture","installedVersion":"100","currentVersion":"101","autoUpdates":false,"pinned":false,"ignored":false,"dependents":0,"major":false,"requiresAdmin":false,"running":false,"relaunchPending":false,"criticalUpdate":false,"expectedTeamMatch":true})).unwrap();
            let artifact = Artifact {
                version: if source_kind == UpdateSource::Electron {
                    "2.0.0-beta.2"
                } else {
                    "101"
                }
                .into(),
                version_key: if source_kind == UpdateSource::Electron {
                    "CFBundleShortVersionString"
                } else {
                    "CFBundleVersion"
                }
                .into(),
                file: File {
                    url,
                    name: "Fixture.zip".into(),
                    size: Some(bytes.len() as u64),
                    architecture: Some("universal".into()),
                    sha256: (source_kind != UpdateSource::Electron)
                        .then(|| format!("{:x}", Sha256::digest(&bytes))),
                    sha512: (source_kind == UpdateSource::Electron)
                        .then(|| STANDARD.encode(Sha512::digest(&bytes))),
                },
            };
            let candidate = Candidate {
                item,
                artifact: Some(artifact),
                app_path: installed.clone(),
                snapshot: Snapshot::capture(&installed).unwrap(),
                feed_item: None,
                public_key: None,
                expected_team: Some("TESTTEAM01".into()),
                checked_at: 0,
            };
            let mut bad = candidate.clone();
            if source_kind == UpdateSource::Electron {
                bad.artifact.as_mut().unwrap().file.sha512 = Some(STANDARD.encode([0; 64]))
            } else {
                bad.artifact.as_mut().unwrap().file.sha256 = Some("0".repeat(64))
            }
            let stage = download::Stage::create(&root.join("staging")).unwrap();
            assert_eq!(
                download::prepare_with_client(
                    &bad,
                    &stage,
                    Arc::new(AtomicBool::new(false)),
                    Arc::new(|_| Ok(())),
                    &client
                )
                .await
                .unwrap_err()
                .code,
                "E_UPDATE_INTEGRITY"
            );
            assert_eq!(
                fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
                b"100"
            );
            let stage = download::Stage::create(&root.join("staging")).unwrap();
            let request = download::prepare_with_client(
                &candidate,
                &stage,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_| Ok(())),
                &client,
            )
            .await
            .unwrap();
            let result = swap::apply(
                &request,
                &swap::Context {
                    inspector: &Observations,
                    files: &TempFiles {
                        fail_register: rollback,
                    },
                    os: "15.0",
                    arch: "arm64",
                },
            );
            if rollback {
                assert_eq!(result.unwrap_err().code, "E_UPDATE_SWAP");
                assert_eq!(
                    fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
                    b"100"
                )
            } else {
                result.unwrap();
                assert_eq!(
                    fs::read(installed.join("Contents/MacOS/fixture")).unwrap(),
                    b"101"
                )
            }
            server.kill().await.unwrap();
            server.wait().await.unwrap();
        }
    }
}

#[test]
fn electron_beta_upgrades_and_stable_transition_pass_every_swap_gate() {
    use opennavo_desktop_lib::model::UpdateSource;
    for (from, to, allowed) in [
        ("2.0.0-beta.1", "2.0.0-beta.2", true),
        ("2.0.0-beta.2", "2.0.0", true),
        ("2.0.0", "2.0.0-beta.2", false),
    ] {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let prepared = root.join("Prepared.app");
        for (path, marketing) in [(&installed, from), (&prepared, to)] {
            bundle(path, "100");
            let info_path = path.join("Contents/Info.plist");
            let mut info = plist::Value::from_file(&info_path).unwrap();
            info.as_dictionary_mut()
                .unwrap()
                .insert("CFBundleShortVersionString".into(), marketing.into());
            info.to_file_xml(info_path).unwrap();
        }
        let request = swap::SwapRequest {
            source: UpdateSource::Electron,
            installed: installed.clone(),
            prepared: prepared.clone(),
            installed_snapshot: Snapshot::capture(&installed).unwrap(),
            prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
            expected_team: None,
            package: None,
        };
        let result = swap::apply(
            &request,
            &swap::Context {
                inspector: &Observations,
                files: &TempFiles {
                    fail_register: false,
                },
                os: "15.0",
                arch: "arm64",
            },
        );
        assert_eq!(result.is_ok(), allowed, "{from} -> {to}: {result:?}");
        let info = plist::Value::from_file(installed.join("Contents/Info.plist")).unwrap();
        assert_eq!(
            info.as_dictionary().unwrap()["CFBundleShortVersionString"]
                .as_string()
                .unwrap(),
            if allowed { to } else { from }
        );
    }
}
