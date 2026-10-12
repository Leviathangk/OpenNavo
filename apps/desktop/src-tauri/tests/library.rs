use opennavo_desktop_lib::{
    brew::json::Info,
    db::Database,
    library::{self, files},
    model::*,
};
use std::path::Path;
fn app(root: &Path, name: &str, version: &str, icon: bool) -> std::path::PathBuf {
    let path = root.join(format!("{name}.app"));
    std::fs::create_dir_all(path.join("Contents/Resources")).unwrap();
    let mut dictionary = plist::Dictionary::new();
    dictionary.insert("CFBundleShortVersionString".into(), version.into());
    dictionary.insert("CFBundleVersion".into(), "200".into());
    if icon {
        dictionary.insert("CFBundleIconFile".into(), "AppIcon".into());
        let mut family = icns::IconFamily::new();
        for width in [128u32, 512, 1024] {
            let icon = icns::Image::from_data(
                icns::PixelFormat::RGBA,
                width,
                width,
                vec![120; (width * width * 4) as usize],
            )
            .unwrap();
            family.add_icon(&icon).unwrap();
        }
        family
            .write(std::fs::File::create(path.join("Contents/Resources/AppIcon.icns")).unwrap())
            .unwrap();
    }
    plist::Value::Dictionary(dictionary)
        .to_file_binary(path.join("Contents/Info.plist"))
        .unwrap();
    path
}
#[test]
fn read_only_json_versions_dependencies_plist_and_sizes() {
    let tmp = tempfile::tempdir().unwrap();
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let prefix = tmp.path().join("brew");
    let mut info: Info =
        serde_json::from_str(include_str!("fixtures/installed_subset.json")).unwrap();
    for cask in &mut info.casks {
        let version = if cask.token == "visual-studio-code" {
            "1.140.1"
        } else {
            "2026.1.4.8"
        };
        let path = app(tmp.path(), &cask.token, version, true);
        cask.artifacts = Some(vec![
            serde_json::json!({"app":[format!("{}.app",cask.token)],"target":path.to_string_lossy()}),
        ]);
    }
    for f in &info.formulae {
        for v in &f.installed {
            let path = files::formula_path(prefix.to_str().unwrap(), &f.name, &v.version).unwrap();
            std::fs::create_dir_all(&path).unwrap();
            std::fs::write(path.join("binary"), [1u8; 200]).unwrap();
        }
    }
    let mut scan = library::scan(&db, &info, prefix.to_str().unwrap(), Locale::ZhCn).unwrap();
    let code = scan
        .items
        .iter()
        .find(|i| i.token == "visual-studio-code")
        .unwrap();
    assert_eq!(code.actual_version.as_deref(), Some("1.140.1"));
    assert_eq!(code.status, InstalledStatus::SelfUpdated);
    assert_eq!(code.installed_at, Some(1788929931000));
    assert!(code.size_bytes.is_none());
    assert!(
        scan.items
            .iter()
            .find(|i| i.token == "pcre2")
            .unwrap()
            .required_by
            .contains(&"ripgrep".into())
    );
    assert!(
        scan.items
            .iter()
            .find(|i| i.token == "ripgrep")
            .unwrap()
            .on_request
    );
    let icons = tmp.path().join("icons");
    library::enrich(&db, &mut scan, &icons, |_| {});
    assert!(scan.items.iter().all(|i| i.size_bytes.is_some()));
    for item in scan.items.iter().filter(|i| i.kind == Kind::Cask) {
        let path = item.icon_path.as_ref().unwrap();
        let image = image::open(path).unwrap();
        assert_eq!((image.width(), image.height()), (256, 256));
        assert!(Path::new(path).starts_with(&icons));
    }
    let cached = library::scan(&db, &info, prefix.to_str().unwrap(), Locale::ZhCn).unwrap();
    assert!(cached.items.iter().all(|i| i.size_bytes.is_some()));
    assert!(library::storage(&cached.items, 1, true).complete);
    let path = Path::new(
        &cached
            .items
            .iter()
            .find(|i| i.kind == Kind::Cask)
            .unwrap()
            .app_paths[0],
    );
    let stamp = std::time::SystemTime::UNIX_EPOCH
        + std::time::Duration::from_millis(files::mtime(path).unwrap() as u64 + 5000);
    std::fs::File::open(path)
        .unwrap()
        .set_times(std::fs::FileTimes::new().set_modified(stamp))
        .unwrap();
    assert!(files::cached_size(&db, path).unwrap().is_none());
    assert!(files::cached_icon(&db, path).unwrap().is_none());
    assert!(files::formula_path("/tmp", "node", "../escape").is_none());
    assert!(files::formula_path("/tmp", "--help", "1").is_none());
}
#[test]
fn asset_scope_allows_extracted_local_icons_without_allowing_other_app_data() {
    let tmp = tempfile::tempdir().unwrap();
    let config: serde_json::Value =
        serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
    let identifier = config["identifier"].as_str().unwrap();
    let cache = tmp.path().join("Caches");
    let data = tmp.path().join("Application Support").join(identifier);
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let patterns: Vec<_> = config["app"]["security"]["assetProtocol"]["scope"]
        .as_array()
        .unwrap()
        .iter()
        .map(|scope| {
            glob::Pattern::new(
                &scope
                    .as_str()
                    .unwrap()
                    .replace("$CACHE", &cache.to_string_lossy())
                    .replace("$APPDATA", &data.to_string_lossy()),
            )
            .unwrap()
        })
        .collect();
    let allowed = |path: &Path| patterns.iter().any(|pattern| pattern.matches_path(path));
    for (name, destination) in [
        ("LocalApp", data.join("logs/local-app-icons")),
        ("BrewApp", cache.join(identifier).join("icons")),
    ] {
        let bundle = app(tmp.path(), name, "1.0", true);
        let png = files::icon(&db, &bundle, &destination).unwrap();
        assert!(
            allowed(Path::new(&png)),
            "extracted icon must be loadable: {png}"
        );
        assert!(image::open(png).is_ok());
    }
    assert!(!allowed(&data.join("opennavo.db")));
    assert!(!allowed(&data.join("logs/tasks/private.png")));
    assert!(!allowed(&tmp.path().join("OtherApp/icon.png")));
}

#[test]
fn plist_fallback_and_symbolic_link_do_not_follow_outside() {
    let tmp = tempfile::tempdir().unwrap();
    let path = app(tmp.path(), "NoIcon", "1.0", false);
    let mut dict = files::plist(&path).unwrap();
    dict.remove("CFBundleShortVersionString");
    plist::Value::Dictionary(dict)
        .to_file_xml(path.join("Contents/Info.plist"))
        .unwrap();
    assert_eq!(files::actual_version(&path).as_deref(), Some("200"));
    let outside = tmp.path().join("outside");
    std::fs::write(&outside, [0u8; 5000]).unwrap();
    let before = files::directory_size(&path).unwrap();
    #[cfg(unix)]
    std::os::unix::fs::symlink(&outside, path.join("Contents/external")).unwrap();
    assert_eq!(files::directory_size(&path).unwrap(), before);
    #[cfg(target_os = "macos")]
    {
        let db = Database::open(&tmp.path().join("db")).unwrap();
        let icon = files::icon(&db, &path, &tmp.path().join("icons")).unwrap();
        let decoded = image::open(icon).unwrap();
        assert!(decoded.width() <= 512);
    }
}

#[test]
fn old_icon_cache_is_replaced_by_normalized_artwork_without_changing_bundle() {
    let tmp = tempfile::tempdir().unwrap();
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let path = app(tmp.path(), "PaddedIcon", "1.0", true);
    let source = path.join("Contents/Resources/AppIcon.icns");
    let before = std::fs::read(&source).unwrap();
    let cache = tmp.path().join("icons");
    let normalized = files::icon(&db, &path, &cache).unwrap();
    let old = cache.join("legacy.png");
    std::fs::copy(&normalized, &old).unwrap();
    db.with(|c| {
        c.execute(
            "UPDATE icon_cache SET png_path=?1",
            [old.to_string_lossy().as_ref()],
        )?;
        Ok(())
    })
    .unwrap();
    assert!(files::cached_icon(&db, &path).unwrap().is_none());
    let regenerated = files::icon(&db, &path, &cache).unwrap();
    assert!(regenerated.ends_with("-normalized-v1.png"));
    assert_eq!(image::open(&regenerated).unwrap().width(), 256);
    assert_eq!(
        files::cached_icon(&db, &path).unwrap().as_deref(),
        Some(regenerated.as_str())
    );
    assert_eq!(std::fs::read(source).unwrap(), before);
}
#[tokio::test]
async fn empty_simulated_scan_caches_and_completes_without_user_paths() {
    let tmp = tempfile::tempdir().unwrap();
    let core = opennavo_desktop_lib::core::DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        std::sync::Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fake-brew/brew")
            .to_string_lossy()
            .into(),
    );
    assert!(
        core.refresh_library(LibraryChangeReason::Refresh, None)
            .await
            .unwrap()
            .is_empty()
    );
    for _ in 0..100 {
        if core.storage_summary().unwrap().complete {
            break;
        }
        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
    }
    assert!(core.storage_summary().unwrap().complete);
    assert!(core.library_list().unwrap().is_empty());
    assert!(core.database.meta("installed_cache").unwrap().is_some());
}

#[test]
fn legacy_null_flags_are_false() {
    let mut info: serde_json::Value =
        serde_json::from_str(include_str!("fixtures/installed_subset.json")).unwrap();
    info["casks"][0]["auto_updates"] = serde_json::Value::Null;
    info["formulae"][0]["installed"][0]["installed_on_request"] = serde_json::Value::Null;
    let parsed: Info = serde_json::from_value(info).unwrap();
    assert!(!parsed.casks[0].auto_updates);
    assert!(!parsed.formulae[0].installed[0].installed_on_request);
}

#[tokio::test]
async fn task_completion_refreshes_new_and_upgraded_dependencies() {
    use opennavo_desktop_lib::core::DesktopCore;
    use std::{os::unix::fs::PermissionsExt, sync::Arc};

    let tmp = tempfile::tempdir().unwrap();
    let fixture = tmp.path().join("installed.json");
    let brew = tmp.path().join("brew");
    // The read-only simulator deliberately omits dependencies in targeted queries to catch partial-refresh regressions.
    std::fs::write(
        &brew,
        format!(
            r#"#!/usr/bin/python3 -I
import json, sys
from pathlib import Path
args = sys.argv[1:]
if args == ['--version']:
    print('Homebrew 7.0.7')
elif args == ['--prefix'] or args == ['--cache']:
    print({root})
elif args[0] == 'info':
    data = json.loads(Path({fixture}).read_text())
    if '--installed' not in args:
        data['formulae'] = [f for f in data['formulae'] if f['name'] == args[-1]]
    print(json.dumps(data))
elif args[0] == 'outdated':
    print('{{"formulae": [], "casks": []}}')
else:
    sys.exit(1)
"#,
            root = serde_json::to_string(tmp.path().to_str().unwrap()).unwrap(),
            fixture = serde_json::to_string(fixture.to_str().unwrap()).unwrap()
        ),
    )
    .unwrap();
    std::fs::set_permissions(&brew, std::fs::Permissions::from_mode(0o755)).unwrap();
    let core = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    core.settings.write().unwrap().brew_path = Some(brew.to_string_lossy().into());
    std::fs::write(
        &fixture,
        r#"{"formulae":[{"name":"dependency","installed":[{"version":"1"}]}],"casks":[]}"#,
    )
    .unwrap();
    core.refresh_library(LibraryChangeReason::Startup, None)
        .await
        .unwrap();
    assert_eq!(core.library_list().unwrap().len(), 1);
    std::fs::write(&fixture, r#"{"formulae":[{"name":"dependency","installed":[{"version":"2"}]},{"name":"new-dependency","installed":[{"version":"1"}]},{"name":"target","installed":[{"version":"1","runtime_dependencies":[{"full_name":"dependency","declared_directly":true},{"full_name":"new-dependency","declared_directly":true}]}],"dependencies":["dependency","new-dependency"]}],"casks":[]}"#).unwrap();
    let mut task = core
        .queue
        .enqueue(
            TaskOp::Install,
            Some(TaskTarget {
                kind: Kind::Formula.into(),
                token: "target".into(),
            }),
            TaskOptions::default(),
            TaskTrigger::Manual,
        )
        .unwrap();
    task.state = TaskState::Succeeded;
    core.after_task(&task).await;
    let items = core.library_list().unwrap();
    assert_eq!(items.len(), 3);
    let dependency = items
        .iter()
        .find(|item| item.token == "dependency")
        .unwrap();
    assert_eq!(dependency.installed_version, "2");
    assert!(dependency.required_by.contains(&"target".into()));
    assert!(items.iter().any(|item| item.token == "new-dependency"));
}
