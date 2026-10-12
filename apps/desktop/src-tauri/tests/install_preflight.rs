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
