use opennavo_desktop_lib::{install_preflight, model::*};
use std::{os::unix::fs::PermissionsExt, path::Path};

#[tokio::test]
async fn simulated_brew_preflight_is_read_only_and_uses_declared_target() {
    let tmp = tempfile::tempdir().unwrap();
    let app = tmp.path().join("Existing App.app");
    std::fs::create_dir_all(app.join("Contents")).unwrap();
    let mut metadata = plist::Dictionary::new();
    metadata.insert(
        "CFBundleIdentifier".into(),
        plist::Value::String("org.example.app".into()),
    );
    plist::Value::Dictionary(metadata)
        .to_file_xml(app.join("Contents/Info.plist"))
        .unwrap();
    let json = serde_json::json!({"casks":[{"token":"example","name":["Example"],"version":"1", "installed":null,
        "artifacts":[{"app":["Original.app",{"target":app.to_string_lossy()}]}]}]}).to_string();
    let brew = tmp.path().join("brew");
    let calls = tmp.path().join("calls");
    // The simulator in a private test directory accepts only discovery and single-package info; any write command fails immediately.
    std::fs::write(&brew, format!("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '{}'\ncase \"$*\" in\n'--version') echo 'Homebrew 7.0.7';;\n'--prefix') echo '{}';;\n'info --json=v2 --cask example') printf '%s\\n' '{}';;\n*) exit 99;;\nesac\n", calls.display(), tmp.path().display(), json.replace('\'', "'\\''"))).unwrap();
    std::fs::set_permissions(&brew, std::fs::Permissions::from_mode(0o755)).unwrap();
    let settings = Settings {
        brew_path: Some(brew.to_string_lossy().into()),
        ..Settings::default()
    };
    let target = TaskTarget {
        kind: Kind::Cask.into(),
        token: "example".into(),
    };
    let result = install_preflight::inspect(&settings, &target)
        .await
        .unwrap();
    assert_eq!(result.status, InstallPreflightStatus::Conflict);
    assert_eq!(result.paths, vec![app.to_string_lossy()]);
    assert!(
        Path::new(&result.paths[0])
            .join("Contents/Info.plist")
            .exists()
    );
    let calls_before = std::fs::read_to_string(&calls).unwrap();
    assert!(calls_before.lines().all(|line| matches!(
        line,
        "--version" | "--prefix" | "info --json=v2 --cask example"
    )));
    for target in [
        TaskTarget {
            kind: Kind::Formula.into(),
            token: "example".into(),
        },
        TaskTarget {
            kind: Kind::Cask.into(),
            token: "--evil".into(),
        },
    ] {
        assert_eq!(
            install_preflight::inspect(&settings, &target)
                .await
                .unwrap_err()
                .code,
            "E_INVALID_ARG"
        );
    }
    assert_eq!(std::fs::read_to_string(&calls).unwrap(), calls_before);
}

#[tokio::test]
async fn untapped_third_party_cask_uses_complete_targets_and_blocks_legacy_metadata() {
    let tmp = tempfile::tempdir().unwrap();
    let brew = tmp.path().join("brew");
    let calls = tmp.path().join("calls");
    // Before the first install brew has not cloned (or trusted) the tap, so info fails like this.
    std::fs::write(&brew, format!("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '{}'\ncase \"$*\" in\n'--version') echo 'Homebrew 7.0.9';;\n'--prefix') echo '{}';;\n'info --json=v2 --cask swiftvm-app/tap/swiftvm') echo \"Error: Cask 'swiftvm-app/tap/swiftvm' is unavailable.\" >&2; exit 1;;\n*) exit 99;;\nesac\n", calls.display(), tmp.path().display())).unwrap();
    std::fs::set_permissions(&brew, std::fs::Permissions::from_mode(0o755)).unwrap();
    let settings = Settings {
        brew_path: Some(brew.to_string_lossy().into()),
        ..Settings::default()
    };
    let target = TaskTarget {
        kind: Kind::Cask.into(),
        token: "swiftvm".into(),
    };
    let item: CatalogItem = serde_json::from_value(serde_json::json!({
        "kind": "cask", "token": "swiftvm", "tap": "swiftvm-app/tap", "name": "SwiftVM", "names": ["SwiftVM"],
        "version": "1.3.0", "homepage": null, "autoUpdates": true, "deprecated": false, "disabled": false,
        "hidden": false, "isFont": false, "isLibrary": false, "categories": [], "tags": [], "installs30d": 0,
        "rank30d": null, "popularity": 0.0, "iconUrl": null, "accentColor": null,
        "apps": ["Source.app"], "binaries": [], "pinyin": null,
        "versionChangedAt": "2026-10-14T00:00:00Z", "downloadSize": null, "onRequest": null
    }))
    .unwrap();
    let result = install_preflight::inspect_listed(&settings, &target, Some(item.clone()))
        .await
        .unwrap();
    assert_eq!(result.status, InstallPreflightStatus::Blocked);
    assert_eq!(result.tap.as_deref(), Some("swiftvm-app/tap"));
    assert_eq!(result.name, "SwiftVM");

    let mut complete = item.clone();
    complete.app_targets = Some(vec!["OpenNavo Preflight Fixture Missing.app".into()]);
    let ready = install_preflight::inspect_listed(&settings, &target, Some(complete.clone()))
        .await
        .unwrap();
    assert_eq!(ready.status, InstallPreflightStatus::Ready);
    assert_eq!(ready.tap.as_deref(), Some("swiftvm-app/tap"));
    // Source.app was renamed: only the real destination determines the adoption prompt.
    let renamed = tmp.path().join("Renamed.app");
    std::fs::create_dir_all(renamed.join("Contents")).unwrap();
    std::fs::write(renamed.join("Contents/Info.plist"),
        r#"<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>test.renamed</string></dict></plist>"#).unwrap();
    complete.app_targets = Some(vec![renamed.to_string_lossy().into()]);
    let conflict = install_preflight::inspect_listed(&settings, &target, Some(complete.clone()))
        .await
        .unwrap();
    assert_eq!(conflict.status, InstallPreflightStatus::Conflict);
    assert_eq!(conflict.paths, vec![renamed.to_string_lossy()]);
    complete.app_targets = Some(vec!["$APPDIR/Unknown.app".into()]);
    assert_eq!(
        install_preflight::inspect_listed(&settings, &target, Some(complete))
            .await
            .unwrap()
            .status,
        InstallPreflightStatus::Blocked
    );
    let calls = std::fs::read_to_string(&calls).unwrap();
    assert!(
        calls
            .lines()
            .any(|line| line == "info --json=v2 --cask swiftvm-app/tap/swiftvm")
    );
    // Without a catalog tap the request cannot smuggle one in through the token.
    let smuggled = TaskTarget {
        kind: Kind::Cask.into(),
        token: "swiftvm-app/tap/swiftvm".into(),
    };
    assert_eq!(
        install_preflight::inspect_listed(&settings, &smuggled, None)
            .await
            .unwrap_err()
            .code,
        "E_INVALID_ARG"
    );
}
