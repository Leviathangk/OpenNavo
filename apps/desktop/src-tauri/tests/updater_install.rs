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
            notarization_unavailable: false,
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
            unsigned: None,
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
        unsigned: None,
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
        unsigned: None,
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
            "E_UPDATE_FEED_SIGNATURE"
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
fn dmg_with_native_imageinfo_diagnostics_still_extracts_and_unmounts() {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().canonicalize().unwrap();
    let source = root.join("source");
    let app = source.join("Fixture.app");
    bundle(&app, "101");
    let dmg = root.join("Fixture.dmg");
    let output = std::process::Command::new("/usr/bin/hdiutil")
        .args(["create", "-quiet", "-format", "UDZO", "-srcfolder"])
        .arg(&source)
        .arg(&dmg)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    // macOS 27 emits the imageinfo deprecation warning on stderr. This uses the
    // real extractor so both the stream parsing and mount cleanup are covered.
    let destination = root.join("extracted");
    let prepared = archive::extract_dmg(&dmg, &destination).unwrap();
    assert_eq!(
        fs::read(prepared.join("Contents/MacOS/fixture")).unwrap(),
        b"101"
    );
    assert_eq!(fs::read_dir(destination.join("mount")).unwrap().count(), 0);
    fs::write(root.join("corrupt.dmg"), b"not a disk image").unwrap();
    let error = archive::extract_dmg(&root.join("corrupt.dmg"), &root.join("invalid")).unwrap_err();
    assert_eq!(error.code, "E_UPDATE_ARCHIVE");
    assert_eq!(error.message, "dmg_info_command");
    assert!(error.detail.unwrap().contains("exitCode"));
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
            let error = result.unwrap_err();
            assert_eq!(error.message, "download_resume_range", "{error:?}");
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
    for name in ["Fixture.app", "中文应用.app"] {
        package_payload_for_name(name);
    }
}
fn package_payload_for_name(name: &str) {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().canonicalize().unwrap();
    let files = root.join("payload");
    let app = files.join(name);
    bundle(&app, "101");
    let mut info = plist::Value::from_file(app.join("Contents/Info.plist")).unwrap();
    info.as_dictionary_mut().unwrap().insert(
        "CFBundlePackageType".into(),
        plist::Value::String("APPL".into()),
    );
    info.to_file_xml(app.join("Contents/Info.plist")).unwrap();
    let pkg = root.join("fixture.pkg");
    // pkgbuild's bundle relocation defaults differ between macOS versions.
    let components = root.join("components.plist");
    let mut component = plist::Dictionary::new();
    component.insert(
        "RootRelativeBundlePath".into(),
        plist::Value::String(name.into()),
    );
    component.insert("BundleIsRelocatable".into(), plist::Value::Boolean(false));
    plist::Value::Array(vec![plist::Value::Dictionary(component)])
        .to_file_xml(&components)
        .unwrap();
    let output = std::process::Command::new("/usr/bin/pkgbuild")
        .args(["--root"])
        .arg(&files)
        .arg("--component-plist")
        .arg(&components)
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
    let installed = root.join(name);
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
    // Unverified installer execution remains forbidden; an authenticated,
    // script-free payload can enter the separate per-artifact review path.
    assert!(package::verify_signature(&pkg, "TESTTEAM01").is_err());
    assert_eq!(
        package::review_signature(&pkg, None, true).unwrap(),
        vec![opennavo_desktop_lib::updater::UpdateWarning::UnsignedDeveloper]
    );
    assert_eq!(
        package::review_signature(&pkg, None, false)
            .unwrap_err()
            .code,
        "E_UPDATE_DIGEST"
    );
}

#[tokio::test]
async fn vendor_retries_replace_invalid_content_without_a_publisher_digest() {
    use opennavo_desktop_lib::updater::release::{Artifact, File};
    use sha2::{Digest, Sha256};
    for scenario in ["html", "zip", "plist", "version", "legacy"] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let source = root.join("Source.app");
        bundle(&installed, "100");
        bundle(&source, "101");
        let bytes = zip_bytes(&source);
        let (invalid, message) = match scenario {
            "zip" => (b"PK\x03\x04truncated".to_vec(), "zip_format"),
            "plist" => {
                fs::write(source.join("Contents/Info.plist"), b"invalid plist").unwrap();
                (zip_bytes(&source), "download_bundle_info")
            }
            "version" => {
                bundle(&source, "102");
                (zip_bytes(&source), "feed_bundle_version_mismatch")
            }
            _ => (
                b"<html>Service temporarily unavailable</html>".to_vec(),
                "unsupported_update_archive",
            ),
        };
        let file = root.join("Fixture.zip");
        fs::write(&file, &bytes).unwrap();
        fs::write(file.with_extension("zip.invalid"), &invalid).unwrap();
        let (mut server, url, client) = server(&file, "invalid_first").await;
        let candidate = Candidate {
            item: serde_json::from_value(serde_json::json!({
                "kind": "link", "token": "", "name": "Fixture", "source": "vendor",
                "appId": "fixture", "installedVersion": "100", "currentVersion": "101",
                "autoUpdates": false, "pinned": false, "ignored": false, "dependents": 0,
                "major": false, "requiresAdmin": false, "running": false,
                "relaunchPending": false, "criticalUpdate": false, "expectedTeamMatch": true
            }))
            .unwrap(),
            artifact: Some(Artifact {
                file: File {
                    url,
                    name: "Fixture.zip".into(),
                    size: None,
                    architecture: None,
                    sha256: None,
                    sha512: None,
                },
                version: "101".into(),
                version_key: "CFBundleVersion".into(),
            }),
            app_path: installed.clone(),
            snapshot: Snapshot::capture(&installed).unwrap(),
            feed_item: None,
            public_key: None,
            expected_team: Some("TESTTEAM01".into()),
            checked_at: 0,
        };
        let staging = root.join("staging");
        let stage = download::Stage::create(&staging).unwrap();
        let error = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap_err();
        assert_eq!(error.message, message, "{scenario}");
        assert!(!stage.path.join("verified.sha256").exists(), "{scenario}");
        drop(stage);
        let cache = fs::read_dir(staging.join("downloads"))
            .unwrap()
            .next()
            .unwrap()
            .unwrap()
            .path();
        let cached = cache.join("partial");
        let metadata = cache.join("metadata.json");
        assert_eq!(fs::metadata(&cached).unwrap().len(), 0, "{scenario}");
        let meta: serde_json::Value =
            serde_json::from_slice(&fs::read(&metadata).unwrap()).unwrap();
        assert!(meta["verified_sha256"].is_null(), "{scenario}");
        if scenario == "legacy" {
            // Model an error page marked reusable by an older app version.
            fs::write(&cached, &invalid).unwrap();
            fs::write(
                &metadata,
                serde_json::to_vec(&serde_json::json!({
                    "validator": "\"fixture\"", "total": invalid.len(),
                    "verified_sha256": format!("{:x}", Sha256::digest(&invalid))
                }))
                .unwrap(),
            )
            .unwrap();
            let stage = download::Stage::create(&staging).unwrap();
            assert_eq!(
                download::prepare_with_client(
                    &candidate,
                    &stage,
                    Arc::new(AtomicBool::new(false)),
                    Arc::new(|_| Ok(())),
                    &client,
                )
                .await
                .unwrap_err()
                .message,
                message
            );
            drop(stage);
            assert_eq!(fs::metadata(&cached).unwrap().len(), 0);
        }
        // The same URL now serves the real archive; retry must fetch it again.
        let stage = download::Stage::create(&staging).unwrap();
        let request = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap();
        assert_eq!(
            fs::read(request.prepared.join("Contents/MacOS/fixture")).unwrap(),
            b"101",
            "{scenario}"
        );
        assert!(candidate.snapshot.still(&installed));
        assert_eq!(fs::read(&cached).unwrap(), bytes, "{scenario}");
        drop(stage);
        server.kill().await.unwrap();
        server.wait().await.unwrap();
        // Valid content remains reusable even without a remote digest or ETag check.
        let stage = download::Stage::create(&staging).unwrap();
        download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap();
        let requests: Vec<serde_json::Value> =
            serde_json::from_slice(&fs::read(file.with_extension("zip.requests")).unwrap())
                .unwrap();
        assert_eq!(requests.len(), 2, "{scenario}");
        assert!(requests.iter().all(|r| r["range"].is_null()));
    }
}
struct RejectedDownload {
    reason: &'static str,
}
impl gate::Inspector for RejectedDownload {
    fn inspect(&self, path: &Path) -> Result<gate::Identity, AppError> {
        let mut identity = gate::Inspector::inspect(&Observations, path)?;
        if fs::read(path.join("Contents/MacOS/fixture")).unwrap() == b"bad-signature" {
            match self.reason {
                // Exercise the actual codesign error, including its command diagnostics.
                "codesign" => return gate::Inspector::inspect(&gate::NativeInspector, path),
                "signature" => identity.valid = false,
                "team" => identity.team_id = "OTHERTEAM1".into(),
                "notarization" => identity.notarized = false,
                "requirement" => {}
                _ => unreachable!(),
            }
        }
        Ok(identity)
    }
    fn satisfies(&self, path: &Path, _: &str) -> Result<bool, AppError> {
        Ok(self.reason != "requirement"
            || fs::read(path.join("Contents/MacOS/fixture")).unwrap() != b"bad-signature")
    }
}

#[tokio::test]
async fn vendor_signature_rejection_discards_cache_and_retries_the_same_url() {
    use opennavo_desktop_lib::updater::release::{Artifact, File};
    for (reason, code) in [
        ("codesign", "E_UPDATE_SIGNATURE"),
        ("signature", "E_UPDATE_SIGNATURE"),
        ("team", "E_UPDATE_TEAM_MISMATCH"),
        ("requirement", "E_UPDATE_TEAM_MISMATCH"),
        ("notarization", "E_UPDATE_NOT_NOTARIZED"),
    ] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let source = root.join("Source.app");
        bundle(&installed, "100");
        bundle(&source, "101");
        let bytes = zip_bytes(&source);
        let file = root.join("Fixture.zip");
        fs::write(&file, &bytes).unwrap();
        // Both responses have the expected bundle version, but the first has bad code.
        fs::write(source.join("Contents/MacOS/fixture"), b"bad-signature").unwrap();
        let invalid = zip_bytes(&source);
        fs::write(file.with_extension("zip.invalid"), &invalid).unwrap();
        let (mut server, url, client) = server(&file, "invalid_first").await;
        let candidate = Candidate {
            item: serde_json::from_value(serde_json::json!({
                "kind": "link", "token": "", "name": "Fixture", "source": "vendor",
                "appId": "fixture", "installedVersion": "100", "currentVersion": "101",
                "autoUpdates": false, "pinned": false, "ignored": false, "dependents": 0,
                "major": false, "requiresAdmin": false, "running": false,
                "relaunchPending": false, "criticalUpdate": false, "expectedTeamMatch": true
            }))
            .unwrap(),
            artifact: Some(Artifact {
                file: File {
                    url,
                    name: "Fixture.zip".into(),
                    size: None,
                    architecture: None,
                    sha256: None,
                    sha512: None,
                },
                version: "101".into(),
                version_key: "CFBundleVersion".into(),
            }),
            app_path: installed.clone(),
            snapshot: Snapshot::capture(&installed).unwrap(),
            feed_item: None,
            public_key: None,
            expected_team: Some("TESTTEAM01".into()),
            checked_at: 0,
        };
        let inspector = RejectedDownload { reason };
        let staging = root.join("staging");
        let stage = download::Stage::create(&staging).unwrap();
        let request = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap();
        let error = stage
            .validate(
                &request,
                &inspector,
                gate::Platform {
                    os: "15.0",
                    arch: "arm64",
                },
            )
            .unwrap_err();
        assert_eq!(error.code, code, "{reason}");
        if reason == "codesign" {
            assert_eq!(error.message, "invalid_bundle_signature");
            assert!(error.detail.is_some());
        }
        let cache = fs::read_dir(staging.join("downloads"))
            .unwrap()
            .next()
            .unwrap()
            .unwrap()
            .path();
        let cached = cache.join("partial");
        assert_eq!(fs::metadata(&cached).unwrap().len(), 0, "{reason}");
        let meta: serde_json::Value =
            serde_json::from_slice(&fs::read(cache.join("metadata.json")).unwrap()).unwrap();
        assert!(meta["verified_sha256"].is_null(), "{reason}");
        assert!(candidate.snapshot.still(&installed));
        drop(stage);

        // The vendor fixes the same URL. The retry must fetch the second response.
        let stage = download::Stage::create(&staging).unwrap();
        let request = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap();
        stage
            .validate(
                &request,
                &inspector,
                gate::Platform {
                    os: "15.0",
                    arch: "arm64",
                },
            )
            .unwrap();
        // An installation failure after passing the gate retains the good download.
        assert_eq!(
            swap::apply(
                &request,
                &swap::Context {
                    inspector: &inspector,
                    files: &TempFiles {
                        fail_register: true
                    },
                    os: "15.0",
                    arch: "arm64",
                },
            )
            .unwrap_err()
            .code,
            "E_UPDATE_SWAP"
        );
        assert_eq!(fs::read(&cached).unwrap(), bytes, "{reason}");
        assert!(candidate.snapshot.still(&installed));
        drop(stage);
        server.kill().await.unwrap();
        server.wait().await.unwrap();

        let stage = download::Stage::create(&staging).unwrap();
        let request = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await
        .unwrap();
        stage
            .validate(
                &request,
                &inspector,
                gate::Platform {
                    os: "15.0",
                    arch: "arm64",
                },
            )
            .unwrap();
        let requests: Vec<serde_json::Value> =
            serde_json::from_slice(&fs::read(file.with_extension("zip.requests")).unwrap())
                .unwrap();
        assert_eq!(requests.len(), 2, "{reason}");
        assert!(requests.iter().all(|r| r["range"].is_null()));
    }
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
            Err(AppError::new("E_UNKNOWN", "fixture_installer_exit")
                .with_detail(r#"{"exitCode":7,"stderr":"fixture installer refused"}"#))
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
            unsigned: None,
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
            let error = result.unwrap_err();
            assert_eq!(error.code, "E_UPDATE_SWAP");
            let detail = error.detail.unwrap();
            assert!(detail.contains("fixture_installer_exit"));
            assert!(detail.contains("fixture installer refused"));
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
                "E_UPDATE_DIGEST"
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
            unsigned: None,
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

#[tokio::test]
async fn verified_download_retries_recheck_trust_and_cache_lifetime() {
    for scenario in [
        "retry",
        "tampered",
        "expired",
        "bad_signature",
        "version_mismatch",
        "changed_signature",
        "no_validator",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let source = root.join("Source.app");
        bundle(&installed, "100");
        bundle(&source, "101");
        let bytes = zip_bytes(&source);
        let file = root.join("Fixture.zip");
        fs::write(&file, &bytes).unwrap();
        let (mut server, url, client) = server(
            &file,
            if scenario == "no_validator" {
                "no_validator"
            } else {
                "normal"
            },
        )
        .await;
        let key = Ed25519KeyPair::from_seed_unchecked(&[23; 32]).unwrap();
        let signature = STANDARD.encode(key.sign(&bytes).as_ref());
        let xml = format!(
            r#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>101</s:version><enclosure url="{url}" length="{}" s:edSignature="{signature}"/></item></channel></rss>"#,
            bytes.len()
        );
        let item = serde_json::from_value(serde_json::json!({"kind":"cask","token":"","name":"Fixture","source":"sparkle","appId":"fixture","installedVersion":"1.0","currentVersion":"2.0","autoUpdates":false,"pinned":false,"ignored":false,"dependents":0,"major":false,"requiresAdmin":false,"running":false,"relaunchPending":false,"criticalUpdate":false})).unwrap();
        let mut candidate = Candidate {
            artifact: None,
            item,
            app_path: installed.clone(),
            snapshot: Snapshot::capture(&installed).unwrap(),
            feed_item: Some(feed::parse(xml.as_bytes()).unwrap().remove(0)),
            public_key: Some(STANDARD.encode(key.public_key().as_ref())),
            expected_team: Some("TESTTEAM01".into()),
            checked_at: 0,
        };
        if scenario == "bad_signature" {
            candidate.feed_item.as_mut().unwrap().ed_signature = Some(STANDARD.encode([0; 64]));
        }
        if scenario == "version_mismatch" {
            candidate.feed_item.as_mut().unwrap().version = "102".into();
        }
        let staging = root.join("staging");
        let stage = download::Stage::create(&staging).unwrap();
        let result = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await;
        if scenario == "bad_signature" {
            assert_eq!(result.unwrap_err().message, "eddsa_verification");
        } else if scenario == "version_mismatch" {
            assert_eq!(result.unwrap_err().code, "E_UPDATE_VERSION");
        } else {
            let request = result.unwrap();
            // A later installation failure must not destroy the verified download.
            assert_eq!(
                swap::apply(
                    &request,
                    &swap::Context {
                        inspector: &Observations,
                        files: &TempFiles {
                            fail_register: true
                        },
                        os: "15.0",
                        arch: "arm64"
                    }
                )
                .unwrap_err()
                .code,
                "E_UPDATE_SWAP"
            );
            let concurrent = download::Stage::create(&staging).unwrap();
            assert_eq!(
                download::prepare_with_client(
                    &candidate,
                    &concurrent,
                    Arc::new(AtomicBool::new(false)),
                    Arc::new(|_| Ok(())),
                    &client
                )
                .await
                .unwrap_err()
                .code,
                "E_BUSY"
            );
        }
        let stage_path = stage.path.clone();
        drop(stage);
        assert!(!stage_path.exists());
        if scenario == "retry" {
            let canceled = download::Stage::create(&staging).unwrap();
            assert_eq!(
                download::prepare_with_client(
                    &candidate,
                    &canceled,
                    Arc::new(AtomicBool::new(true)),
                    Arc::new(|_| Ok(())),
                    &client
                )
                .await
                .unwrap_err()
                .code,
                "E_INTERRUPTED"
            );
            assert!(!canceled.path.join("archive").exists());
        }
        let cache_dir = fs::read_dir(staging.join("downloads"))
            .unwrap()
            .next()
            .unwrap()
            .unwrap()
            .path();
        let cached = cache_dir.join("partial");
        if matches!(scenario, "bad_signature" | "version_mismatch") {
            assert_eq!(fs::metadata(&cached).unwrap().len(), 0);
        }
        if scenario == "tampered" {
            fs::write(&cached, vec![0; bytes.len()]).unwrap();
        }
        if scenario == "expired" {
            fs::File::options()
                .write(true)
                .open(&cached)
                .unwrap()
                .set_times(fs::FileTimes::new().set_modified(
                    std::time::SystemTime::now() - std::time::Duration::from_secs(8 * 86400),
                ))
                .unwrap();
        }
        if scenario == "changed_signature" {
            candidate.feed_item.as_mut().unwrap().ed_signature = Some(STANDARD.encode([0; 64]));
        }
        let stage = download::Stage::create(&staging).unwrap();
        // Even an offline retry can use a verified cached artifact.
        let offline = matches!(scenario, "retry" | "no_validator");
        if offline {
            server.kill().await.unwrap();
            server.wait().await.unwrap();
        }
        let result = download::prepare_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
        )
        .await;
        match scenario {
            "bad_signature" | "changed_signature" => {
                assert_eq!(result.unwrap_err().message, "eddsa_verification")
            }
            "version_mismatch" => assert_eq!(result.unwrap_err().code, "E_UPDATE_VERSION"),
            _ => {
                result.unwrap();
                stage.discard_download().unwrap();
                assert_eq!(fs::metadata(&cached).unwrap().len(), 0);
            }
        }
        let requests: Vec<serde_json::Value> =
            serde_json::from_slice(&fs::read(file.with_extension("zip.requests")).unwrap())
                .unwrap();
        assert_eq!(requests.len(), if offline { 1 } else { 2 }, "{scenario}");
        assert!(candidate.snapshot.still(&installed));
        if !offline {
            server.kill().await.unwrap();
            server.wait().await.unwrap();
        }
    }
}

// Real Mach-O/signature fixtures: an ad-hoc Electron bundle can have a broken
// resource seal (as LX Music does) even when its published archive hash matches.
fn unsigned_bundle(path: &Path, version: &str, adhoc: bool) {
    bundle(path, version);
    let file = path.join("Contents/Info.plist");
    let mut info = plist::Value::from_file(&file).unwrap();
    info.as_dictionary_mut()
        .unwrap()
        .insert("CFBundleShortVersionString".into(), version.into());
    info.to_file_xml(file).unwrap();
    let executable = path.join("Contents/MacOS/fixture");
    assert!(
        std::process::Command::new("/usr/bin/lipo")
            .args(["/usr/bin/true", "-thin", "x86_64", "-output"])
            .arg(&executable)
            .status()
            .unwrap()
            .success()
    );
    let status = std::process::Command::new("/usr/bin/codesign")
        .arg("--remove-signature")
        .arg(&executable)
        .status()
        .unwrap();
    assert!(status.success());
    if adhoc {
        assert!(
            std::process::Command::new("/usr/bin/codesign")
                .args(["--force", "--sign", "-"])
                .arg(path)
                .status()
                .unwrap()
                .success()
        );
        fs::remove_file(path.join("Contents/_CodeSignature/CodeResources")).unwrap();
    }
}

fn unsigned_candidate(installed: &Path, url: String, bytes: &[u8]) -> Candidate {
    use opennavo_desktop_lib::updater::release::{Artifact, File};
    use sha2::{Digest, Sha512};
    let mut candidate = Candidate {
        item: serde_json::from_value(serde_json::json!({
            "kind":"link", "token":"fixture", "name":"Fixture", "source":"electron",
            "appId":"fixture", "bundleId":"test.opennavo.Fixture", "installedVersion":"1.0.0",
            "currentVersion":"1.1.0", "autoUpdates":false, "pinned":false, "ignored":false,
            "dependents":0, "major":false, "requiresAdmin":false, "running":false,
            "relaunchPending":false, "criticalUpdate":false
        }))
        .unwrap(),
        artifact: Some(Artifact {
            version: "1.1.0".into(),
            version_key: "CFBundleVersion".into(),
            file: File {
                url,
                name: "Fixture.zip".into(),
                size: Some(bytes.len() as u64),
                architecture: Some("universal".into()),
                sha256: None,
                sha512: Some(STANDARD.encode(Sha512::digest(bytes))),
            },
        }),
        app_path: installed.into(),
        snapshot: Snapshot::capture(installed).unwrap(),
        feed_item: None,
        public_key: None,
        expected_team: None,
        checked_at: 0,
    };
    candidate.item.unsigned_update = Some(candidate.unsigned_key().unwrap());
    candidate
}

#[test]
fn unsigned_consent_is_bound_to_release_path_identity_and_snapshot() {
    let tmp = tempfile::tempdir().unwrap();
    let root = tmp.path().canonicalize().unwrap();
    let installed = root.join("Fixture.app");
    bundle(&installed, "1.0.0");
    let candidate =
        unsigned_candidate(&installed, "https://example.org/Fixture.zip".into(), b"zip");
    let key = candidate.item.unsigned_update.as_deref();
    candidate.validate_unsigned_confirmation(key).unwrap();
    assert!(candidate.validate_unsigned_confirmation(None).is_err());
    assert!(
        candidate
            .validate_unsigned_confirmation(Some("old-consent"))
            .is_err()
    );
    for field in ["url", "digest", "path", "id", "team", "source", "snapshot"] {
        let mut changed = candidate.clone();
        match field {
            "url" => changed
                .artifact
                .as_mut()
                .unwrap()
                .file
                .url
                .push_str("?different"),
            "digest" => {
                changed.artifact.as_mut().unwrap().file.sha512 = Some(STANDARD.encode([0; 64]))
            }
            "path" => changed.app_path = tmp.path().join("Another.app"),
            "id" => changed.item.bundle_id = Some("other.bundle".into()),
            "team" => changed.expected_team = Some("TESTTEAM01".into()),
            "source" => changed.item.source = opennavo_desktop_lib::model::UpdateSource::Vendor,
            "snapshot" => changed.snapshot = Snapshot::capture(&root).unwrap(),
            _ => unreachable!(),
        }
        assert!(
            changed.validate_unsigned_confirmation(key).is_err(),
            "{field}"
        );
    }
}

struct UnsignedFiles<'a> {
    scenario: &'a str,
}
impl swap::Files for UnsignedFiles<'_> {
    fn copy_bundle(&self, from: &Path, to: &Path) -> Result<(), AppError> {
        swap::NativeFiles.copy_bundle(from, to)?;
        if (self.scenario == "private_tamper" && to.ends_with("replacement.app"))
            || (self.scenario == "backup_tamper" && to.ends_with("previous.app"))
        {
            fs::write(to.join("Contents/changed"), "tampered copy")?;
        }
        Ok(())
    }
    fn exchange(&self, a: &Path, b: &Path) -> Result<(), AppError> {
        swap::NativeFiles.exchange(a, b)
    }
    fn register(&self, path: &Path) -> Result<(), AppError> {
        TempFiles {
            fail_register: self.scenario == "rollback",
        }
        .register(path)
    }
    fn clear_quarantine(&self, _: &Path) -> Result<(), AppError> {
        panic!("unsigned installation must preserve quarantine");
    }
}

#[tokio::test]
async fn confirmed_unsigned_download_keeps_hash_gate_and_atomic_recovery() {
    for scenario in [
        "unsigned",
        "adhoc",
        "hash",
        "id",
        "version",
        "arch",
        "minimum",
        "signed",
        "tamper",
        "private_tamper",
        "backup_tamper",
        "rollback",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let installed = root.join("Installed.app");
        let source = root.join("Source.app");
        unsigned_bundle(&installed, "1.0.0", scenario == "adhoc");
        unsigned_bundle(&source, "1.1.0", scenario == "adhoc");
        if scenario == "signed" {
            fs::copy("/usr/bin/true", source.join("Contents/MacOS/fixture")).unwrap();
        }
        if ["id", "version", "minimum"].contains(&scenario) {
            let path = source.join("Contents/Info.plist");
            let mut info = plist::Value::from_file(&path).unwrap();
            let (key, value) = match scenario {
                "id" => ("CFBundleIdentifier", "wrong.bundle"),
                "version" => ("CFBundleVersion", "0.9.0"),
                _ => ("LSMinimumSystemVersion", "99.0"),
            };
            info.as_dictionary_mut()
                .unwrap()
                .insert(key.into(), value.into());
            info.to_file_xml(path).unwrap();
        }
        let bytes = zip_bytes(&source);
        let archive = root.join("Fixture.zip");
        fs::write(&archive, &bytes).unwrap();
        let (mut server, url, client) = server(&archive, "normal").await;
        let mut candidate = unsigned_candidate(&installed, url, &bytes);
        if scenario == "hash" {
            candidate.artifact.as_mut().unwrap().file.sha512 = Some(STANDARD.encode([0; 64]));
            candidate.item.unsigned_update = Some(candidate.unsigned_key().unwrap());
        }
        let stage = download::Stage::create(&root.join("staging")).unwrap();
        // No approval cannot even start downloading.
        assert_eq!(
            download::prepare_with_client(
                &candidate,
                &stage,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_| Ok(())),
                &client
            )
            .await
            .unwrap_err()
            .message,
            "unsigned_confirmation_required"
        );
        let result = download::prepare_confirmed_with_client(
            &candidate,
            &stage,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_| Ok(())),
            &client,
            candidate.item.unsigned_update.as_deref(),
        )
        .await;
        if ["hash", "version"].contains(&scenario) {
            assert_eq!(
                result.unwrap_err().code,
                if scenario == "hash" {
                    "E_UPDATE_DIGEST"
                } else {
                    "E_UPDATE_VERSION"
                }
            );
        } else {
            let request = result.unwrap();
            if scenario == "tamper" {
                fs::write(
                    request.prepared.join("Contents/extra"),
                    "changed after verification",
                )
                .unwrap();
            }
            let result = swap::apply(
                &request,
                &swap::Context {
                    inspector: &gate::NativeInspector,
                    files: &UnsignedFiles { scenario },
                    os: "98.0",
                    arch: if scenario == "arch" {
                        "unsupported"
                    } else {
                        "x86_64"
                    },
                },
            );
            if ["unsigned", "adhoc"].contains(&scenario) {
                let backup = result.unwrap();
                assert_eq!(
                    plist::Value::from_file(backup.join("Contents/Info.plist"))
                        .unwrap()
                        .as_dictionary()
                        .unwrap()["CFBundleVersion"]
                        .as_string(),
                    Some("1.0.0")
                );
            } else {
                assert!(result.is_err(), "{scenario}");
                assert_eq!(
                    plist::Value::from_file(installed.join("Contents/Info.plist"))
                        .unwrap()
                        .as_dictionary()
                        .unwrap()["CFBundleVersion"]
                        .as_string(),
                    Some("1.0.0")
                );
            }
        }
        server.kill().await.unwrap();
        server.wait().await.unwrap();
    }
}

#[test]
fn native_reinstall_and_downgrade_consent_is_bound_to_the_exact_artifact_and_tree() {
    use opennavo_desktop_lib::updater::{
        UpdateConsent, UpdateSource, UpdateWarning, unsigned::Approval,
    };
    for source in [
        UpdateSource::Electron,
        UpdateSource::Sparkle,
        UpdateSource::Github,
        UpdateSource::Vendor,
    ] {
        for current in ["1.1.0", "2.0.0", "0.9.0"] {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().canonicalize().unwrap();
            let installed = root.join("Installed.app");
            let prepared = root.join("Prepared.app");
            unsigned_bundle(&installed, current, false);
            unsigned_bundle(&prepared, "1.1.0", false);
            if current == "0.9.0" {
                // A growing build number does not hide a marketing-version downgrade.
                for (path, version) in [(&installed, "2.0.0"), (&prepared, "1.0.0")] {
                    let file = path.join("Contents/Info.plist");
                    let mut info = plist::Value::from_file(&file).unwrap();
                    info.as_dictionary_mut()
                        .unwrap()
                        .insert("CFBundleShortVersionString".into(), version.into());
                    info.to_file_xml(file).unwrap();
                }
            }
            let mut candidate = unsigned_candidate(
                &installed,
                "https://example.org/Fixture.zip".into(),
                b"verified archive",
            );
            candidate.item.source = source;
            if source == UpdateSource::Sparkle {
                candidate.artifact = None;
                candidate.public_key = Some(STANDARD.encode([1; 32]));
                candidate.feed_item = Some(feed::parse(br#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>1.1.0</s:version><enclosure url="https://example.org/Fixture.zip" s:edSignature="fixture"/></item></channel></rss>"#).unwrap().remove(0));
            }
            let request = swap::SwapRequest {
                source,
                installed: installed.clone(),
                prepared: prepared.clone(),
                installed_snapshot: Snapshot::capture(&installed).unwrap(),
                prepared_snapshot: Snapshot::capture(&prepared).unwrap(),
                expected_team: None,
                unsigned: None,
                package: None,
            };
            let platform = || gate::Platform {
                os: "98.0",
                arch: "x86_64",
            };
            let mut unapproved = request.clone();
            let error =
                Approval::review(&candidate, &mut unapproved, None, platform()).unwrap_err();
            assert_eq!(error.code, "E_UPDATE_CONFIRMATION");
            let consent: UpdateConsent =
                serde_json::from_str(error.detail.as_deref().unwrap()).unwrap();
            assert_eq!(
                consent.warnings,
                vec![UpdateWarning::UnsignedDeveloper, UpdateWarning::NonNewer]
            );
            let mut changed = candidate.clone();
            changed.item.bundle_id = Some("other.app".into());
            assert_eq!(
                Approval::review(&changed, &mut request.clone(), Some(&consent), platform())
                    .unwrap_err()
                    .code,
                "E_UPDATE_IDENTITY"
            );
            fs::write(prepared.join("Contents/after-review"), "changed").unwrap();
            assert_eq!(
                Approval::review(&candidate, &mut request.clone(), Some(&consent), platform())
                    .unwrap_err()
                    .code,
                "E_UPDATE_CONFIRMATION"
            );
            fs::remove_file(prepared.join("Contents/after-review")).unwrap();
            let mut early = request.clone();
            early.unsigned = Some(Approval::prepare(&candidate, &prepared).unwrap());
            fs::write(prepared.join("Contents/after-download"), "changed").unwrap();
            assert_eq!(
                Approval::review(&candidate, &mut early, Some(&consent), platform())
                    .unwrap_err()
                    .code,
                "E_UPDATE_CHANGED"
            );
            fs::remove_file(prepared.join("Contents/after-download")).unwrap();
            let mut approved = request;
            Approval::review(&candidate, &mut approved, Some(&consent), platform()).unwrap();
            swap::apply(
                &approved,
                &swap::Context {
                    inspector: &gate::NativeInspector,
                    files: &UnsignedFiles {
                        scenario: "unsigned",
                    },
                    os: "98.0",
                    arch: "x86_64",
                },
            )
            .unwrap();
            assert_eq!(
                plist::Value::from_file(installed.join("Contents/Info.plist"))
                    .unwrap()
                    .as_dictionary()
                    .unwrap()["CFBundleVersion"]
                    .as_string(),
                Some("1.1.0")
            );
        }
    }
}
