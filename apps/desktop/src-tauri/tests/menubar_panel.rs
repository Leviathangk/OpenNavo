//! The native regression runs in its own main-thread process without showing UI.
#[cfg(target_os = "macos")]
#[test]
fn native_panel_preserves_kvo_and_contains_configuration_failures() {
    use std::process::Command;

    let temp = tempfile::tempdir().unwrap();
    let binary = temp.path().join("menubar-panel-test");
    let build = Command::new("xcrun")
        .args([
            "clang",
            "-fobjc-arc",
            "-fblocks",
            "-Wall",
            "-Wextra",
            "-Werror",
            "tests/native/menubar_panel.m",
            "-framework",
            "AppKit",
            "-framework",
            "IOKit",
            "-framework",
            "CoreServices",
            "-o",
        ])
        .arg(&binary)
        .output()
        .unwrap();
    assert!(
        build.status.success(),
        "native fixture build failed: {}",
        String::from_utf8_lossy(&build.stderr)
    );
    let result = Command::new(binary).output().unwrap();
    assert!(
        result.status.success(),
        "native panel regression failed: {}\n{}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
}
