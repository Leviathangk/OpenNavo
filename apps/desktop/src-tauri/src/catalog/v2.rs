use super::*;
use sha2::{Digest, Sha256};
use std::io::Read;
use std::path::PathBuf;
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TextPackInfo {
    pub locale: Locale,
    pub version: i64,
    pub cursor: i64,
    pub url: String,
    pub sha256: String,
    pub bytes: u64,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TextItem {
    pub kind: Kind,
    pub token: String,
    pub display_name: Option<String>,
    pub summary: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TextCategory {
    pub slug: String,
    pub name: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TextPack {
    pub format_version: u32,
    pub locale: Locale,
    pub version: i64,
    pub cursor: i64,
    pub generated_at: String,
    pub items: Vec<TextItem>,
    pub categories: Vec<TextCategory>,
}

pub fn read_gzip<T: serde::de::DeserializeOwned>(
    path: &Path,
    sha256: &str,
    expected: u64,
) -> Result<T, AppError> {
    if expected == 0
        || expected > 64 * 1024 * 1024
        || sha256.len() != 64
        || !sha256.bytes().all(|b| b.is_ascii_hexdigit())
    {
        return Err(protocol("snapshot_metadata"));
    }
    let mut file = std::fs::File::open(path)?;
    let mut hash = Sha256::new();
    let mut bytes = 0u64;
    let mut buffer = [0; 65536];
    loop {
        let n = file.read(&mut buffer)?;
        if n == 0 {
            break;
        }
        bytes += n as u64;
        if bytes > expected {
            return Err(AppError::new("E_CHECKSUM", "snapshot_checksum"));
        }
        hash.update(&buffer[..n]);
    }
    if bytes != expected || format!("{:x}", hash.finalize()) != sha256.to_ascii_lowercase() {
        return Err(AppError::new("E_CHECKSUM", "snapshot_checksum"));
    }
    // Read decompression boundaries through EOF to catch oversized trailing data after valid JSON.
    let mut raw = Vec::new();
    flate2::read::GzDecoder::new(std::fs::File::open(path)?)
        .take(128 * 1024 * 1024 + 1)
        .read_to_end(&mut raw)?;
    if raw.len() > 128 * 1024 * 1024 {
        return Err(protocol("snapshot_too_large"));
    }
    serde_json::from_slice(&raw).map_err(|e| protocol("snapshot_json").with_detail(e.to_string()))
}
pub fn read_base(path: &Path, info: &SnapshotInfo) -> Result<Snapshot, AppError> {
    if info.format_version != 2 || info.cursor < 0 || info.text_packs.len() != 6 {
        return Err(protocol("snapshot_metadata"));
    }
    for locale in crate::locales_gen::LOCALES {
        let pack = info
            .text_packs
            .get(&locale)
            .ok_or_else(|| protocol("missing_text_pack"))?;
        if pack.locale != locale || pack.cursor != info.cursor || pack.version <= 0 {
            return Err(protocol("text_pack_metadata"));
        }
    }
    let raw: serde_json::Value = read_gzip(path, &info.sha256, info.bytes)?;
    for field in ["items", "categories"] {
        for item in raw
            .get(field)
            .and_then(serde_json::Value::as_array)
            .ok_or_else(|| protocol("snapshot_json"))?
        {
            if item.get("sourceLocale").is_none()
                || item.get("displayName").is_some()
                || item.get("summary").is_some()
                || item.get("name").is_some_and(|v| !v.is_string())
            {
                return Err(protocol("base_text_or_source"));
            }
        }
    }
    let base: Snapshot = serde_json::from_value(raw)?;
    if base.format_version != 2
        || base.cursor != info.cursor
        || base.items.len() != info.item_count as usize
    {
        return Err(protocol("snapshot_mismatch"));
    }
    chrono::DateTime::parse_from_rfc3339(&base.generated_at)
        .map_err(|_| protocol("snapshot_date"))?;
    let mut identities = HashSet::new();
    for item in &base.items {
        if item.kind != Kind::Cask || !identities.insert((item.kind, &item.token)) {
            return Err(protocol("base_identity"));
        }
    }
    let mut slugs = HashSet::new();
    for category in &base.categories {
        if !slugs.insert(&category.slug) || category.applies_to == CategoryAppliesTo::Formula {
            return Err(protocol("category_identity"));
        }
    }
    Ok(base)
}
pub fn needed(base: &Snapshot, locale: Locale) -> HashSet<Locale> {
    let mut needed = HashSet::from([locale, Locale::EnUs]);
    needed.extend(base.items.iter().map(|v| v.source_locale));
    needed.extend(base.categories.iter().map(|v| v.source_locale));
    needed
}
pub fn read_pack(path: &Path, info: &TextPackInfo) -> Result<TextPack, AppError> {
    let pack: TextPack = read_gzip(path, &info.sha256, info.bytes)?;
    if pack.format_version != 2
        || pack.locale != info.locale
        || pack.version != info.version
        || pack.cursor != info.cursor
        || pack.version <= 0
        || pack.cursor < 0
    {
        return Err(protocol("text_pack_mismatch"));
    }
    chrono::DateTime::parse_from_rfc3339(&pack.generated_at)
        .map_err(|_| protocol("text_pack_date"))?;
    let mut identities = HashSet::new();
    for item in &pack.items {
        crate::brew::args::validate_token(&item.token)?;
        if item.kind != Kind::Cask
            || !identities.insert((item.kind, &item.token))
            || (item.display_name.is_none() && item.summary.is_none())
            || item
                .display_name
                .iter()
                .chain(item.summary.iter())
                .any(String::is_empty)
        {
            return Err(protocol("text_pack_identity"));
        }
    }
    let mut slugs = HashSet::new();
    for category in &pack.categories {
        if category.name.is_empty() || !slugs.insert(&category.slug) {
            return Err(protocol("text_category_identity"));
        }
    }
    Ok(pack)
}
fn merge(
    items: &mut [CatalogItem],
    categories: &mut [SnapshotCategory],
    packs: &[TextPack],
    strict: bool,
    protected: &HashSet<(Kind, String)>,
    protected_categories: bool,
) -> Result<(), AppError> {
    let mut by_identity: HashMap<_, _> = items
        .iter_mut()
        .map(|i| ((i.kind, i.token.clone()), i))
        .collect();
    let mut by_slug: HashMap<_, _> = categories.iter_mut().map(|c| (c.slug.clone(), c)).collect();
    let mut locales = HashSet::new();
    for pack in packs {
        if !locales.insert(pack.locale) {
            return Err(protocol("duplicate_text_locale"));
        }
        for ((kind, token), item) in &mut by_identity {
            if !protected.contains(&(*kind, token.clone())) {
                item.display_name.0.remove(&pack.locale);
                item.summary.0.remove(&pack.locale);
            }
        }
        if !protected_categories {
            for category in by_slug.values_mut() {
                category.name.0.remove(&pack.locale);
            }
        }
        for text in &pack.items {
            let identity = (text.kind, text.token.clone());
            if let Some(item) = by_identity.get_mut(&identity) {
                if protected.contains(&identity) {
                    continue;
                }
                if let Some(name) = &text.display_name {
                    item.display_name.0.insert(pack.locale, name.clone());
                }
                if let Some(summary) = &text.summary {
                    item.summary.0.insert(pack.locale, summary.clone());
                }
            } else if strict {
                return Err(protocol("unknown_text_identity"));
            }
        }
        for text in pack.categories.iter().filter(|_| !protected_categories) {
            if let Some(category) = by_slug.get_mut(&text.slug) {
                category.name.0.insert(pack.locale, text.name.clone());
            } else if strict {
                return Err(protocol("unknown_text_category"));
            }
        }
    }
    Ok(())
}
pub(super) fn insert_category(
    connection: &rusqlite::Connection,
    category: &SnapshotCategory,
) -> Result<(), AppError> {
    connection.execute(
        "INSERT INTO categories(slug,data,sort) VALUES(?1,?2,?3)",
        params![
            category.slug,
            serde_json::to_string(category)?,
            category.sort
        ],
    )?;
    for (locale, name) in &category.name.0 {
        connection
            .prepare_cached("INSERT INTO category_texts(slug,locale,name) VALUES(?1,?2,?3)")?
            .execute(params![category.slug, locale.as_str(), name])?;
    }
    Ok(())
}
pub fn import_files(
    database: &Database,
    base_path: &Path,
    info: &SnapshotInfo,
    paths: &[(Locale, PathBuf)],
    locale: Locale,
    now: i64,
) -> Result<u32, AppError> {
    let base = read_base(base_path, info)?;
    let packs = paths
        .iter()
        .map(|(locale, path)| {
            read_pack(
                path,
                info.text_packs
                    .get(locale)
                    .ok_or_else(|| protocol("missing_text_pack"))?,
            )
        })
        .collect::<Result<Vec<_>, _>>()?;
    import(database, base, info, &packs, locale, now)
}
pub fn import(
    database: &Database,
    mut base: Snapshot,
    info: &SnapshotInfo,
    packs: &[TextPack],
    locale: Locale,
    now: i64,
) -> Result<u32, AppError> {
    if base.format_version != 2
        || base.cursor != info.cursor
        || base.items.len() != info.item_count as usize
    {
        return Err(protocol("snapshot_mismatch"));
    }
    let supplied: HashSet<_> = packs.iter().map(|p| p.locale).collect();
    if !needed(&base, locale).is_subset(&supplied)
        || packs.iter().any(|p| {
            p.cursor != base.cursor
                || info
                    .text_packs
                    .get(&p.locale)
                    .is_none_or(|m| m.version != p.version)
        })
    {
        return Err(protocol("missing_text_pack"));
    }
    database.with(|connection| {
        let tx = connection.transaction()?;
        // Keep cached other languages, but only for objects still present in the new base file.
        for item in &mut base.items {
            let old: Option<String> = tx
                .query_row(
                    "SELECT data FROM catalog_items WHERE kind=?1 AND token=?2",
                    params![item.kind.as_str(), item.token],
                    |r| r.get(0),
                )
                .optional()?;
            if let Some(old) = old {
                let old: CatalogItem = serde_json::from_str(&old)?;
                item.display_name = old.display_name;
                item.summary = old.summary;
            }
        }
        let cached: HashMap<_, _> = load_categories(&tx)?
            .into_iter()
            .map(|c| (c.slug, c.name))
            .collect();
        for category in &mut base.categories {
            if let Some(name) = cached.get(&category.slug) {
                category.name = name.clone();
            }
        }
        merge(
            &mut base.items,
            &mut base.categories,
            packs,
            true,
            &HashSet::new(),
            false,
        )?;
        tx.execute_batch(
            "DELETE FROM catalog_fts WHERE kind<>'link'; DELETE FROM catalog_items WHERE kind<>'link'; DELETE FROM categories;",
        )?;
        for item in &base.items {
            upsert_new(&tx, item)?;
        }
        rebuild_texts(&tx)?;
        for category in &base.categories {
            insert_category(&tx, category)?;
        }
        set_meta(&tx, "catalog_cursor", &base.cursor.to_string())?;
        set_meta(&tx, "catalog_synced_at", &now.to_string())?;
        set_meta(&tx, "snapshot_imported_at", &now.to_string())?;
        set_meta(&tx, "snapshot_sha256", &info.sha256)?;
        set_meta(&tx, "snapshot_format", "2")?;
        set_meta(&tx, "category_cursor", &base.cursor.to_string())?;
        set_meta(&tx, "category_view", "snapshot")?;
        // Languages not reapplied with the new base remain usable as fallbacks but lose complete-pack markers.
        tx.execute("DELETE FROM meta WHERE key GLOB 'text_version:*'", [])?;
        for pack in packs {
            set_meta(
                &tx,
                &format!("text_version:{}", pack.locale.as_str()),
                &pack.version.to_string(),
            )?;
        }
        tx.commit()?;
        Ok(base.items.len() as u32)
    })
}
pub fn apply_text(database: &Database, packs: &[TextPack], now: i64) -> Result<u32, AppError> {
    database.with(|connection| {
        let tx = connection.transaction()?;
        let data = tx
            .prepare("SELECT data FROM catalog_items")?
            .query_map([], |r| r.get::<_, String>(0))?
            .collect::<Result<Vec<_>, _>>()?;
        let mut items = data
            .into_iter()
            .map(|s| serde_json::from_str(&s))
            .collect::<Result<Vec<CatalogItem>, _>>()?;
        let mut categories = load_categories(&tx)?;
        // When deltas are newer, packs must neither overwrite new translations nor resurrect deleted objects.
        let category_cursor = tx
            .query_row(
                "SELECT value FROM meta WHERE key='category_cursor'",
                [],
                |r| r.get::<_, String>(0),
            )
            .optional()?
            .and_then(|s| s.parse::<i64>().ok())
            .unwrap_or(0);
        // Category edits do not advance package cursors; the complete delta view takes precedence independent of pack build versions.
        // Conservatively retain categories without legacy provenance until the next full import.
        let delta_categories = tx
            .query_row(
                "SELECT value FROM meta WHERE key='category_view'",
                [],
                |r| r.get::<_, String>(0),
            )
            .optional()?
            .as_deref()
            != Some("snapshot");
        let cursor = tx
            .query_row(
                "SELECT value FROM meta WHERE key='catalog_cursor'",
                [],
                |r| r.get::<_, String>(0),
            )
            .optional()?
            .and_then(|s| s.parse::<i64>().ok())
            .unwrap_or(0);
        for pack in packs {
            let protected = tx
                .prepare("SELECT kind,token FROM catalog_items WHERE text_cursor>?1")?
                .query_map([pack.cursor], |r| {
                    Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?))
                })?
                .map(|r| {
                    let (k, t) = r?;
                    Ok((
                        if k == "cask" {
                            Kind::Cask
                        } else {
                            Kind::Formula
                        },
                        t,
                    ))
                })
                .collect::<Result<HashSet<_>, rusqlite::Error>>()?;
            merge(
                &mut items,
                &mut categories,
                std::slice::from_ref(pack),
                false,
                &protected,
                delta_categories || pack.cursor < category_cursor,
            )?;
        }
        for item in &items {
            upsert(&tx, item)?;
        }
        tx.execute("DELETE FROM categories", [])?;
        for category in &categories {
            insert_category(&tx, category)?;
        }
        for pack in packs {
            if pack.cursor > cursor {
                // Skip apps not yet present when predownloaded; permit reapplication until the catalog catches up.
                tx.execute(
                    "DELETE FROM meta WHERE key=?1",
                    [format!("text_version:{}", pack.locale.as_str())],
                )?;
                continue;
            }
            set_meta(
                &tx,
                &format!("text_version:{}", pack.locale.as_str()),
                &pack.version.to_string(),
            )?;
        }
        set_meta(&tx, "catalog_synced_at", &now.to_string())?;
        tx.commit()?;
        Ok(items.len() as u32)
    })
}
pub(super) fn apply_categories(
    connection: &rusqlite::Connection,
    changes: &Changes,
) -> Result<(), AppError> {
    match (&changes.categories, &changes.category_names) {
        (None, None) => Ok(()),
        (Some(categories), Some(names)) => {
            let mut categories = categories.clone();
            let mut slugs = HashSet::new();
            for category in &mut categories {
                if !slugs.insert(category.slug.clone()) {
                    return Err(protocol("duplicate_category"));
                }
                category.name.0.clear();
                for (locale, names) in names {
                    if let Some(name) = names.get(&category.slug)
                        && !name.is_empty()
                    {
                        category.name.0.insert(*locale, name.clone());
                    }
                }
            }
            if names
                .values()
                .any(|names| names.keys().any(|slug| !slugs.contains(slug)))
            {
                return Err(protocol("unknown_text_category"));
            }
            set_meta(
                connection,
                "category_cursor",
                &changes.next_cursor.to_string(),
            )?;
            set_meta(connection, "category_view", "delta")?;
            connection.execute("DELETE FROM categories", [])?;
            for category in &categories {
                insert_category(connection, category)?;
            }
            Ok(())
        }
        _ => Err(protocol("category_pair")),
    }
}
