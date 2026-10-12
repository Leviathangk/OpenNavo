fn main() {
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        use std::{path::PathBuf, process::Command};
        let out = PathBuf::from(std::env::var_os("OUT_DIR").expect("OUT_DIR"));
        let mut objects = Vec::new();
        for source in ["native/fan_bridge.m", "native/monitor_bridge.m"] {
            let object = out
                .join(std::path::Path::new(source).file_stem().unwrap())
                .with_extension("o");
            let target = std::env::var("TARGET").expect("TARGET");
            let arch = if target.starts_with("aarch64") {
                "arm64"
            } else {
                "x86_64"
            };
            assert!(
                Command::new("xcrun")
                    .args([
                        "clang",
                        "-fobjc-arc",
                        "-fblocks",
                        "-Wall",
                        "-Wextra",
                        "-Werror",
                        "-mmacosx-version-min=13.0",
                        "-arch",
                        arch,
                        "-c",
                        source,
                        "-o"
                    ])
                    .arg(&object)
                    .status()
                    .expect("compile native fan boundary")
                    .success()
            );
            objects.push(object);
            println!("cargo:rerun-if-changed={source}");
        }
        assert!(
            Command::new("xcrun")
                .args(["ar", "crs"])
                .arg(out.join("libopennavo_fan_bridge.a"))
                .args(objects)
                .status()
                .expect("archive native fan boundary")
                .success()
        );
        println!("cargo:rustc-link-search=native={}", out.display());
        println!("cargo:rustc-link-lib=static=opennavo_fan_bridge");
        for framework in ["Foundation", "AppKit", "Security", "ServiceManagement"] {
            println!("cargo:rustc-link-lib=framework={framework}");
        }
        println!("cargo:rerun-if-changed=native/fan_bridge.m");
    }
    if std::env::var_os("CARGO_FEATURE_FAN_HELPER").is_some()
        && std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos")
    {
        let info =
            std::path::PathBuf::from(std::env::var_os("CARGO_MANIFEST_DIR").expect("manifest"))
                .join("fan-helper/Info.plist");
        println!(
            "cargo:rustc-link-arg-bin=opennavo-fan-helper=-Wl,-sectcreate,__TEXT,__info_plist,{}",
            info.display()
        );
        println!("cargo:rerun-if-changed=fan-helper/Info.plist");
    }
    tauri_build::build();
}
