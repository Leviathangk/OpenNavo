use base64::{Engine, engine::general_purpose::STANDARD};
use opennavo_desktop_lib::{
    model::*,
    uninstall::rules::Snapshot,
    updater::{
        electron,
        release::{Artifact, File, Release},
        service::Candidate,
    },
};
use sha2::{Digest, Sha256, Sha512};
use std::fs;
fn artifact(bytes: &[u8]) -> Artifact {
    Artifact {
        version: "2.0".into(),
        version_key: "CFBundleShortVersionString".into(),
        file: File {
            url: "https://github.com/example/app/releases/download/v2/App-arm64.zip".into(),
            name: "App-arm64.zip".into(),
            size: Some(bytes.len() as u64),
            architecture: Some("arm64".into()),
            sha256: Some(format!("{:x}", Sha256::digest(bytes))),
            sha512: Some(STANDARD.encode(Sha512::digest(bytes))),
        },
    }
}
#[test]
fn electron_config_supports_only_public_https_providers_and_bounded_yaml() {
    let c=electron::Config::parse(b"provider: generic\nurl: https://example.org/releases\nupdaterCacheDirName: fixture-updater\n").unwrap();
    assert_eq!(
        c.manifest(UpdateChannel::Stable).unwrap(),
        "https://example.org/releases/latest-mac.yml"
    );
    assert_eq!(
        c.manifest(UpdateChannel::Beta).unwrap(),
        "https://example.org/releases/beta-mac.yml"
    );
    let c = electron::Config::parse(b"provider: github\nowner: example\nrepo: app\n").unwrap();
    assert_eq!(
        c.manifest(UpdateChannel::Stable).unwrap(),
        "https://github.com/example/app/releases/latest/download/latest-mac.yml"
    );
    for body in [
        "provider: generic\nurl: http://example.org/",
        "provider: generic\nurl: https://user:secret@example.org/",
        "provider: github\nowner: example\nrepo: app\nprivate: true",
        "provider: github\nowner: example\nrepo: app\ntoken: hidden",
        "provider: generic\nurl: &url https://example.org/\nother: *url",
        "provider: !custom generic\nurl: https://example.org/",
        "provider: generic\nurl: https://example.org/\n---\nother: document",
    ] {
        assert!(electron::Config::parse(body.as_bytes()).is_err(), "{body}");
    }
    let deep = format!("key: {}1{}", "[".repeat(20), "]".repeat(20));
    assert!(electron::yaml(deep.as_bytes()).is_err());
    assert!(electron::yaml(&vec![b'x'; electron::MAX_MANIFEST + 1]).is_err());
}
#[test]
fn electron_manifest_architecture_hash_and_minimum_system_selection() {
    let hash = STANDARD.encode([1; 64]);
    let raw = format!(
        "version: 2.1.0\nminimumSystemVersion: '14.0'\nreleaseNotes: Fixed bugs\nfiles:\n  - url: App-x64.zip\n    sha512: {hash}\n  - url: App-arm64.zip\n    sha512: {hash}\n  - url: App-universal.dmg\n    sha512: {hash}\n"
    );
    let r = electron::parse(
        raw.as_bytes(),
        "https://example.org/releases/latest-mac.yml",
    )
    .unwrap();
    assert_eq!(r.select("arm64", "15.0").unwrap().name, "App-arm64.zip");
    assert_eq!(r.select("x86_64", "15.0").unwrap().name, "App-x64.zip");
    assert!(r.select("arm64", "13.0").is_none());
    assert_eq!(r.files[0].url, "https://example.org/releases/App-x64.zip");
    assert!(
        electron::parse(
            raw.replace(&hash, "invalid").as_bytes(),
            "https://example.org/latest-mac.yml"
        )
        .is_err()
    );
    assert!(
        electron::parse(
            raw.replace("App-x64.zip", "file:///tmp/App.zip").as_bytes(),
            "https://example.org/latest-mac.yml"
        )
        .is_err()
    );
}
#[test]
fn every_digest_is_checked_and_github_cannot_replace_digest_with_team_id() {
    let a = artifact(b"archive");
    for source in [
        UpdateSource::Electron,
        UpdateSource::Github,
        UpdateSource::Vendor,
    ] {
        a.verify(b"archive", source).unwrap();
        assert_eq!(
            a.verify(b"tamper!", source).unwrap_err().code,
            "E_UPDATE_INTEGRITY"
        );
    }
    let mut a = a;
    a.file.sha512 = None;
    assert!(a.verify(b"archive", UpdateSource::Electron).is_err());
    a.file.sha256 = None;
    assert!(a.verify(b"archive", UpdateSource::Github).is_err());
    assert_eq!(a.integrity(UpdateSource::Vendor), UpdateIntegrity::TeamId);
    a.verify(b"archive", UpdateSource::Vendor).unwrap();
}
#[test]
fn github_release_requires_digest_and_direct_repository_asset() {
    let mut r = Release {
        version: "2".into(),
        version_key: "CFBundleShortVersionString".into(),
        notes: None,
        minimum_os: None,
        files: vec![artifact(b"zip").file],
    };
    r.validate(UpdateSource::Github).unwrap();
    r.files[0].url = "https://mirror.example.org/App.zip".into();
    assert!(r.validate(UpdateSource::Github).is_err());
    r.files[0].url = artifact(b"zip").file.url;
    r.files[0].sha256 = None;
    assert!(r.validate(UpdateSource::Github).is_err());
}

#[test]
fn installed_app_routing_uses_enabled_sources_in_priority_order() {
    use opennavo_desktop_lib::{
        db::Database,
        updater::{routing, service::FeedRule},
    };
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().canonicalize().unwrap().join("Fixture.app");
    fs::create_dir_all(path.join("Contents/Resources")).unwrap();
    fs::write(
        path.join("Contents/Resources/app-update.yml"),
        "provider: generic\nurl: https://example.org/releases\n",
    )
    .unwrap();
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let mut app: LocalApp = serde_json::from_value(serde_json::json!({
        "id":"fixture", "path":path, "name":"Fixture", "bundleId":"test.app",
        "source":"direct", "running":false, "removable":true
    }))
    .unwrap();
    let rules: Vec<FeedRule> = [UpdateSource::Github, UpdateSource::Vendor]
        .into_iter()
        .map(|source| {
            serde_json::from_value(
                serde_json::json!({"bundleId":"test.app", "source":source, "teamId":"TESTTEAM01"}),
            )
            .unwrap()
        })
        .collect();
    let mut settings = Settings::default();
    let select =
        |settings: &Settings, app: &LocalApp| routing::select(&db, settings, app, &rules).unwrap();
    assert_eq!(select(&settings, &app), Some(UpdateSource::Electron));
    settings
        .update_sources
        .0
        .insert(UpdateSource::Electron, false);
    assert_eq!(select(&settings, &app), Some(UpdateSource::Github));
    settings
        .update_sources
        .0
        .insert(UpdateSource::Github, false);
    assert_eq!(select(&settings, &app), Some(UpdateSource::Vendor));
    settings
        .update_sources
        .0
        .insert(UpdateSource::Vendor, false);
    assert_eq!(select(&settings, &app), None);
    app.cask = Some("fixture".into());
    assert_eq!(select(&settings, &app), Some(UpdateSource::Homebrew));
    app.source = AppSource::AppStore;
    assert_eq!(select(&settings, &app), Some(UpdateSource::Mas));
    settings.update_sources.0.insert(UpdateSource::Mas, false);
    assert_eq!(select(&settings, &app), None);
    app.path = "/System/Applications/Fixture.app".into();
    assert_eq!(select(&Settings::default(), &app), None);
}
#[test]
fn pending_update_requires_matching_metadata_and_complete_sha512_without_symlinks() {
    let tmp = tempfile::tempdir().unwrap();
    let cache = tmp.path().canonicalize().unwrap();
    let root = cache.join("fixture-updater/pending");
    fs::create_dir_all(&root).unwrap();
    let config = electron::Config::parse(
        b"provider: generic\nurl: https://example.org/\nupdaterCacheDirName: fixture-updater",
    )
    .unwrap();
    let artifact = artifact(b"complete archive");
    assert!(!electron::pending_in(&cache, &config, &artifact).unwrap());
    fs::write(root.join("Fixture.zip"), b"complete archive").unwrap();
    assert!(!electron::pending_in(&cache, &config, &artifact).unwrap());
    fs::write(
        root.join("update-info.json"),
        serde_json::to_vec(
            &serde_json::json!({"fileName":"Fixture.zip","sha512":artifact.file.sha512}),
        )
        .unwrap(),
    )
    .unwrap();
    assert!(electron::pending_in(&cache, &config, &artifact).unwrap());
    fs::write(root.join("Fixture.zip"), b"truncated").unwrap();
    assert!(!electron::pending_in(&cache, &config, &artifact).unwrap());
    fs::remove_file(root.join("Fixture.zip")).unwrap();
    std::os::unix::fs::symlink(root.join("update-info.json"), root.join("Fixture.zip")).unwrap();
    assert!(!electron::pending_in(&cache, &config, &artifact).unwrap());
}
#[test]
fn mismatched_or_missing_required_team_is_rejected_before_queue_persistence() {
    use opennavo_desktop_lib::{db::Database, queue::TaskQueue};
    use std::sync::{Arc, RwLock};
    let tmp = tempfile::tempdir().unwrap();
    let app = tmp.path().canonicalize().unwrap().join("Fixture.app");
    fs::create_dir(&app).unwrap();
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let queue = TaskQueue::new(
        db.clone(),
        Arc::new(RwLock::new(Settings::default())),
        tmp.path().join("logs"),
        tmp.path().join("askpass"),
        Arc::new(|_| panic!("invalid task emitted event")),
    )
    .unwrap();
    for source in [
        UpdateSource::Electron,
        UpdateSource::Github,
        UpdateSource::Vendor,
    ] {
        let item:OutdatedItem=serde_json::from_value(serde_json::json!({"kind":"link","token":"navigation-only","name":"Fixture","source":source,"appId":"fixture","installedVersion":"1","currentVersion":"2","autoUpdates":false,"pinned":false,"ignored":false,"dependents":0,"major":false,"requiresAdmin":false,"running":false,"relaunchPending":false,"criticalUpdate":false,"expectedTeamMatch":false})).unwrap();
        let candidate = Candidate {
            item,
            artifact: Some(artifact(b"zip")),
            app_path: app.clone(),
            snapshot: Snapshot::capture(&app).unwrap(),
            feed_item: None,
            public_key: None,
            expected_team: Some("TESTTEAM01".into()),
            checked_at: 0,
        };
        db.set_meta(
            "native_update_candidates",
            &serde_json::to_string(&vec![candidate.clone()]).unwrap(),
        )
        .unwrap();
        let error = queue
            .enqueue(
                TaskOp::Upgrade,
                Some(TaskTarget {
                    kind: TaskKind::App,
                    token: "fixture".into(),
                }),
                TaskOptions {
                    source: Some(source),
                    ..Default::default()
                },
                TaskTrigger::Manual,
            )
            .unwrap_err();
        assert_eq!(error.code, "E_UPDATE_TEAM_MISMATCH");
        assert!(db.tasks().unwrap().is_empty());
        if source != UpdateSource::Electron {
            let mut c = candidate;
            c.item.expected_team_match = None;
            c.expected_team = None;
            assert_eq!(
                c.validate_identity().unwrap_err().code,
                "E_UPDATE_TEAM_MISMATCH"
            )
        }
    }
}

#[test]
fn sparkle_routing_requires_supported_signing_configuration() {
    use opennavo_desktop_lib::{
        db::Database,
        updater::{routing, service::FeedRule},
    };
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("Fixture.app");
    fs::create_dir_all(path.join("Contents")).unwrap();
    let db = Database::open(&tmp.path().join("db")).unwrap();
    let data = serde_json::json!({"kind":"cask","token":"fixture","name":"Fixture","names":[],"version":"1","autoUpdates":true,"deprecated":false,"disabled":false,"hidden":false,"isFont":false,"isLibrary":false,"categories":[],"tags":[],"installs30d":0,"popularity":0,"apps":[],"binaries":[],"versionChangedAt":""}).to_string();
    db.with(|c| { c.execute("INSERT INTO catalog_items(kind,token,data,name,version,version_changed_at) VALUES('cask','fixture',?1,'Fixture','1',0)", [&data])?; Ok(()) }).unwrap();
    let app: LocalApp = serde_json::from_value(serde_json::json!({"id":"fixture","path":path,"name":"Fixture","bundleId":"test.app","cask":"fixture","source":"homebrew","running":false,"removable":true})).unwrap();
    let rule: FeedRule = serde_json::from_value(serde_json::json!({"bundleId":"test.app","source":"sparkle","feedUrl":"https://example.org/feed"})).unwrap();
    for key in [
        None,
        Some("".to_owned()),
        Some("invalid".to_owned()),
        Some(STANDARD.encode([1; 31])),
        Some(STANDARD.encode([1; 32])),
    ] {
        let mut dict = plist::Dictionary::new();
        dict.insert("SUFeedURL".into(), "https://example.org/feed".into());
        if let Some(key) = &key {
            dict.insert("SUPublicEDKey".into(), key.clone().into());
        }
        plist::Value::Dictionary(dict)
            .to_file_xml(path.join("Contents/Info.plist"))
            .unwrap();
        let expected = if key == Some(STANDARD.encode([1; 32])) {
            UpdateSource::Sparkle
        } else {
            UpdateSource::Homebrew
        };
        for rules in [&[][..], std::slice::from_ref(&rule)] {
            assert_eq!(
                routing::select(&db, &Settings::default(), &app, rules).unwrap(),
                Some(expected)
            );
        }
    }
}
