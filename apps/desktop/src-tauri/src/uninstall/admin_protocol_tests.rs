//! Exercise the actual privileged envelope through JSON bytes, without executing any actions.
use super::*;
use crate::{
    appstore::install::PrivilegedInstall,
    security::admin::{Action as SecurityAction, Request as SecurityRequest},
    updater::{
        admin::{Action as NativeAction, Request as NativeRequest},
        package::Artifact,
        swap::SwapRequest,
    },
};

const NANOS: u128 = 1_792_205_123_456_789_123;

fn snapshot(modified: u128) -> Snapshot {
    Snapshot {
        kind: 0,
        length: 123,
        modified,
        device: 4,
        inode: 5,
        link: None,
        parents: vec![("/fixture-only".into(), 4, 6)],
    }
}

fn batch(snapshot: &Snapshot) -> AdminBatch {
    let source = PathBuf::from("/fixture-only/Installed.app");
    let destination = PathBuf::from("/fixture-only/Prepared.app");
    let swap = SwapRequest {
        unsigned: None,
        source: UpdateSource::Electron,
        installed: source.clone(),
        prepared: destination.clone(),
        installed_snapshot: snapshot.clone(),
        prepared_snapshot: snapshot.clone(),
        expected_team: Some("TESTTEAM01".into()),
        package: None,
    };
    let mut pkg = swap.clone();
    pkg.package = Some(Artifact {
        path: "/fixture-only/Update.pkg".into(),
        snapshot: snapshot.clone(),
        sha256: "a".repeat(64),
    });
    AdminBatch {
        home: "/Users/fixture-only".into(),
        requests: vec![AdminRequest {
            item: PlanItem {
                id: "uninstall".into(),
                path: source.to_string_lossy().into_owned(),
                category: ItemCategory::AppData,
                kind: PlanItemKind::File,
                count: 1,
                size_bytes: Some(snapshot.length),
                reason: MatchReason::BundleId,
                confidence: PlanItemConfidence::High,
                selected: true,
                required: false,
                needs_admin: true,
                shared_with: vec![],
                note: None,
            },
            snapshot: snapshot.clone(),
            action: ItemAction::Path {
                empty_only: false,
                bootout: None,
            },
            mode: DeleteMode::Trash,
            destination: Some(destination.clone()),
            app_path: Some(source.clone()),
        }],
        native: [
            NativeAction::AppSwap {
                request: Box::new(swap),
            },
            NativeAction::PkgInstall {
                request: Box::new(pkg),
            },
            NativeAction::MasInstallPkg {
                request: Box::new(PrivilegedInstall {
                    adam_id: 123,
                    bundle_id: Some("test.fixture".into()),
                    staging: "/fixture-only".into(),
                    staging_snapshot: snapshot.clone(),
                    pkg: "/fixture-only/Store.pkg".into(),
                    pkg_snapshot: snapshot.clone(),
                    receipt: "/fixture-only/receipt".into(),
                    receipt_snapshot: snapshot.clone(),
                    app_path: source.clone(),
                    app_snapshot: Some(snapshot.clone()),
                    parent_snapshot: snapshot.clone(),
                }),
            },
        ]
        .into_iter()
        .enumerate()
        .map(|(i, action)| NativeRequest {
            id: format!("native-{i}"),
            action,
        })
        .collect(),
        security: [
            SecurityAction::QuarantineMove {
                source: source.clone(),
                destination: destination.clone(),
                snapshot: snapshot.clone(),
            },
            SecurityAction::QuarantineRestore {
                source: destination,
                destination: source.clone(),
                snapshot: snapshot.clone(),
                mode: 0o644,
            },
            SecurityAction::LaunchdDisableSystem {
                path: source.clone(),
                snapshot: snapshot.clone(),
                label: "test.fixture".into(),
            },
            SecurityAction::LaunchdEnableSystem {
                path: source,
                snapshot: snapshot.clone(),
                label: "test.fixture".into(),
            },
        ]
        .into_iter()
        .enumerate()
        .map(|(i, action)| SecurityRequest {
            id: format!("security-{i}"),
            action,
        })
        .collect(),
    }
}

fn roundtrip(batch: &AdminBatch) -> AdminBatch {
    let bytes = serde_json::to_vec(batch).unwrap();
    // This is the exact decoder used by admin_entry, not from_value or direct execute().
    let decoded: AdminBatch = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(serde_json::to_vec(&decoded).unwrap(), bytes);
    decoded
}

#[test]
fn admin_batch_roundtrips_all_snapshot_actions() {
    let dir = tempfile::tempdir().unwrap();
    let file = dir.path().canonicalize().unwrap().join("fixture");
    fs::write(&file, b"fixture").unwrap();
    let captured = Snapshot::capture(&file).unwrap();
    let decoded = roundtrip(&batch(&captured));
    assert!(decoded.requests[0].snapshot.matches(&file));
    assert_eq!(decoded.native.len(), 3);
    assert_eq!(decoded.security.len(), 4);
}

#[test]
fn admin_batch_preserves_full_nanosecond_integer_range() {
    for modified in [
        0,
        1,
        (1 << 53) + 1,
        NANOS,
        u64::MAX as u128,
        u64::MAX as u128 + 1,
        u128::MAX,
    ] {
        let snapshot = snapshot(modified);
        let bytes = serde_json::to_vec(&snapshot).unwrap();
        assert!(String::from_utf8_lossy(&bytes).contains(&format!("\"modified\":{modified},")));
        assert_eq!(
            serde_json::from_slice::<Snapshot>(&bytes).unwrap(),
            snapshot
        );
        roundtrip(&batch(&snapshot));
    }
}

#[test]
fn admin_batch_accepts_legacy_payloads_without_new_fields() {
    let mut batch = batch(&snapshot(NANOS));
    let legacy = serde_json::to_string(&batch)
        .unwrap()
        .replace("\"source\":\"electron\",", "");
    let decoded: AdminBatch = serde_json::from_slice(legacy.as_bytes()).unwrap();
    for request in &decoded.native[..2] {
        let (NativeAction::AppSwap { request } | NativeAction::PkgInstall { request }) =
            &request.action
        else {
            panic!("wrong action")
        };
        assert_eq!(request.source, UpdateSource::Homebrew);
        assert_eq!(request.installed_snapshot.modified, NANOS);
        assert_eq!(request.prepared_snapshot.modified, NANOS);
    }
    batch.native.clear();
    batch.security.clear();
    let legacy = serde_json::to_string(&batch)
        .unwrap()
        .replace(",\"native\":[],\"security\":[]", "");
    let decoded: AdminBatch = serde_json::from_slice(legacy.as_bytes()).unwrap();
    assert!(decoded.native.is_empty() && decoded.security.is_empty());
    assert_eq!(decoded.requests[0].snapshot, snapshot(NANOS));
}

#[test]
fn admin_batch_rejects_invalid_snapshot_nanoseconds() {
    let snapshot = snapshot(NANOS);
    let mut batch = batch(&snapshot);
    let mut envelopes = vec![serde_json::to_string(&snapshot).unwrap()];
    // Isolate each action so failure in the first one cannot hide another decoder's acceptance.
    let native = std::mem::take(&mut batch.native);
    let security = std::mem::take(&mut batch.security);
    envelopes.push(serde_json::to_string(&batch).unwrap());
    batch.requests.clear();
    for request in native {
        batch.native = vec![request];
        envelopes.push(serde_json::to_string(&batch).unwrap());
    }
    batch.native.clear();
    for request in security {
        batch.security = vec![request];
        envelopes.push(serde_json::to_string(&batch).unwrap());
    }
    for bad in [
        "-1",
        "1.0",
        "1.5",
        "1e3",
        "340282366920938463463374607431768211456",
        "\"123\"",
        "true",
        "null",
        "[]",
        "{}",
    ] {
        for (index, envelope) in envelopes.iter().enumerate() {
            let bytes = envelope.replace(
                &format!("\"modified\":{NANOS}"),
                &format!("\"modified\":{bad}"),
            );
            let rejected = if index == 0 {
                serde_json::from_slice::<Snapshot>(bytes.as_bytes()).is_err()
            } else {
                serde_json::from_slice::<AdminBatch>(bytes.as_bytes()).is_err()
            };
            assert!(rejected, "accepted {bad} in envelope {index}");
        }
    }
}
