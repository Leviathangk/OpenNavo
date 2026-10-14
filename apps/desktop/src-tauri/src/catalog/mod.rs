pub mod sync;
pub mod v2;
use crate::{AppError, db::Database, model::*};
use rusqlite::{OptionalExtension, params};
use serde::{Deserialize, Serialize};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SnapshotCategory {
    pub slug: String,
    pub parent: Option<String>,
    #[serde(default)]
    pub name: LocalizedText,
    #[serde(default = "category_source")]
    pub source_locale: Locale,
    pub icon: String,
    pub sort: i32,
    pub hidden_by_default: bool,
    pub applies_to: CategoryAppliesTo,
}
fn category_source() -> Locale {
    Locale::ZhCn
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    pub format_version: u32,
    pub generated_at: String,
    pub cursor: i64,
    pub categories: Vec<SnapshotCategory>,
    pub items: Vec<CatalogItem>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SnapshotInfo {
    pub format_version: u32,
    pub cursor: i64,
    pub item_count: u32,
    pub url: String,
    pub sha256: String,
    pub bytes: u64,
    pub created_at: String,
    #[serde(default)]
    pub text_packs: HashMap<Locale, v2::TextPackInfo>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Change {
    pub cursor: i64,
    pub op: String,
    pub kind: Kind,
    pub token: String,
    pub item: Option<CatalogItem>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Changes {
    pub changes: Vec<Change>,
    pub next_cursor: i64,
    pub has_more: bool,
    #[serde(default)]
    pub categories: Option<Vec<SnapshotCategory>>,
    #[serde(default)]
    pub category_names: Option<HashMap<Locale, HashMap<String, String>>>,
}
#[derive(Deserialize)]
pub struct Response<T> {
    pub code: String,
    pub data: Option<T>,
}
fn protocol(message: &str) -> AppError {
    AppError::new("E_NETWORK", message)
}

pub fn import_file(
    database: &Database,
    path: &Path,
    info: &SnapshotInfo,
    now: i64,
) -> Result<u32, AppError> {
    if info.format_version != 1 || info.cursor < 0 || info.bytes > 64 * 1024 * 1024 {
        return Err(protocol("snapshot_metadata"));
    }
    let snapshot: Snapshot = v2::read_gzip(path, &info.sha256, info.bytes)?;

    import_snapshot(database, &snapshot, info, now)
}
pub fn import_snapshot(
    database: &Database,
    snapshot: &Snapshot,
    info: &SnapshotInfo,
    now: i64,
) -> Result<u32, AppError> {
    if info.format_version != 1
        || info.cursor < 0
        || snapshot.format_version != 1
        || snapshot.cursor != info.cursor
        || snapshot.items.len() != info.item_count as usize
    {
        return Err(protocol("snapshot_mismatch"));
    }
    chrono::DateTime::parse_from_rfc3339(&snapshot.generated_at)
        .map_err(|_| protocol("snapshot_date"))?;
    let mut identities = HashSet::new();
    for item in &snapshot.items {
        if !identities.insert((item.kind, item.token.as_str())) {
            return Err(protocol("duplicate_catalog_identity"));
        }
    }
    database.with(|connection| {
        let transaction = connection.transaction()?;
        transaction.execute_batch(
            "DELETE FROM catalog_items; DELETE FROM catalog_fts; DELETE FROM categories;",
        )?;
        for item in &snapshot.items {
            upsert_new(&transaction, item)?;
        }
        rebuild_texts(&transaction)?;
        for category in &snapshot.categories {
            v2::insert_category(&transaction, category)?;
        }
        set_meta(&transaction, "catalog_cursor", &snapshot.cursor.to_string())?;
        set_meta(&transaction, "catalog_synced_at", &now.to_string())?;
        set_meta(&transaction, "snapshot_sha256", &info.sha256)?;
        set_meta(&transaction, "snapshot_imported_at", &now.to_string())?;
        transaction.commit()?;
        Ok(snapshot.items.len() as u32)
    })
}
fn set_meta(connection: &rusqlite::Connection, key: &str, value: &str) -> Result<(), AppError> {
    connection.execute("INSERT INTO meta(key,value) VALUES(?1,?2) ON CONFLICT(key) DO UPDATE SET value=excluded.value",params![key,value])?;
    Ok(())
}
fn integer(value: u64) -> Result<i64, AppError> {
    i64::try_from(value).map_err(|_| protocol("catalog_integer_range"))
}
pub(super) fn upsert(
    connection: &rusqlite::Connection,
    item: &CatalogItem,
) -> Result<(), AppError> {
    upsert_impl(connection, item, true)
}
pub(super) fn upsert_new(
    connection: &rusqlite::Connection,
    item: &CatalogItem,
) -> Result<(), AppError> {
    upsert_impl(connection, item, false)
}
fn upsert_impl(
    connection: &rusqlite::Connection,
    item: &CatalogItem,
    replace: bool,
) -> Result<(), AppError> {
    crate::brew::args::validate_token(&item.token)?;
    if let Some(tap) = &item.tap {
        crate::brew::args::validate_tap(tap)?;
    }
    let changed = chrono::DateTime::parse_from_rfc3339(&item.version_changed_at)
        .map_err(|_| protocol("catalog_date"))?
        .timestamp_millis();
    let request = item.on_request.as_ref();
    // Base imports insert into empty tables without RETURNING temporary rows; only deltas need rowid to update FTS.
    const INSERT: &str = "INSERT INTO catalog_items(kind,token,data,name,version,primary_category,popularity,installs_30d,installs_90d,installs_365d,on_request_30d,on_request_90d,on_request_365d,download_size,rank_30d,hidden,disabled,is_font,is_library,version_changed_at) VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14,?15,?16,?17,?18,?19,?20) ON CONFLICT(kind,token) DO UPDATE SET data=excluded.data,name=excluded.name,version=excluded.version,primary_category=excluded.primary_category,popularity=excluded.popularity,installs_30d=excluded.installs_30d,installs_90d=excluded.installs_90d,installs_365d=excluded.installs_365d,on_request_30d=excluded.on_request_30d,on_request_90d=excluded.on_request_90d,on_request_365d=excluded.on_request_365d,download_size=excluded.download_size,rank_30d=excluded.rank_30d,hidden=excluded.hidden,disabled=excluded.disabled,is_font=excluded.is_font,is_library=excluded.is_library,version_changed_at=excluded.version_changed_at";
    let data = serde_json::to_string(item)?;
    let values = params![
        item.kind.as_str(),
        item.token,
        data,
        item.name,
        item.version,
        item.categories.first(),
        item.popularity,
        integer(item.installs_30d)?,
        item.installs_90d.map(integer).transpose()?,
        item.installs_365d.map(integer).transpose()?,
        request.map(|v| integer(v.d30)).transpose()?,
        request.map(|v| integer(v.d90)).transpose()?,
        request.map(|v| integer(v.d365)).transpose()?,
        item.download_size.map(integer).transpose()?,
        item.rank_30d,
        item.hidden,
        item.disabled,
        item.is_font,
        item.is_library,
        changed
    ];
    let rowid = if replace {
        connection
            .prepare_cached(&format!("{INSERT} RETURNING rowid"))?
            .query_row(values, |row| row.get::<_, i64>(0))?
    } else {
        connection.prepare_cached(INSERT)?.execute(values)?;
        0
    };
    if replace {
        connection
            .prepare_cached("DELETE FROM catalog_texts WHERE kind=?1 AND token=?2")?
            .execute(params![item.kind.as_str(), item.token])?;
    }
    if replace {
        let locales: HashSet<_> = item
            .display_name
            .0
            .keys()
            .chain(item.summary.0.keys())
            .copied()
            .collect();
        for locale in locales {
            connection.prepare_cached("INSERT INTO catalog_texts(kind,token,locale,display_name,summary) VALUES(?1,?2,?3,?4,?5)")?.execute(params![item.kind.as_str(),item.token,locale.as_str(),item.display_name.get(locale),item.summary.get(locale)])?;
        }
    }
    if replace {
        let names = item
            .names
            .iter()
            .chain(item.binaries.iter())
            .map(String::as_str)
            .collect::<Vec<_>>()
            .join(" ");
        let displays = item
            .display_name
            .0
            .values()
            .cloned()
            .collect::<Vec<_>>()
            .join(" ");
        let summary = item
            .summary
            .0
            .values()
            .cloned()
            .collect::<Vec<_>>()
            .join(" ");
        connection.prepare_cached("INSERT OR REPLACE INTO catalog_fts(rowid,kind,token,names,display_name,summary,pinyin) VALUES(?1,?2,?3,?4,?5,?6,?7)")?.execute(params![rowid,item.kind.as_str(),item.token,names,displays,summary,item.pinyin])?;
    }
    Ok(())
}
/// Build text tables in bulk during base import to avoid one Rust/SQLite crossing per locale.
pub(super) fn rebuild_texts(connection: &rusqlite::Connection) -> Result<(), AppError> {
    connection.execute_batch(r#"
        INSERT INTO catalog_texts(kind,token,locale,display_name,summary)
        SELECT p.kind,p.token,j.key,j.value,json_extract(p.data,'$.summary.' || '"' || j.key || '"') FROM catalog_items p,json_each(p.data,'$.displayName') j WHERE j.value IS NOT NULL;
        INSERT INTO catalog_texts(kind,token,locale,display_name,summary)
        SELECT p.kind,p.token,j.key,NULL,j.value FROM catalog_items p,json_each(p.data,'$.summary') j WHERE j.value IS NOT NULL ON CONFLICT(kind,token,locale) DO UPDATE SET summary=excluded.summary;
        INSERT INTO catalog_fts(rowid,kind,token,names,display_name,summary,pinyin)
        SELECT p.rowid,p.kind,p.token,p.name || ' ' || COALESCE((SELECT group_concat(value,' ') FROM json_each(p.data,'$.names')),'') || ' ' || COALESCE((SELECT group_concat(value,' ') FROM json_each(p.data,'$.binaries')),''),COALESCE((SELECT group_concat(display_name,' ') FROM catalog_texts t WHERE t.kind=p.kind AND t.token=p.token),''),COALESCE((SELECT group_concat(summary,' ') FROM catalog_texts t WHERE t.kind=p.kind AND t.token=p.token),''),json_extract(p.data,'$.pinyin') FROM catalog_items p;
    "#)?;
    Ok(())
}
pub fn apply_changes(database: &Database, changes: &Changes, now: i64) -> Result<u32, AppError> {
    database.with(|connection|{
    let transaction=connection.transaction()?;
    let cursor=transaction.query_row("SELECT value FROM meta WHERE key='catalog_cursor'",[],|r|r.get::<_,String>(0)).optional()?.and_then(|v|v.parse::<i64>().ok()).unwrap_or(0);
    if changes.next_cursor<cursor{return Err(protocol("cursor_regression"));}
    let mut applied=0;let mut previous=0;
    for change in &changes.changes{
        if change.cursor<=previous||change.cursor>changes.next_cursor{return Err(protocol("invalid_change_cursor"));}previous=change.cursor;
        crate::brew::args::validate_token(&change.token)?;if change.cursor<=cursor{continue;}
        match change.op.as_str(){
            "upsert"=>{let item=change.item.as_ref().ok_or_else(||protocol("missing_change_item"))?;if item.kind!=change.kind||item.token!=change.token{return Err(protocol("change_identity"));}upsert(&transaction,item)?;transaction.execute("UPDATE catalog_items SET text_cursor=?1 WHERE kind=?2 AND token=?3",params![change.cursor,change.kind.as_str(),change.token])?;},
            "delete"=>{transaction.execute("DELETE FROM catalog_fts WHERE rowid=(SELECT rowid FROM catalog_items WHERE kind=?1 AND token=?2)",params![change.kind.as_str(),change.token])?;transaction.execute("DELETE FROM catalog_items WHERE kind=?1 AND token=?2",params![change.kind.as_str(),change.token])?;},
            _=>return Err(protocol("change_operation")),
        }applied+=1;
    }
    if changes.has_more && changes.next_cursor<=cursor && changes.changes.is_empty(){return Err(protocol("nonprogressing_changes"));}
    v2::apply_categories(&transaction, changes)?;
    set_meta(&transaction,"catalog_cursor",&changes.next_cursor.to_string())?;set_meta(&transaction,"catalog_synced_at",&now.to_string())?;transaction.commit()?;Ok(applied)
})
}
pub fn status(database: &Database, syncing: bool) -> Result<CatalogStatus, AppError> {
    database.with(|connection| {
        let count: u32 =
            connection.query_row("SELECT count(*) FROM catalog_items", [], |r| r.get(0))?;
        let meta = |key: &str| -> Result<Option<i64>, AppError> {
            Ok(connection
                .query_row("SELECT value FROM meta WHERE key=?1", [key], |r| {
                    r.get::<_, String>(0)
                })
                .optional()?
                .and_then(|s| s.parse().ok()))
        };
        Ok(CatalogStatus {
            cursor: meta("catalog_cursor")?.unwrap_or(0),
            item_count: count,
            synced_at: meta("catalog_synced_at")?,
            syncing,
        })
    })
}
pub fn get(database: &Database, kind: Kind, token: &str) -> Result<Option<CatalogItem>, AppError> {
    crate::brew::args::validate_token(token)?;
    database.with(|connection| {
        let data: Option<String> = connection
            .query_row(
                "SELECT data FROM catalog_items WHERE kind=?1 AND token=?2",
                params![kind.as_str(), token],
                |r| r.get(0),
            )
            .optional()?;
        data.map(|data| serde_json::from_str(&data).map_err(AppError::from))
            .transpose()
    })
}
fn descendants(
    connection: &rusqlite::Connection,
    category: Option<&str>,
) -> Result<Option<HashSet<String>>, AppError> {
    let Some(slug) = category else {
        return Ok(None);
    };
    if slug.len() > 128 {
        return Err(AppError::new("E_INVALID_ARG", "category"));
    }
    let categories = load_categories(connection)?;
    let mut slugs = HashSet::from([slug.to_owned()]);
    loop {
        let before = slugs.len();
        for category in &categories {
            if category
                .parent
                .as_ref()
                .is_some_and(|parent| slugs.contains(parent))
            {
                slugs.insert(category.slug.clone());
            }
        }
        if slugs.len() == before {
            break;
        }
    }
    Ok(Some(slugs))
}
fn load_categories(connection: &rusqlite::Connection) -> Result<Vec<SnapshotCategory>, AppError> {
    let mut statement = connection.prepare("SELECT data FROM categories ORDER BY sort,slug")?;
    statement
        .query_map([], |r| r.get::<_, String>(0))?
        .map(|s| Ok(serde_json::from_str(&s?)?))
        .collect()
}
pub fn categories(database: &Database, locale: Locale) -> Result<Vec<CategoryItem>, AppError> {
    database.with(|connection| {
        let categories = load_categories(connection)?;
        let mut direct: HashMap<String, HashSet<(Kind, String)>> = HashMap::new();
        let mut statement =
            connection.prepare("SELECT kind,token,data FROM catalog_items WHERE hidden=0")?;
        for row in statement.query_map([], |r| r.get::<_, String>(2))? {
            let item: CatalogItem = serde_json::from_str(&row?)?;
            for slug in &item.categories {
                direct
                    .entry(slug.clone())
                    .or_default()
                    .insert((item.kind, item.token.clone()));
            }
        }
        let mut result = Vec::new();
        for category in categories {
            let children = descendants(connection, Some(&category.slug))?.unwrap_or_default();
            let mut identities = HashSet::new();
            for child in children {
                if let Some(items) = direct.get(&child) {
                    identities.extend(items.iter().cloned());
                }
            }
            let name = category
                .name
                .fallback(locale, category.source_locale)
                .cloned()
                .unwrap_or_else(|| category.slug.clone());
            result.push(CategoryItem {
                slug: category.slug,
                parent: category.parent,
                name,
                icon: category.icon,
                applies_to: category.applies_to,
                hidden_by_default: category.hidden_by_default,
                sort: category.sort,
                package_count: identities.len() as u32,
            });
        }
        Ok(result)
    })
}
pub fn list(database: &Database, query: &ListQuery) -> Result<Page<CatalogItem>, AppError> {
    list_locale(database, query, Locale::EnUs)
}
pub fn list_locale(
    database: &Database,
    query: &ListQuery,
    locale: Locale,
) -> Result<Page<CatalogItem>, AppError> {
    if query.limit > 200 {
        return Err(AppError::new("E_INVALID_ARG", "list_limit"));
    }
    database.with(|connection|{
        let children=descendants(connection,query.category.as_deref())?;
        let order=match query.sort{ListSort::Popular=>"popularity DESC",ListSort::Updated=>"version_changed_at DESC",ListSort::Name=>"lower(name)",ListSort::Installs30d=>"coalesce(on_request_30d,installs_30d) DESC NULLS LAST",ListSort::Installs90d=>"coalesce(on_request_90d,installs_90d) DESC NULLS LAST",ListSort::Installs365d=>"coalesce(on_request_365d,installs_365d) DESC NULLS LAST"};
        let sql=format!("SELECT data FROM catalog_items WHERE hidden=0 AND (?1 IS NULL OR kind=?1) AND (?2 OR disabled=0) AND (?3 OR is_font=0) AND (?4 OR is_library=0) ORDER BY {order},kind,token");
        let mut statement=connection.prepare(&sql)?;let mut items=Vec::new();
        for row in statement.query_map(params![query.kind.map(Kind::as_str),query.include_disabled,query.include_fonts,query.include_libraries],|r|r.get::<_,String>(0))?{
            let item:CatalogItem=serde_json::from_str(&row?)?;
            if children.as_ref().is_none_or(|children|item.categories.iter().any(|c|children.contains(c))){items.push(item);}
        }
        if query.sort==ListSort::Name {items.sort_by(|a,b| {
            let name=|item:&CatalogItem|item.display_name.fallback(locale,item.source_locale).unwrap_or(&item.name).to_lowercase();
            name(a).cmp(&name(b)).then_with(||a.kind.as_str().cmp(b.kind.as_str())).then_with(||a.token.cmp(&b.token))
        });}
        let total=items.len() as u32;Ok(Page{total,items:items.into_iter().skip(query.offset as usize).take(query.limit as usize).collect()})
    })
}
pub fn search(
    database: &Database,
    query: &SearchQuery,
    now: i64,
) -> Result<Page<SearchHit>, AppError> {
    let q = query.q.trim().to_lowercase();
    if q.chars().count() > 64 || query.limit > 200 || q.chars().any(char::is_control) {
        return Err(AppError::new("E_INVALID_ARG", "search_query"));
    }
    if q.is_empty() {
        return Ok(Page {
            total: 0,
            items: Vec::new(),
        });
    }
    database.with(|connection|{
        let children=descendants(connection,query.category.as_deref())?;let long=q.chars().count()>=3;
        let predicate=if long{"catalog_fts MATCH ?1"}else{"(catalog_fts.token LIKE ?1 ESCAPE '\\' OR names LIKE ?1 ESCAPE '\\' OR display_name LIKE ?1 ESCAPE '\\' OR summary LIKE ?1 ESCAPE '\\' OR pinyin LIKE ?1 ESCAPE '\\')"};
        let text=if long{format!("\"{}\"",q.replace('"',"\"\""))}else{format!("%{}%",q.replace('\\',"\\\\").replace('%',"\\%").replace('_',"\\_"))};
        let sql=format!("SELECT p.data,{} FROM catalog_fts JOIN catalog_items p ON p.kind=catalog_fts.kind AND p.token=catalog_fts.token WHERE {predicate} AND p.hidden=0 AND (?2 IS NULL OR p.kind=?2) AND (?3 OR p.disabled=0)",if long{"bm25(catalog_fts)"}else{"0.0"});
        let mut statement=connection.prepare(&sql)?;let mut hits=Vec::new();
        for row in statement.query_map(params![text,query.kind.map(Kind::as_str),query.include_disabled],|r|Ok((r.get::<_,String>(0)?,r.get::<_,f64>(1)?)))?{
            let (data,bm25)=row?;let item:CatalogItem=serde_json::from_str(&data)?;
            if children.as_ref().is_some_and(|children|!item.categories.iter().any(|c|children.contains(c))){continue;}
            let names=item.names.iter().chain(item.binaries.iter()).chain(std::iter::once(&item.name)).chain(item.display_name.0.values());
            let exact=item.token==q||names.clone().any(|name|name.to_lowercase()==q);let matched_name=names.filter(|n|n.to_lowercase().contains(&q)).min_by_key(|n|n.len()).cloned();
            let score=if exact{1_000_000.0}else{0.0}+(1.0-bm25)*(2.0+item.popularity.max(0.0)).ln()*if item.is_font{0.5}else if item.is_library{0.7}else{1.0};
            hits.push(SearchHit{item,score,matched_name});
        }
        hits.sort_by(|a,b|b.score.total_cmp(&a.score).then_with(||a.item.token.cmp(&b.item.token)));let total=hits.len() as u32;
        connection.execute("INSERT INTO search_history(q,last_used_at,count) VALUES(?1,?2,1) ON CONFLICT(q) DO UPDATE SET last_used_at=excluded.last_used_at,count=count+1",params![query.q.trim(),now])?;
        connection.execute("DELETE FROM search_history WHERE q NOT IN(SELECT q FROM search_history ORDER BY last_used_at DESC,q LIMIT 20)",[])?;
        Ok(Page{total,items:hits.into_iter().skip(query.offset as usize).take(query.limit as usize).collect()})
    })
}
