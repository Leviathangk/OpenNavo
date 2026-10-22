use opennavo_desktop_lib::{appstore::install::Runner, model::TaskOp, updater::download::Stage};
use std::{
    collections::BTreeMap,
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
fn runner(scenario: &str) -> Runner {
    Runner {
        executable: PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fake-store/store"),
        environment: BTreeMap::from([
            ("OPENNAVO_FAKE_STORE_SCENARIO".into(), scenario.into()),
            ("OPENNAVO_FAKE_STORE_FIXTURES".into(), "1".into()),
        ]),
    }
}
#[tokio::test]
async fn fake_store_success_and_201_retain_snapshots_without_root_actions() {
    for scenario in ["success", "201"] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let results = runner(scenario)
            .download(
                TaskOp::Install,
                &[42, 43],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_, _, _| Ok(())),
            )
            .await
            .unwrap();
        assert_eq!(results.len(), 2);
        for result in results {
            assert!(result.error.is_none());
            assert_eq!(result.privileged.is_some(), scenario == "201");
            if let Some(request) = result.privileged {
                request.validate(unsafe { nix::libc::getuid() }).unwrap();
                std::fs::write(&request.pkg, b"tampered").unwrap();
                assert!(request.validate(unsafe { nix::libc::getuid() }).is_err());
            }
        }
    }
}
#[tokio::test]
async fn paths_the_helper_respells_still_resolve_to_files_inside_staging() {
    // The real helper reports /var instead of /private/var and decomposed accents; the staging
    // folder's own spelling must not decide whether the download counts.
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stag\u{e9}s")).unwrap();
    let results = runner("201_respelled")
        .download(
            TaskOp::Upgrade,
            &[42],
            &stage.path,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_, _, _| Ok(())),
        )
        .await
        .unwrap();
    let request = results[0].privileged.as_ref().expect("privileged install");
    assert_eq!(request.pkg.parent(), Some(stage.path.as_path()));
    assert_eq!(request.receipt.parent(), Some(stage.path.as_path()));
    request.validate(unsafe { nix::libc::getuid() }).unwrap();
}
#[tokio::test]
async fn password_dialog_calls_a_store_update_an_update() {
    use opennavo_desktop_lib::{
        brew::env::Reason,
        updater::admin::{Action, Request, password_reason},
    };
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    let mut results = runner("201")
        .download(
            TaskOp::Upgrade,
            &[42],
            &stage.path,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_, _, _| Ok(())),
        )
        .await
        .unwrap();
    let mut request = results.remove(0).privileged.unwrap();
    let ask = |request: &opennavo_desktop_lib::appstore::install::PrivilegedInstall| {
        password_reason(&[Request {
            id: "task".into(),
            action: Action::MasInstallPkg {
                request: Box::new(request.clone()),
            },
        }])
    };
    // Nothing is installed at the destination yet.
    assert_eq!(ask(&request), (Reason::Install, Some("Fixture".into())));
    // The app is already there, so the same package replaces it.
    request.app_snapshot =
        Some(opennavo_desktop_lib::uninstall::rules::Snapshot::capture(&stage.path).unwrap());
    assert_eq!(ask(&request), (Reason::Update, Some("Fixture".into())));
}
#[tokio::test]
async fn fake_store_reports_typed_errors_and_bounds_unterminated_lines() {
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    for code in [
        "E_MAS_NOT_SIGNED_IN",
        "E_MAS_NOT_OWNED",
        "E_MAS_PAID",
        "E_MAS_OTHER_ACCOUNT",
        "E_MAS_REGION",
        "E_MAS_RATE_LIMITED",
        "E_MAS_ENGINE",
    ] {
        let results = runner(&format!("error:{code}"))
            .download(
                TaskOp::Upgrade,
                &[42],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_, _, _| Ok(())),
            )
            .await
            .unwrap();
        assert_eq!(results[0].error.as_ref().unwrap().code, code);
    }
    let error = runner("oversized")
        .download(
            TaskOp::Install,
            &[42],
            &stage.path,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_, _, _| Ok(())),
        )
        .await
        .unwrap_err();
    assert_eq!(error.message, "store_protocol_line_size");
}
#[tokio::test]
async fn closing_stdin_cancels_owned_store_child_promptly() {
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    let cancel = Arc::new(AtomicBool::new(false));
    let signal = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(100)).await;
        signal.store(true, Ordering::Release);
    });
    let result = tokio::time::timeout(
        Duration::from_secs(5),
        runner("cancel").download(
            TaskOp::Install,
            &[42],
            &stage.path,
            cancel,
            Arc::new(|_, _, _| Ok(())),
        ),
    )
    .await
    .unwrap();
    let items = result.unwrap();
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].adam_id, 42);
    assert!(items[0].privileged.is_none());
    assert_eq!(items[0].error.as_ref().unwrap().code, "E_INTERRUPTED");
}

#[test]
fn apple_signed_packages_are_still_scoped_to_the_requested_app() {
    use opennavo_desktop_lib::updater::admin::store_payload_scope;
    let info = br#"<pkg-info install-location="/Applications"><bundle-version><bundle id="test.opennavo.Fixture" path="Fixture.app"/></bundle-version></pkg-info>"#;
    let app = std::path::Path::new("/Applications/Fixture.app");
    let payload = "./Fixture.app\n./Fixture.app/Contents\n./Fixture.app/Contents/MacOS/fixture\n";
    assert!(store_payload_scope(info, payload, app, "test.opennavo.Fixture").is_ok());
    for bad in [
        "./Other.app/Contents/file",
        "./Fixture.app/../Other.app/Contents/file",
        "/Applications/Fixture.app/Contents/file",
        "./Fixture.app",
    ] {
        assert!(store_payload_scope(info, bad, app, "test.opennavo.Fixture").is_err());
    }
    assert!(store_payload_scope(info, payload, app, "test.other.App").is_err());
    let scripts = br#"<pkg-info install-location="/Applications"><scripts/><bundle id="test.opennavo.Fixture"/></pkg-info>"#;
    assert!(store_payload_scope(scripts, payload, app, "test.opennavo.Fixture").is_err());
}

fn jsonl_runner(root: &std::path::Path, percent: Option<&str>) -> Runner {
    use std::os::unix::fs::PermissionsExt;
    let field = percent
        .map(|raw| format!(",\"percent\":{raw}"))
        .unwrap_or_default();
    // Emit literal JSONL so exponent spelling, null, missing and malformed values reach the real decoder.
    let script = format!(
        "#!/bin/sh\ncat <<'OPENNAVO_PROTOCOL_FIXTURE'\n{{\"event\":\"phase\",\"adamId\":42,\"phase\":\"downloading\"{field}}}\n{{\"event\":\"phase\",\"adamId\":42,\"phase\":\"done\",\"percent\":100}}\nOPENNAVO_PROTOCOL_FIXTURE\n"
    );
    let executable = root.join("jsonl-store");
    std::fs::write(&executable, script).unwrap();
    std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
    Runner {
        executable,
        environment: BTreeMap::new(),
    }
}

#[tokio::test]
async fn runner_delivers_integer_decimal_exponent_and_unknown_progress() {
    for (raw, expected) in [
        (Some("50"), Some(50.0)),
        (Some("50.0"), Some(50.0)),
        (Some("37.5"), Some(37.5)),
        (Some("3.75e1"), Some(37.5)),
        (Some("null"), None),
        (None, None),
    ] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
        let progress = seen.clone();
        let results = jsonl_runner(dir.path(), raw)
            .download(
                TaskOp::Install,
                &[42],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(move |id, phase, percent| {
                    progress
                        .lock()
                        .unwrap()
                        .push((id, phase.to_owned(), percent));
                    Ok(())
                }),
            )
            .await
            .unwrap();
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].adam_id, 42);
        assert!(results[0].error.is_none() && results[0].privileged.is_none());
        assert_eq!(
            *seen.lock().unwrap(),
            vec![
                (42, "downloading".into(), expected),
                (42, "done".into(), Some(100.0))
            ],
            "raw={raw:?}"
        );
    }
}

#[tokio::test]
async fn runner_rejects_invalid_progress_before_publishing_or_finishing() {
    for raw in [
        "-1",
        "100.1",
        "100.000001",
        "1e39",
        "1e999",
        "-1e999",
        "NaN",
        "Infinity",
        "\"NaN\"",
        "\"37.5\"",
        "true",
        "[]",
        "{}",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let called = Arc::new(AtomicBool::new(false));
        let progress = called.clone();
        let error = jsonl_runner(dir.path(), Some(raw))
            .download(
                TaskOp::Install,
                &[42],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(move |_, _, _| {
                    progress.store(true, Ordering::Release);
                    Ok(())
                }),
            )
            .await
            .unwrap_err();
        assert_eq!(error.code, "E_MAS_ENGINE", "raw={raw}");
        assert!(
            !called.load(Ordering::Acquire),
            "published invalid progress {raw}"
        );
    }
}

#[tokio::test]
async fn signal_exit_preserves_completed_items_and_fails_unfinished_items() {
    let dir = tempfile::tempdir().unwrap();
    let stage = Stage::create(&dir.path().join("stages")).unwrap();
    let items = runner("partial_signal")
        .download(
            TaskOp::Install,
            &[42, 43],
            &stage.path,
            Arc::new(AtomicBool::new(false)),
            Arc::new(|_, _, _| Ok(())),
        )
        .await
        .unwrap();
    assert_eq!(items.len(), 2);
    assert_eq!(items[0].adam_id, 42);
    assert!(items[0].error.is_none() && items[0].privileged.is_none());
    assert_eq!(items[1].adam_id, 43);
    assert_eq!(
        items[1].error.as_ref().unwrap().message,
        "store_exit_failed"
    );
    assert!(items[1].privileged.is_none());
}

#[tokio::test]
async fn item_failure_preserves_other_privileged_requests_but_abnormal_exit_does_not() {
    for scenario in [
        "201_then_error",
        "201_then_error_signal",
        "201_then_incomplete",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let stage = Stage::create(&dir.path().join("stages")).unwrap();
        let items = runner(scenario)
            .download(
                TaskOp::Install,
                &[42, 43],
                &stage.path,
                Arc::new(AtomicBool::new(false)),
                Arc::new(|_, _, _| Ok(())),
            )
            .await
            .unwrap();
        assert_eq!(items.len(), 2);
        assert_eq!(items[0].adam_id, 42);
        assert_eq!(items[1].adam_id, 43);
        if scenario == "201_then_error" {
            assert!(items[0].error.is_none());
            items[0]
                .privileged
                .as_ref()
                .unwrap()
                .validate(unsafe { nix::libc::getuid() })
                .unwrap();
        } else {
            assert!(items[0].privileged.is_none());
            assert_eq!(
                items[0].error.as_ref().unwrap().message,
                "store_exit_failed"
            );
        }
        if scenario == "201_then_incomplete" {
            assert_eq!(
                items[1].error.as_ref().unwrap().message,
                "store_exit_failed"
            );
        } else {
            assert_eq!(items[1].error.as_ref().unwrap().code, "E_MAS_NOT_OWNED");
        }
    }
}

/// `pkgutil --check-signature` for a package downloaded from the Mac App Store (macOS 27).
const APP_STORE_SIGNATURE: &str = r#"Package "store.pkg":
   Status: signed by Apple for the App Store
   Signed with a trusted timestamp on: 2026-10-16 09:01:38 +0000
   Certificate Chain:
    1. Apple Mac OS Installer Package Signing
       Expires: 2028-07-25 18:55:33 +0000
       SHA256 Fingerprint:
           95 9B 96 CE F0 DC 7F 8C EF 53 13 67 A3 39 4E 11 C4 A1 26 43 DB 8B
           9F BC 90 95 06 7E 24 80 FB BA
       ------------------------------------------------------------------------
    2. Apple Worldwide Developer Relations Certification Authority
       Expires: 2030-12-10 00:00:00 +0000
       SHA256 Fingerprint:
           53 FD 00 82 78 E5 A5 95 FE 1E 90 8A E9 C5 E5 67 5F 26 24 32 64 A5
           A6 43 8C 02 3E 3C E2 87 07 60
       ------------------------------------------------------------------------
    3. Apple Root CA
       Expires: 2035-02-09 21:40:36 +0000
       SHA256 Fingerprint:
           B0 B1 73 0E CB C7 FF 45 05 14 2C 49 F1 29 5E 6E DA 6B CA ED 7E 2C
           68 C5 BE 91 B5 A1 10 01 F0 24"#;

#[test]
fn app_store_signature_gate_accepts_only_apples_store_installer_chain() {
    use opennavo_desktop_lib::updater::admin::apple_package_signature;
    assert!(apple_package_signature(APP_STORE_SIGNATURE));
    // Older macOS wording for the same chain.
    assert!(apple_package_signature(&APP_STORE_SIGNATURE.replace(
        "signed by Apple for the App Store",
        "signed by a certificate trusted by macOS"
    )));
    for bad in [
        // Third-party installers and Apple's operating-system packages also chain to Apple.
        APP_STORE_SIGNATURE.replace(
            "1. Apple Mac OS Installer Package Signing",
            "1. Developer ID Installer: Fixture (TESTTEAM01)",
        ),
        APP_STORE_SIGNATURE.replace(
            "1. Apple Mac OS Installer Package Signing",
            "1. 3rd Party Mac Developer Installer: Fixture (TESTTEAM01)",
        ),
        APP_STORE_SIGNATURE.replace(
            "1. Apple Mac OS Installer Package Signing",
            "1. Software Update",
        ),
        APP_STORE_SIGNATURE.replace(
            "2. Apple Worldwide Developer Relations Certification Authority",
            "2. Developer ID Certification Authority",
        ),
        APP_STORE_SIGNATURE.replace("3. Apple Root CA", "3. Fixture Root CA"),
        // The Apple leaf name only counts at the leaf position of a trusted chain.
        APP_STORE_SIGNATURE.replace(
            "1. Apple Mac OS Installer Package Signing",
            "4. Apple Mac OS Installer Package Signing",
        ),
        APP_STORE_SIGNATURE.replace("signed by Apple for the App Store", "no signature"),
        APP_STORE_SIGNATURE.replace(
            "signed by Apple for the App Store",
            "signed by a certificate that is untrusted",
        ),
        "Package \"store.pkg\":\n   Status: no signature".to_owned(),
        String::new(),
    ] {
        assert!(!apple_package_signature(&bad), "{bad}");
    }
}

const DISTRIBUTION: &str = r#"<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<installer-gui-script minSpecVersion="2">
    <pkg-ref id="test.opennavo.Fixture">
        <bundle-version>
            <bundle CFBundleShortVersionString="1.0" CFBundleVersion="1" id="test.opennavo.Fixture" path="Fixture.app"/>
        </bundle-version>
    </pkg-ref>
    <product id="test.opennavo.Fixture" version="1.0"/>
    <title>Fixture &amp; Co</title>
    <options customize="never" require-scripts="false" hostArchitectures="arm64,x86_64"/>
    <volume-check>
        <allowed-os-versions>
            <os-version min="12.0"/>
        </allowed-os-versions>
    </volume-check>
    <choices-outline>
        <line choice="default">
            <line choice="test.opennavo.Fixture"/>
        </line>
    </choices-outline>
    <choice id="default" title="Fixture" versStr="1.0"/>
    <choice id="test.opennavo.Fixture" title="Fixture" visible="false" customLocation="/Applications">
        <pkg-ref id="test.opennavo.Fixture"/>
    </choice>
    <pkg-ref id="test.opennavo.Fixture" version="1.0" onConclusion="none" installKBytes="3" updateKBytes="0">#test.opennavo.Fixture.pkg</pkg-ref>
</installer-gui-script>"#;

#[test]
fn store_distribution_must_stay_declarative_and_name_the_checked_component() {
    use opennavo_desktop_lib::updater::admin::store_distribution_scope;
    let component = "test.opennavo.Fixture.pkg";
    let location = std::path::Path::new("/Applications");
    assert!(store_distribution_scope(DISTRIBUTION.as_bytes(), component, location).is_ok());
    // A trailing slash names the same directory.
    assert!(
        store_distribution_scope(
            DISTRIBUTION.as_bytes(),
            component,
            std::path::Path::new("/Applications/")
        )
        .is_ok()
    );
    let with = |from: &str, to: &str| {
        assert!(DISTRIBUTION.contains(from), "{from}");
        DISTRIBUTION.replacen(from, to, 1)
    };
    for bad in [
        // Installer evaluates JavaScript from these.
        with("<title>", "<script>system.run('/bin/sh')</script><title>"),
        with(
            "<volume-check>",
            "<installation-check script=\"pm_check()\"/><volume-check>",
        ),
        with("<volume-check>", "<volume-check script=\"vc()\">"),
        with("require-scripts=\"false\"", "require-scripts=\"true\""),
        with(
            "require-scripts=\"false\"",
            "allow-external-scripts=\"yes\"",
        ),
        with(
            "visible=\"false\"",
            "start_visible=\"system.run('/bin/sh')\"",
        ),
        with(
            "visible=\"false\"",
            "visible=\"installer.choices.x.selected\"",
        ),
        with("<title>", "<localization><strings/></localization><title>"),
        // Another install location, another component, no component.
        with(
            "customLocation=\"/Applications\"",
            "customLocation=\"/Library/LaunchDaemons\"",
        ),
        with("#test.opennavo.Fixture.pkg", "#Other.pkg"),
        with("#test.opennavo.Fixture.pkg", ""),
        with(
            "#test.opennavo.Fixture.pkg</pkg-ref>",
            "#test.opennavo.Fixture.pkg</pkg-ref><pkg-ref id=\"x\">#Other.pkg</pkg-ref>",
        ),
        with(
            "#test.opennavo.Fixture.pkg",
            "<![CDATA[#test.opennavo.Fixture.pkg]]>",
        ),
        // Markup that is not a plain Distribution.
        with(
            "<installer-gui-script",
            "<!DOCTYPE x [<!ENTITY a \"b\">]><installer-gui-script",
        ),
        with("<title>", "<?run something?><title>"),
        DISTRIBUTION.replace("installer-gui-script", "installer-script"),
        format!("{DISTRIBUTION}{DISTRIBUTION}"),
        DISTRIBUTION.replace("</installer-gui-script>", ""),
        String::new(),
        "<installer-gui-script/>".to_owned(),
    ] {
        assert!(
            store_distribution_scope(bad.as_bytes(), component, location).is_err(),
            "{bad}"
        );
    }
    assert!(store_distribution_scope(DISTRIBUTION.as_bytes(), "Other.pkg", location).is_err());
    assert!(
        store_distribution_scope(
            DISTRIBUTION.as_bytes(),
            component,
            std::path::Path::new("/Applications/Sub")
        )
        .is_err()
    );
    assert!(store_distribution_scope(&vec![b' '; 1024 * 1024 + 1], component, location).is_err());
}

mod store_package {
    use opennavo_desktop_lib::updater::admin::validate_store_package;
    use std::{fs, path::Path, process::Command};

    const BUNDLE: &str = "test.opennavo.Fixture";
    fn run(program: &str, args: &[&Path]) {
        let output = Command::new(program).args(args).output().unwrap();
        assert!(
            output.status.success(),
            "{program}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }
    /// The component package pkgbuild writes for a throwaway `Fixture.app`.
    fn component(root: &Path, identifier: &str, scripts: bool) -> std::path::PathBuf {
        let app = root.join("payload/Fixture.app/Contents");
        fs::create_dir_all(app.join("MacOS")).unwrap();
        fs::write(
            app.join("Info.plist"),
            format!(
                r#"<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>{BUNDLE}</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>"#
            ),
        )
        .unwrap();
        fs::write(app.join("MacOS/fixture"), b"#!/bin/sh\n").unwrap();
        let components = root.join("components.plist");
        fs::write(
            &components,
            r#"<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><array><dict><key>RootRelativeBundlePath</key><string>Fixture.app</string><key>BundleIsRelocatable</key><false/></dict></array></plist>"#,
        )
        .unwrap();
        let folder = root.join("components");
        fs::create_dir_all(&folder).unwrap();
        let output = folder.join(format!("{identifier}.pkg"));
        let payload = root.join("payload");
        let mut args: Vec<&Path> = vec![
            Path::new("--root"),
            &payload,
            Path::new("--component-plist"),
            &components,
            Path::new("--install-location"),
            Path::new("/Applications"),
            Path::new("--identifier"),
            Path::new(identifier),
            Path::new("--version"),
            Path::new("1.0"),
        ];
        let script_folder = root.join("scripts");
        if scripts {
            fs::create_dir_all(&script_folder).unwrap();
            fs::write(script_folder.join("postinstall"), b"#!/bin/sh\nexit 0\n").unwrap();
            fs::set_permissions(
                script_folder.join("postinstall"),
                std::os::unix::fs::PermissionsExt::from_mode(0o755),
            )
            .unwrap();
            args.extend([Path::new("--scripts"), &script_folder]);
        }
        args.push(&output);
        run("/usr/bin/pkgbuild", &args);
        output
    }
    /// A distribution archive laid out like an App Store download.
    fn distribution(root: &Path, extra_component: bool, scripts: bool) -> std::path::PathBuf {
        component(root, BUNDLE, scripts);
        let mut choices = format!(
            "<choice id=\"{BUNDLE}\" title=\"Fixture\" visible=\"false\" customLocation=\"/Applications\"><pkg-ref id=\"{BUNDLE}\"/></choice>"
        );
        let mut outline = format!("<line choice=\"{BUNDLE}\"/>");
        let mut references = format!(
            "<pkg-ref id=\"{BUNDLE}\" version=\"1.0\" onConclusion=\"none\">{BUNDLE}.pkg</pkg-ref>"
        );
        if extra_component {
            fs::copy(
                root.join(format!("components/{BUNDLE}.pkg")),
                root.join("components/test.opennavo.Second.pkg"),
            )
            .unwrap();
            choices += "<choice id=\"test.opennavo.Second\" title=\"Second\" visible=\"false\" customLocation=\"/Applications\"><pkg-ref id=\"test.opennavo.Second\"/></choice>";
            outline += "<line choice=\"test.opennavo.Second\"/>";
            references += "<pkg-ref id=\"test.opennavo.Second\" version=\"1.0\" onConclusion=\"none\">test.opennavo.Second.pkg</pkg-ref>";
        }
        let file = root.join("Distribution.xml");
        fs::write(
            &file,
            format!(
                r#"<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<installer-gui-script minSpecVersion="2">
    <title>Fixture</title>
    <options customize="never" require-scripts="false"/>
    <choices-outline><line choice="default">{outline}</line></choices-outline>
    <choice id="default" title="Fixture"/>
    {choices}
    {references}
</installer-gui-script>"#
            ),
        )
        .unwrap();
        let output = root.join("store.pkg");
        let components = root.join("components");
        run(
            "/usr/bin/productbuild",
            &[
                Path::new("--distribution"),
                &file,
                Path::new("--package-path"),
                &components,
                &output,
            ],
        );
        output
    }
    fn check(pkg: &Path, app: &str, bundle: &str) -> Result<(), opennavo_desktop_lib::AppError> {
        let work = tempfile::tempdir().unwrap();
        validate_store_package(pkg, Path::new(app), bundle, work.path())
    }
    fn refused(pkg: &Path, app: &str, bundle: &str, why: &str) {
        let error = check(pkg, app, bundle).unwrap_err();
        assert_eq!(
            (error.code.as_str(), error.message.as_str()),
            ("E_PROTECTED", why)
        );
    }

    #[test]
    fn distribution_archives_install_only_the_requested_app() {
        let dir = tempfile::tempdir().unwrap();
        let pkg = distribution(dir.path(), false, false);
        check(&pkg, "/Applications/Fixture.app", BUNDLE).unwrap();
        refused(
            &pkg,
            "/Applications/Fixture.app",
            "test.opennavo.Other",
            "store_package_identity",
        );
        refused(
            &pkg,
            "/Applications/Other.app",
            BUNDLE,
            "store_payload_scope",
        );
        refused(
            &pkg,
            "/Library/Fixture.app",
            BUNDLE,
            "store_package_identity",
        );
    }

    #[test]
    fn bare_component_packages_still_validate() {
        let dir = tempfile::tempdir().unwrap();
        let pkg = component(dir.path(), BUNDLE, false);
        check(&pkg, "/Applications/Fixture.app", BUNDLE).unwrap();
        let dir = tempfile::tempdir().unwrap();
        let pkg = component(dir.path(), BUNDLE, true);
        refused(
            &pkg,
            "/Applications/Fixture.app",
            BUNDLE,
            "store_package_components",
        );
    }

    #[test]
    fn installer_scripts_and_extra_components_are_refused() {
        let dir = tempfile::tempdir().unwrap();
        let pkg = distribution(dir.path(), false, true);
        refused(
            &pkg,
            "/Applications/Fixture.app",
            BUNDLE,
            "store_package_components",
        );
        let dir = tempfile::tempdir().unwrap();
        let pkg = distribution(dir.path(), true, false);
        refused(
            &pkg,
            "/Applications/Fixture.app",
            BUNDLE,
            "store_package_components",
        );
    }
}
