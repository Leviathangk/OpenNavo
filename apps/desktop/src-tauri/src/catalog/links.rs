use crate::{
    AppError,
    db::Database,
    model::{CatalogItem, Kind, LocalizedText, TaskKind, TaskTarget},
};
use rusqlite::{OptionalExtension, params};
use serde::{Deserialize, Serialize};
use std::{collections::HashSet, future::Future};

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LinkInfo {
    pub homepage: Option<String>,
    pub download_url: Option<String>,
    pub github: Option<LinkGithub>,
    pub team_id: Option<String>,
    pub pricing: LinkPricing,
    pub notes: Option<LocalizedText>,
    pub adopted_by_cask: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct LinkGithub {
    pub repo: String,
    pub latest: Option<GithubRelease>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum LinkPricing {
    Free,
    Paid,
    Freemium,
    Subscription,
    OpenSource,
    Unknown,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct GithubRelease {
    pub tag: String,
    pub version: String,
    pub published_at: String,
    pub url: String,
    pub assets: Vec<ReleaseAsset>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
pub struct ReleaseAsset {
    pub name: String,
    pub url: String,
    pub size: u64,
    pub arch: Option<ReleaseArch>,
    pub format: ReleaseFormat,
    pub sha256: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum ReleaseArch {
    Arm64,
    X86_64,
    Universal,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum ReleaseFormat {
    Dmg,
    Zip,
    Pkg,
    Other,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "snake_case")]
pub enum LinkDestination {
    Download,
    Homepage,
    Github,
}
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(untagged, deny_unknown_fields)]
pub enum LinkOpenTarget {
    Named(LinkDestination),
    Asset { asset: String },
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LinkCatalog {
    pub etag: String,
    pub items: Vec<CatalogItem>,
}

fn invalid() -> AppError {
    AppError::new("E_NETWORK", "link_catalog_invalid")
}
pub fn https(value: &str) -> bool {
    let Ok(url) = reqwest::Url::parse(value) else {
        return false;
    };
    value.len() <= 4096
        && url.scheme() == "https"
        && url.host_str().is_some()
        && url.username().is_empty()
        && url.password().is_none()
        && url.fragment().is_none()
        && !value.chars().any(char::is_control)
}
fn valid_repo(repo: &str) -> bool {
    let parts: Vec<_> = repo.split('/').collect();
    parts.len() == 2
        && !repo.contains("..")
        && !repo.ends_with(".git")
        && parts.iter().all(|s| {
            !s.is_empty()
                && s.len() <= 100
                && s.as_bytes()[0].is_ascii_alphanumeric()
                && s.bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
        })
}
fn validate(item: &CatalogItem) -> Result<(), AppError> {
    if item.kind != Kind::Link || item.tap.is_some() {
        return Err(invalid());
    }
    crate::brew::args::validate_token(&item.token)?;
    let link = item.link.as_ref().ok_or_else(invalid)?;
    if link.homepage.is_none() && link.download_url.is_none() && link.github.is_none() {
        return Err(invalid());
    }
    for url in [&link.homepage, &link.download_url].into_iter().flatten() {
        if !https(url) {
            return Err(invalid());
        }
    }
    if link.team_id.as_ref().is_some_and(|s| {
        s.len() != 10
            || !s
                .bytes()
                .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
    }) {
        return Err(invalid());
    }
    if let Some(token) = &link.adopted_by_cask {
        crate::brew::args::validate_token(token)?;
    }
    if item.bundle_ids.as_ref().is_some_and(|bundles| {
        bundles.len() > 32
            || bundles.iter().any(|b| {
                b.len() > 255
                    || !b.contains('.')
                    || b.split('.').any(|p| {
                        p.is_empty()
                            || !p
                                .bytes()
                                .all(|c| c.is_ascii_alphanumeric() || b"_-".contains(&c))
                    })
            })
    }) {
        return Err(invalid());
    }
    if let Some(github) = &link.github {
        if !valid_repo(&github.repo) {
            return Err(invalid());
        }
        if let Some(release) = &github.latest {
            if release.version.is_empty()
                || release.version.len() > 256
                || release.tag.len() > 256
                || !https(&release.url)
                || chrono::DateTime::parse_from_rfc3339(&release.published_at).is_err()
                || release.assets.len() > 500
            {
                return Err(invalid());
            }
            let mut names = HashSet::new();
            for asset in &release.assets {
                if asset.name.is_empty()
                    || asset.name.len() > 512
                    || !names.insert(&asset.name)
                    || !https(&asset.url)
                    || asset.size > 9_007_199_254_740_991
                    || asset
                        .sha256
                        .as_ref()
                        .is_some_and(|s| s.len() != 64 || !s.bytes().all(|b| b.is_ascii_hexdigit()))
                {
                    return Err(invalid());
                }
            }
        }
    }
    Ok(())
}

/// Full replacement is atomic and restricted to links. Snapshot/changes cannot
/// introduce links, and a stale links response cannot shadow a newly adopted cask.
pub fn apply(database: &Database, catalog: &LinkCatalog, now: i64) -> Result<u32, AppError> {
    if catalog.etag.len() < 2
        || catalog.etag.len() > 256
        || !catalog.etag.is_ascii()
        || catalog.etag.chars().any(char::is_control)
        || catalog.items.len() > 20_000
    {
        return Err(invalid());
    }
    let mut tokens = HashSet::new();
    for item in &catalog.items {
        validate(item)?;
        if !tokens.insert(&item.token) {
            return Err(invalid());
        }
    }
    database.with(|connection| {
        let tx = connection.transaction()?;
        let old: Option<String> = tx
            .query_row("SELECT value FROM meta WHERE key='links_etag'", [], |r| {
                r.get(0)
            })
            .optional()?;
        if old.as_ref() == Some(&catalog.etag) {
            return Ok(0);
        }
        tx.execute("DELETE FROM catalog_fts WHERE kind='link'", [])?;
        let removed = tx.execute("DELETE FROM catalog_items WHERE kind='link'", [])?;
        let mut count = 0;
        for item in &catalog.items {
            let cask: bool = tx.query_row(
                "SELECT EXISTS(SELECT 1 FROM catalog_items WHERE kind='cask' AND token=?1)",
                [&item.token],
                |r| r.get(0),
            )?;
            if !cask
                && item
                    .link
                    .as_ref()
                    .is_some_and(|l| l.adopted_by_cask.is_none())
            {
                super::upsert(&tx, item)?;
                count += 1;
            }
        }
        super::set_meta(&tx, "links_etag", &catalog.etag)?;
        super::set_meta(&tx, "links_synced_at", &now.to_string())?;
        tx.execute("DELETE FROM meta WHERE key='links_last_error'", [])?;
        tx.commit()?;
        Ok(count.max(removed as u32))
    })
}

pub fn reject_write(database: &Database, target: &TaskTarget) -> Result<(), AppError> {
    if target.kind == TaskKind::Link {
        return Err(AppError::new("E_NOT_INSTALLABLE", "link_target"));
    }
    // A caller cannot bypass the guard by relabelling a link as a cask. Formula
    // tokens occupy a separate namespace and local-app plans use an app identity.
    if target.kind == TaskKind::Cask
        && super::get(database, Kind::Cask, &target.token)?.is_none()
        && super::get(database, Kind::Link, &target.token)?.is_some()
    {
        return Err(AppError::new("E_NOT_INSTALLABLE", "link_target"));
    }
    Ok(())
}
pub fn destination(
    database: &Database,
    token: &str,
    target: &LinkOpenTarget,
) -> Result<String, AppError> {
    let item = super::get(database, Kind::Link, token)?
        .ok_or_else(|| AppError::new("E_NOT_FOUND", "link_app"))?;
    validate(&item)?;
    let link = item.link.as_ref().ok_or_else(invalid)?;
    let url = match target {
        LinkOpenTarget::Named(LinkDestination::Homepage) => link.homepage.clone(),
        LinkOpenTarget::Named(LinkDestination::Download) => link.download_url.clone(),
        LinkOpenTarget::Named(LinkDestination::Github) => link.github.as_ref().map(|g| {
            g.latest
                .as_ref()
                .map(|release| release.url.clone())
                .unwrap_or_else(|| format!("https://github.com/{}/releases/latest", g.repo))
        }),
        LinkOpenTarget::Asset { asset } => link
            .github
            .as_ref()
            .and_then(|g| g.latest.as_ref())
            .and_then(|r| {
                r.assets
                    .iter()
                    .find(|a| &a.name == asset || &a.url == asset)
            })
            .map(|a| a.url.clone()),
    }
    .ok_or_else(|| AppError::new("E_INVALID_ARG", "link_destination"))?;
    if !https(&url) {
        return Err(invalid());
    }
    Ok(url)
}
pub async fn open<F, Fut>(
    database: &Database,
    token: String,
    target: LinkOpenTarget,
    opener: F,
) -> Result<(), AppError>
where
    F: FnOnce(String) -> Fut,
    Fut: Future<Output = Result<(), AppError>>,
{
    let url = destination(database, &token, &target)?;
    opener(url.clone()).await?;
    let target = serde_json::to_string(&target)?;
    database.with(|connection|{
        connection.execute("INSERT INTO link_activity(token,target,url,opened_at) VALUES(?1,?2,?3,?4)",params![token,target,url,chrono::Utc::now().timestamp_millis()])?;
        connection.execute("DELETE FROM link_activity WHERE id NOT IN (SELECT id FROM link_activity ORDER BY opened_at DESC,id DESC LIMIT 1000)",[])?;
        Ok(())
    })
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    pub(crate) fn item() -> CatalogItem {
        let fixture = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../../apps/server/testdata/desktop/v2/catalog.json.gz");
        let file = std::fs::File::open(fixture).unwrap();
        let mut json: serde_json::Value =
            serde_json::from_reader(flate2::read::GzDecoder::new(file)).unwrap();
        let mut item = json["items"][0].take();
        item["kind"] = "link".into();
        item["token"] = "external-example".into();
        item["displayName"] = serde_json::json!({"en-US":"External Example","ja-JP":"外部アプリ"});
        item["summary"] = serde_json::json!({"en-US":"External software"});
        item["bundleIds"] = serde_json::json!(["com.example.App"]);
        item["link"] = serde_json::json!({"homepage":"https://example.com","downloadUrl":"https://example.com/download","github":{"repo":"owner/app","latest":{"tag":"v2","version":"2","publishedAt":"2026-10-16T00:00:00Z","url":"https://github.com/owner/app/releases/tag/v2","assets":[{"name":"App.dmg","url":"https://example.com/App.dmg","size":12,"arch":"arm64","format":"dmg","sha256":null}]}},"teamId":null,"pricing":"free","notes":null,"adoptedByCask":null});
        serde_json::from_value(item).unwrap()
    }
    #[test]
    fn github_destination_prefers_release_then_latest_and_rejects_unsafe_urls() {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let mut entry = item();
        let target = LinkOpenTarget::Named(LinkDestination::Github);
        db.with(|c| super::super::upsert(c, &entry)).unwrap();
        assert_eq!(
            destination(&db, &entry.token, &target).unwrap(),
            "https://github.com/owner/app/releases/tag/v2"
        );
        entry.link.as_mut().unwrap().github.as_mut().unwrap().latest = None;
        db.with(|c| super::super::upsert(c, &entry)).unwrap();
        assert_eq!(
            destination(&db, &entry.token, &target).unwrap(),
            "https://github.com/owner/app/releases/latest"
        );
        entry = item();
        entry
            .link
            .as_mut()
            .unwrap()
            .github
            .as_mut()
            .unwrap()
            .latest
            .as_mut()
            .unwrap()
            .url = "http://github.com/owner/app/releases/tag/v2".into();
        db.with(|c| super::super::upsert(c, &entry)).unwrap();
        assert!(destination(&db, &entry.token, &target).is_err());
    }

    #[test]
    fn app_queries_mix_links_with_casks_before_ranking_and_paging() {
        use crate::model::{ListQuery, ListSort, Locale, SearchQuery};
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let category = serde_json::json!({"slug":"apps","parent":null,"name":{"en-US":"Apps"},"icon":"box","sort":0,"hiddenByDefault":false,"appliesTo":"cask"});
        db.with(|c| {
            c.execute(
                "INSERT INTO categories(slug,data,sort) VALUES('apps',?1,0)",
                [category.to_string()],
            )?;
            for (kind, token, name, popularity) in [
                (Kind::Cask, "needle-cask", "Zebra needle", 1.0),
                (Kind::Link, "needle-link", "Alpha needle", 20.0),
                (Kind::Formula, "needle-cli", "Command needle", 100.0),
            ] {
                let mut entry = item();
                entry.kind = kind;
                entry.token = token.into();
                entry.name = name.into();
                entry.names = vec![name.into()];
                entry.display_name = LocalizedText::default();
                entry.summary = LocalizedText::default();
                entry.popularity = popularity;
                entry.categories = if kind == Kind::Formula {
                    vec![]
                } else {
                    vec!["apps".into()]
                };
                super::super::upsert(c, &entry)?;
            }
            Ok(())
        })
        .unwrap();
        let old = serde_json::json!({"kind":"cask","category":null,"sort":"popular","includeFonts":true,"includeLibraries":true,"includeDisabled":false,"limit":1,"offset":0});
        let mut list: ListQuery = serde_json::from_value(old).unwrap();
        assert!(!list.include_links);
        assert_eq!(super::super::list(&db, &list).unwrap().total, 1);
        list.include_links = true;
        for sort in [ListSort::Popular, ListSort::Name] {
            list.sort = sort;
            list.offset = 0;
            let page = super::super::list(&db, &list).unwrap();
            assert_eq!(page.total, 2);
            assert_eq!(page.items[0].kind, Kind::Link);
            list.offset = 1;
            assert_eq!(
                super::super::list(&db, &list).unwrap().items[0].kind,
                Kind::Cask
            );
        }
        let mut search: SearchQuery = serde_json::from_value(serde_json::json!({"q":"needle","kind":"cask","category":null,"includeDisabled":false,"limit":1,"offset":0})).unwrap();
        assert!(!search.include_links);
        assert_eq!(super::super::search(&db, &search, 1).unwrap().total, 1);
        search.include_links = true;
        let page = super::super::search(&db, &search, 1).unwrap();
        assert_eq!(page.total, 2);
        assert_eq!(page.items[0].item.kind, Kind::Link);
        search.offset = 1;
        assert_eq!(
            super::super::search(&db, &search, 1).unwrap().items[0]
                .item
                .kind,
            Kind::Cask
        );
        search.kind = Some(Kind::Formula);
        search.offset = 0;
        assert_eq!(super::super::search(&db, &search, 1).unwrap().total, 1);
        assert_eq!(
            super::super::categories(&db, Locale::EnUs).unwrap()[0].package_count,
            2
        );
    }
    #[test]
    fn separate_full_sync_is_atomic_and_cannot_enter_legacy_protocols() {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let link = item();
        let catalog = LinkCatalog {
            etag: "\"one\"".into(),
            items: vec![link.clone()],
        };
        assert_eq!(apply(&db, &catalog, 1).unwrap(), 1);
        assert_eq!(apply(&db, &catalog, 2).unwrap(), 0);
        let mut invalid = catalog.clone();
        invalid.etag = "\"bad\"".into();
        invalid.items[0].link.as_mut().unwrap().download_url = Some("file:///etc/passwd".into());
        assert!(apply(&db, &invalid, 3).is_err());
        assert_eq!(db.meta("links_etag").unwrap().as_deref(), Some("\"one\""));
        assert_eq!(
            super::super::get(&db, Kind::Link, &link.token).unwrap(),
            Some(link.clone())
        );
        let changes = super::super::Changes {
            changes: vec![super::super::Change {
                cursor: 1,
                op: "upsert".into(),
                kind: Kind::Link,
                token: link.token.clone(),
                item: Some(link.clone()),
            }],
            next_cursor: 1,
            has_more: false,
            categories: None,
            category_names: None,
        };
        assert!(super::super::apply_changes(&db, &changes, 3).is_err());
        assert!(db.meta("catalog_cursor").unwrap().is_none());
        // Rebuilds performed by a cask snapshot retain link text and FTS rows.
        db.with(|connection| super::super::rebuild_texts(connection))
            .unwrap();
        let count: i64 = db
            .with(|c| {
                Ok(c.query_row(
                    "SELECT count(*) FROM catalog_fts WHERE catalog_fts MATCH 'External'",
                    [],
                    |r| r.get(0),
                )?)
            })
            .unwrap();
        assert_eq!(count, 1);
        let mut cask = link.clone();
        cask.kind = Kind::Cask;
        cask.link = None;
        cask.bundle_ids = None;
        db.with(|c| super::super::upsert(c, &cask)).unwrap();
        assert!(
            super::super::get(&db, Kind::Link, &link.token)
                .unwrap()
                .is_none()
        );
        assert_eq!(apply(&db, &catalog, 4).unwrap(), 0);
        assert!(
            super::super::get(&db, Kind::Link, &link.token)
                .unwrap()
                .is_none()
        );
        assert!(
            super::super::get(&db, Kind::Cask, &link.token)
                .unwrap()
                .is_some()
        );
    }
    #[tokio::test]
    async fn only_registered_https_destinations_open_and_create_activity() {
        let temp = tempfile::tempdir().unwrap();
        let db = Database::open(&temp.path().join("catalog.db")).unwrap();
        let item = item();
        apply(
            &db,
            &LinkCatalog {
                etag: "\"one\"".into(),
                items: vec![item.clone()],
            },
            1,
        )
        .unwrap();
        let target = LinkOpenTarget::Asset {
            asset: "App.dmg".into(),
        };
        open(&db, item.token.clone(), target, |url| async move {
            assert_eq!(url, "https://example.com/App.dmg");
            Ok(())
        })
        .await
        .unwrap();
        let bad = LinkOpenTarget::Asset {
            asset: "https://malicious.example/other.dmg".into(),
        };
        assert!(
            open(&db, item.token.clone(), bad, |_| async {
                panic!("unregistered destination opened")
            })
            .await
            .is_err()
        );
        assert!(
            open(
                &db,
                item.token,
                LinkOpenTarget::Named(LinkDestination::Github),
                |_| async { Err(AppError::new("E_IO", "fake_open_failure")) }
            )
            .await
            .is_err()
        );
        let count: i64 = db
            .with(|c| Ok(c.query_row("SELECT count(*) FROM link_activity", [], |r| r.get(0))?))
            .unwrap();
        assert_eq!(count, 1);
        assert!(db.tasks().unwrap().is_empty());
    }
    #[test]
    fn deep_link_install_is_navigation_only_and_brew_args_always_reject() {
        let event =
            crate::deeplink::parse("opennavo://package/link/external-example?action=install")
                .unwrap();
        assert_eq!(event.route, "/package/link/external-example");
        assert!(event.action.is_none());
        for op in [
            crate::model::TaskOp::Install,
            crate::model::TaskOp::Upgrade,
            crate::model::TaskOp::Reinstall,
            crate::model::TaskOp::Uninstall,
        ] {
            assert_eq!(
                crate::brew::args::write_args(
                    op,
                    Some(&TaskTarget {
                        kind: TaskKind::Link,
                        token: "external-example".into()
                    }),
                    &Default::default()
                )
                .unwrap_err()
                .code,
                "E_NOT_INSTALLABLE"
            );
        }
    }
}
