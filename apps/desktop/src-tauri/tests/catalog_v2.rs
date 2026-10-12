use opennavo_desktop_lib::{
    catalog::{self, SnapshotInfo, v2},
    db::Database,
    model::*,
};
use std::{
    path::{Path, PathBuf},
    time::Instant,
};
fn directory() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../apps/server/testdata/desktop/v2")
}
fn metadata(dir: &Path) -> SnapshotInfo {
    serde_json::from_slice(&std::fs::read(dir.join("latest.json")).unwrap()).unwrap()
}
fn paths(info: &SnapshotInfo, dir: &Path, real: bool) -> Vec<(Locale, PathBuf)> {
    [Locale::JaJp, Locale::EnUs, Locale::ZhCn]
        .iter()
        .map(|locale| {
            (
                *locale,
                dir.join(if real {
                    info.text_packs[locale]
                        .url
                        .rsplit('/')
                        .next()
                        .unwrap()
                        .to_string()
                } else {
                    format!("text-{}.json.gz", locale.as_str())
                }),
            )
        })
        .collect()
}
fn gzip(value: &impl serde::Serialize) -> Vec<u8> {
    use std::io::Write;
    let mut writer = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
    writer
        .write_all(&serde_json::to_vec(value).unwrap())
        .unwrap();
    writer.finish().unwrap()
}
fn digest(bytes: &[u8]) -> String {
    use sha2::{Digest, Sha256};
    format!("{:x}", Sha256::digest(bytes))
}
#[test]
fn sparse_maps_and_fallback() {
    let text: LocalizedText =
        serde_json::from_str(r#"{"en-US":null,"zh-CN":"原文","ja-JP":"日本語"}"#).unwrap();
    assert_eq!(text.0.len(), 2);
    assert_eq!(text.fallback(Locale::RuRu, Locale::ZhCn).unwrap(), "原文");
    let mut text = text;
    text.0.insert(Locale::EnUs, "English".into());
    assert_eq!(
        text.fallback(Locale::RuRu, Locale::ZhCn).unwrap(),
        "English"
    );
    assert_eq!(text.fallback(Locale::JaJp, Locale::ZhCn).unwrap(), "日本語");
    assert!(!serde_json::to_string(&text).unwrap().contains("null"));
}
#[test]
fn v2_japanese_offline_search_text_only_delta_and_atomic_failure() {
    let temp = tempfile::tempdir().unwrap();
    let db = Database::open(&temp.path().join("catalog.db")).unwrap();
    let dir = directory();
    let mut info = metadata(&dir);
    let base = dir.join("catalog.json.gz");
    let mut files = paths(&info, &dir, false);
    // Synthetic Japanese is confined to temporary fixtures; never modify or translate development database content.
    let mut jp = v2::read_pack(&files[0].1, &info.text_packs[&Locale::JaJp]).unwrap();
    jp.items.retain(|item| item.token != "visual-studio-code");
    jp.items.push(v2::TextItem {
        kind: Kind::Cask,
        token: "visual-studio-code".into(),
        display_name: Some("日本語エディター".into()),
        summary: Some("ソースコードの編集".into()),
    });
    let raw = gzip(&jp);
    let pack_path = temp.path().join("ja.gz");
    std::fs::write(&pack_path, &raw).unwrap();
    files[0].1 = pack_path.clone();
    info.text_packs.get_mut(&Locale::JaJp).unwrap().sha256 = digest(&raw);
    info.text_packs.get_mut(&Locale::JaJp).unwrap().bytes = raw.len() as u64;
    assert_eq!(
        v2::import_files(&db, &base, &info, &files, Locale::JaJp, 1).unwrap(),
        40
    );
    let query = SearchQuery {
        q: "日本語".into(),
        kind: None,
        category: None,
        include_disabled: false,
        limit: 20,
        offset: 0,
    };
    let hits = catalog::search(&db, &query, 2).unwrap();
    assert_eq!(hits.items[0].item.token, "visual-studio-code");
    assert_eq!(
        hits.items[0].matched_name.as_deref(),
        Some("日本語エディター")
    );
    let query_name = ListQuery {
        kind: None,
        category: None,
        sort: ListSort::Name,
        include_fonts: false,
        include_libraries: false,
        include_disabled: false,
        limit: 200,
        offset: 0,
    };
    let position = |locale| {
        let items = catalog::list_locale(&db, &query_name, locale)
            .unwrap()
            .items;
        let editor = items
            .iter()
            .position(|v| v.token == "visual-studio-code")
            .unwrap();
        let chat = items.iter().position(|v| v.token == "wechat").unwrap();
        (editor, chat)
    };
    let (en_editor, en_chat) = position(Locale::EnUs);
    let (ja_editor, ja_chat) = position(Locale::JaJp);
    assert!(en_editor < en_chat);
    assert!(ja_editor > ja_chat);
    let old_hash = db.meta("snapshot_sha256").unwrap();
    let old_versions = db.meta("text_version:ja-JP").unwrap();
    let mut bad = info.clone();
    bad.text_packs.get_mut(&Locale::JaJp).unwrap().sha256 = "0".repeat(64);
    assert_eq!(
        v2::import_files(&db, &base, &bad, &files, Locale::JaJp, 3)
            .unwrap_err()
            .code,
        "E_CHECKSUM"
    );
    assert_eq!(db.meta("snapshot_sha256").unwrap(), old_hash);
    assert_eq!(db.meta("text_version:ja-JP").unwrap(), old_versions);
    // Base files/packages validate, then category writes fail inside the transaction; all earlier catalog deletes/inserts roll back.
    let mut snap = v2::read_base(&base, &info).unwrap();
    snap.categories.push(snap.categories[0].clone());
    let packs = files
        .iter()
        .map(|(locale, path)| v2::read_pack(path, &info.text_packs[locale]).unwrap())
        .collect::<Vec<_>>();
    assert!(v2::import(&db, snap, &info, &packs, Locale::JaJp, 4).is_err());
    assert_eq!(catalog::status(&db, false).unwrap().item_count, 40);
    assert_eq!(db.meta("snapshot_sha256").unwrap(), old_hash);
    assert_eq!(
        catalog::search(&db, &query, 5).unwrap().items[0].item.token,
        "visual-studio-code"
    );
    let mut changes:catalog::Changes=serde_json::from_value(serde_json::json!({"changes":[{"cursor":61,"op":"upsert","kind":"cask","token":"visual-studio-code","item":hits.items[0].item}],"nextCursor":61,"hasMore":false})).unwrap();
    let updated = changes.changes[0].item.as_mut().unwrap();
    updated.display_name.0.remove(&Locale::JaJp);
    updated
        .display_name
        .0
        .insert(Locale::RuRu, "Редактор".into());
    catalog::apply_changes(&db, &changes, 6).unwrap();
    assert!(catalog::search(&db, &query, 7).unwrap().items.is_empty());
    // Old language packs cannot overwrite newer delta mappings; new packs replace only the selected language and preserve others.
    v2::apply_text(&db, &[jp.clone()], 8).unwrap();
    assert!(catalog::search(&db, &query, 9).unwrap().items.is_empty());
    jp.cursor = 61;
    jp.version += 1;
    v2::apply_text(&db, &[jp], 10).unwrap();
    assert_eq!(catalog::search(&db, &query, 11).unwrap().total, 1);
    let item = catalog::get(&db, Kind::Cask, "visual-studio-code")
        .unwrap()
        .unwrap();
    assert_eq!(item.display_name.get(Locale::RuRu).unwrap(), "Редактор");
    assert_eq!(catalog::status(&db, false).unwrap().cursor, 61);
    changes.changes[0].op = "delete".into();
    changes.changes[0].item = None;
    changes.changes[0].cursor = 62;
    changes.next_cursor = 62;
    catalog::apply_changes(&db, &changes, 12).unwrap();
    assert!(catalog::search(&db, &query, 13).unwrap().items.is_empty());
    db.with(|c| {
        assert_eq!(
            c.query_row(
                "SELECT count(*) FROM catalog_texts WHERE token='visual-studio-code'",
                [],
                |r| r.get::<_, u32>(0)
            )?,
            0
        );
        Ok(())
    })
    .unwrap();
}
#[test]
fn v1_sqlite_migration_preserves_history_queue_and_cached_texts() {
    let temp = tempfile::tempdir().unwrap();
    let path = temp.path().join("old.db");
    let connection = rusqlite::Connection::open(&path).unwrap();
    connection
        .execute_batch(include_str!("../migrations/001_initial.sql"))
        .unwrap();
    connection
        .execute("INSERT INTO meta VALUES('schema_version','1')", [])
        .unwrap();
    connection.execute("INSERT INTO tasks(id,op,options,trigger,state,created_at) VALUES('task','update','{}','manual','succeeded',1)",[]).unwrap();
    connection.execute("INSERT INTO tasks(id,op,options,trigger,state,created_at) VALUES('queued','update','{}','manual','queued',2)",[]).unwrap();
    let mut old: catalog::Snapshot = serde_json::from_reader(flate2::read::GzDecoder::new(
        std::fs::File::open(directory().parent().unwrap().join("catalog.json.gz")).unwrap(),
    ))
    .unwrap();
    let mut item = old.items.remove(0);
    item.display_name
        .0
        .insert(Locale::ZhCn, "原文编辑器".into());
    item.display_name
        .0
        .insert(Locale::EnUs, "Source editor".into());
    item.display_name
        .0
        .insert(Locale::JaJp, "日本語エディター".into());
    connection.execute("INSERT INTO catalog_items(kind,token,data,name,display_name_zh,summary_zh,summary_en,version,version_changed_at) VALUES('cask',?1,?2,?3,'原文编辑器',?4,?5,?6,1)",rusqlite::params![item.token,serde_json::to_string(&item).unwrap(),item.name,item.summary.get(Locale::ZhCn),item.summary.get(Locale::EnUs),item.version]).unwrap();
    let category = &old.categories[0];
    connection
        .execute(
            "INSERT INTO categories VALUES(?1,?2,?3)",
            rusqlite::params![
                category.slug,
                serde_json::to_string(category).unwrap(),
                category.sort
            ],
        )
        .unwrap();
    connection
        .execute("INSERT INTO meta VALUES('catalog_cursor','60')", [])
        .unwrap();
    drop(connection);
    let db = Database::open(&path).unwrap();
    assert_eq!(db.meta("schema_version").unwrap().as_deref(), Some("4"));
    assert_eq!(db.tasks().unwrap().len(), 2);
    assert_eq!(db.tasks().unwrap()[0].id, "task");
    assert_eq!(db.tasks().unwrap()[1].state, TaskState::Queued);
    assert_eq!(db.meta("catalog_cursor").unwrap().as_deref(), Some("60"));
    let migrated = catalog::get(&db, Kind::Cask, &item.token).unwrap().unwrap();
    assert_eq!(
        migrated.display_name.get(Locale::ZhCn).unwrap(),
        "原文编辑器"
    );
    assert_eq!(
        migrated.display_name.get(Locale::EnUs).unwrap(),
        "Source editor"
    );
    assert_eq!(migrated.source_locale, Locale::EnUs);
    let query = SearchQuery {
        q: "日本語".into(),
        kind: None,
        category: None,
        include_disabled: false,
        limit: 20,
        offset: 0,
    };
    assert_eq!(
        catalog::search(&db, &query, 3).unwrap().items[0].item.token,
        item.token
    );
    db.with(|c| {
        assert!(
            c.prepare("SELECT display_name_zh FROM catalog_items")
                .is_err()
        );
        assert_eq!(
            c.query_row("SELECT count(*) FROM catalog_texts", [], |r| r
                .get::<_, u32>(0))?,
            3
        );
        assert!(
            c.query_row("SELECT count(*) FROM category_texts", [], |r| r
                .get::<_, u32>(0))?
                >= 2
        );
        Ok(())
    })
    .unwrap();
}
#[test]
fn measured_real_cask_v2_import() {
    let Ok(value) = std::env::var("OPENNAVO_SNAPSHOT_BENCH_DIR") else {
        return;
    };
    let dir = PathBuf::from(value);
    let info: SnapshotInfo =
        serde_json::from_slice(&std::fs::read(dir.join("manifest.json")).unwrap()).unwrap();
    let base = dir.join(info.url.rsplit('/').next().unwrap());
    let snapshot = v2::read_base(&base, &info).unwrap();
    let needed = v2::needed(&snapshot, Locale::JaJp);
    let files = needed
        .iter()
        .map(|locale| {
            (
                *locale,
                dir.join(info.text_packs[locale].url.rsplit('/').next().unwrap()),
            )
        })
        .collect::<Vec<_>>();
    for run in 1..=3 {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let start = Instant::now();
        v2::import_files(&db, &base, &info, &files, Locale::JaJp, 1).unwrap();
        let elapsed = start.elapsed();
        println!(
            "real Cask v2 import run={run} items={} locales={needed:?} milliseconds={}",
            info.item_count,
            elapsed.as_millis()
        );
        assert!(elapsed.as_secs_f64() < 3.0, "{elapsed:?}");
    }
}

#[test]
fn same_cursor_text_pack_preserves_authoritative_delta_categories() {
    let temp = tempfile::tempdir().unwrap();
    let db = Database::open(&temp.path().join("catalog.db")).unwrap();
    let dir = directory();
    let info = metadata(&dir);
    let base = v2::read_base(&dir.join("catalog.json.gz"), &info).unwrap();
    let files = paths(&info, &dir, false);
    let packs = files
        .iter()
        .map(|(locale, path)| v2::read_pack(path, &info.text_packs[locale]).unwrap())
        .collect::<Vec<_>>();
    v2::import(&db, base.clone(), &info, &packs, Locale::JaJp, 1).unwrap();
    let slug = &base.categories[0].slug;
    let mut old = packs
        .iter()
        .find(|p| p.locale == Locale::JaJp)
        .unwrap()
        .clone();
    old.categories = vec![v2::TextCategory {
        slug: slug.clone(),
        name: "古いカテゴリ".into(),
    }];
    // Before receiving deltas, a same-cursor language pack must still supplement base snapshot category text.
    v2::apply_text(&db, &[old.clone()], 1).unwrap();
    assert_eq!(
        catalog::categories(&db, Locale::JaJp)
            .unwrap()
            .iter()
            .find(|c| &c.slug == slug)
            .unwrap()
            .name,
        "古いカテゴリ"
    );
    let changes: catalog::Changes = serde_json::from_value(serde_json::json!({
        "changes":[], "nextCursor":info.cursor, "hasMore":false,
        "categories":base.categories,
        "categoryNames":{"en-US":{slug:"Latest category"},"ja-JP":{slug:"最新カテゴリ"}}
    }))
    .unwrap();
    catalog::apply_changes(&db, &changes, 2).unwrap();
    assert_eq!(old.cursor, changes.next_cursor);
    v2::apply_text(&db, &[old.clone()], 3).unwrap();
    assert_eq!(
        catalog::categories(&db, Locale::JaJp)
            .unwrap()
            .iter()
            .find(|c| &c.slug == slug)
            .unwrap()
            .name,
        "最新カテゴリ"
    );
    // The category source removed this translation; even a newer pack build must not resurrect it.
    let mut deleted = changes;
    deleted
        .category_names
        .as_mut()
        .unwrap()
        .remove(&Locale::JaJp);
    catalog::apply_changes(&db, &deleted, 4).unwrap();
    old.version += 1;
    v2::apply_text(&db, &[old.clone()], 5).unwrap();
    assert_eq!(
        catalog::categories(&db, Locale::JaJp)
            .unwrap()
            .iter()
            .find(|c| &c.slug == slug)
            .unwrap()
            .name,
        "Latest category"
    );
    // Existing databases without provenance markers must also prevent old packs from overwriting saved delta views.
    db.with(|c| {
        c.execute("DELETE FROM meta WHERE key='category_view'", [])?;
        Ok(())
    })
    .unwrap();
    v2::apply_text(&db, &[old.clone()], 6).unwrap();
    assert_eq!(
        catalog::categories(&db, Locale::JaJp)
            .unwrap()
            .iter()
            .find(|c| &c.slug == slug)
            .unwrap()
            .name,
        "Latest category"
    );
    v2::import(&db, base.clone(), &info, &packs, Locale::JaJp, 7).unwrap();
    assert_eq!(
        db.meta("category_view").unwrap().as_deref(),
        Some("snapshot")
    );
    v2::apply_text(&db, &[old], 8).unwrap();
    assert_eq!(
        catalog::categories(&db, Locale::JaJp)
            .unwrap()
            .iter()
            .find(|c| &c.slug == slug)
            .unwrap()
            .name,
        "古いカテゴリ"
    );
}

#[test]
fn full_snapshot_invalidates_unapplied_language_completion() {
    let temp = tempfile::tempdir().unwrap();
    let db = Database::open(&temp.path().join("catalog.db")).unwrap();
    let dir = directory();
    let mut info = metadata(&dir);
    let mut base = v2::read_base(&dir.join("catalog.json.gz"), &info).unwrap();
    let files = paths(&info, &dir, false);
    let packs = files
        .iter()
        .map(|(locale, path)| v2::read_pack(path, &info.text_packs[locale]).unwrap())
        .collect::<Vec<_>>();
    v2::import(&db, base.clone(), &info, &packs, Locale::JaJp, 1).unwrap();
    let mut added = base.items[0].clone();
    added.token = "new-language-app".into();
    let mut jp = packs
        .iter()
        .find(|p| p.locale == Locale::JaJp)
        .unwrap()
        .clone();
    jp.cursor += 1;
    jp.version += 1;
    jp.items.push(v2::TextItem {
        kind: Kind::Cask,
        token: added.token.clone(),
        display_name: Some("新しいアプリ".into()),
        summary: None,
    });
    v2::apply_text(&db, &[jp.clone()], 2).unwrap();
    // A new pack with missing apps is only partially applied and must not be marked complete.
    assert!(db.meta("text_version:ja-JP").unwrap().is_none());
    base.cursor = jp.cursor;
    base.items.push(added);
    info.cursor = base.cursor;
    info.item_count = base.items.len() as u32;
    for pack in info.text_packs.values_mut() {
        pack.cursor = base.cursor;
        pack.version = jp.version;
    }
    let current = packs
        .into_iter()
        .filter(|p| p.locale != Locale::JaJp)
        .map(|mut pack| {
            pack.cursor = base.cursor;
            pack.version = jp.version;
            pack
        })
        .collect::<Vec<_>>();
    // Simulate an erroneous completion marker from an old client; full import without Japanese must clear it.
    db.set_meta("text_version:ja-JP", &jp.version.to_string())
        .unwrap();
    v2::import(&db, base, &info, &current, Locale::EnUs, 3).unwrap();
    assert!(db.meta("text_version:ja-JP").unwrap().is_none());
    assert!(
        catalog::get(&db, Kind::Cask, "new-language-app")
            .unwrap()
            .unwrap()
            .display_name
            .get(Locale::JaJp)
            .is_none()
    );
    v2::apply_text(&db, &[jp.clone()], 4).unwrap();
    assert_eq!(
        catalog::get(&db, Kind::Cask, "new-language-app")
            .unwrap()
            .unwrap()
            .display_name
            .get(Locale::JaJp)
            .unwrap(),
        "新しいアプリ"
    );
    assert_eq!(
        db.meta("text_version:ja-JP").unwrap(),
        Some(jp.version.to_string())
    );
}
