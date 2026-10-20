use super::{
    electron, feed, prefs,
    release::{Artifact, Release},
    routing,
    service::{Candidate, FeedRule, Service, requires_admin, system},
    version,
};
use crate::{AppError, model::*, uninstall::rules::Snapshot};
use std::path::Path;
struct Environment {
    os: String,
    arch: String,
    manual: bool,
}
impl Service {
    async fn sparkle_candidate(
        &self,
        app: &LocalApp,
        rule: Option<&FeedRule>,
        env: &Environment,
    ) -> Result<Option<Candidate>, AppError> {
        let path = Path::new(&app.path);
        let info = plist::Value::from_file(path.join("Contents/Info.plist"))
            .map_err(|_| AppError::new("E_UPDATE_FEED", "installed_info"))?;
        let dict = info
            .as_dictionary()
            .ok_or_else(|| AppError::new("E_UPDATE_FEED", "installed_info"))?;
        let string = |key: &str| dict.get(key).and_then(|v| v.as_string()).map(str::to_owned);
        let url = string("SUFeedURL")
            .or_else(|| rule.and_then(|r| r.feed_url.clone()))
            .ok_or_else(|| AppError::new("E_UPDATE_FEED", "sparkle_url"))?;
        let url = feed::https(&url)?;
        let key = format!("sparkle_feed:{}", app.id);
        let bytes = self.read_url(&url, &key, feed::MAX_FEED).await?;
        let items = feed::parse(&bytes)?;
        let pref = prefs::load(&self.database, &format!("app:{}", app.id))?;
        let build = string("CFBundleVersion")
            .ok_or_else(|| AppError::new("E_UPDATE_FEED", "installed_build_version"))?;
        let selection = feed::Selection {
            installed: &build,
            os: &env.os,
            arch: &env.arch,
            channel: pref.channel,
            skipped: pref.skipped_version.as_deref(),
            manual: env.manual,
            now: chrono::Utc::now().timestamp(),
            rollout_group: super::service::rollout_group(&app.id),
        };
        let Some(selected) = feed::select(&items, &selection) else {
            return Ok(None);
        };
        let (kind, token) = self.catalog_identity(app, rule)?;
        let informational = selected.informational_for(&build);
        let item = OutdatedItem {
            source: UpdateSource::Sparkle,
            app_id: Some(app.id.clone()),
            bundle_id: app.bundle_id.clone(),
            channel: Some(pref.channel),
            major: version::major(
                app.version.as_deref().unwrap_or(""),
                &selected.short_version,
            ),
            notes: selected.notes.clone(),
            integrity: if informational {
                None
            } else {
                Some(UpdateIntegrity::Eddsa)
            },
            requires_admin: selected.package || requires_admin(path),
            running: app.running,
            relaunch_pending: false,
            skipped_version: pref.skipped_version,
            critical_update: selected.critical_for(&build),
            informational_url: if informational {
                selected.link.clone()
            } else {
                None
            },
            download_host: selected
                .url
                .as_deref()
                .and_then(|u| reqwest::Url::parse(u).ok())
                .and_then(|u| u.host_str().map(str::to_owned))
                .filter(|_| !informational),
            expected_team_match: rule
                .and_then(|r| r.team_id.as_ref())
                .map(|team| app.team_id.as_ref() == Some(team)),
            kind,
            token,
            name: app.name.clone(),
            installed_version: app.version.clone().unwrap_or(build),
            current_version: selected.short_version.clone(),
            auto_updates: true,
            pinned: false,
            ignored: pref.ignored,
            dependents: 0,
            download_size: selected.length,
        };
        Ok::<_, AppError>(Some(Candidate {
            item,
            app_path: path.into(),
            snapshot: Snapshot::capture(path)?,
            feed_item: Some(selected.clone()),
            public_key: string("SUPublicEDKey"),
            artifact: None,
            expected_team: rule.and_then(|r| r.team_id.clone()),
            checked_at: chrono::Utc::now().timestamp_millis(),
        }))
    }
}

impl Service {
    pub(super) async fn check_native(
        &self,
        sources: &[UpdateSource],
        apps: &[LocalApp],
        candidates: &mut Vec<Candidate>,
        manual: bool,
    ) -> Result<super::outcome::CheckOutcome, AppError> {
        let native = |s| {
            matches!(
                s,
                UpdateSource::Sparkle
                    | UpdateSource::Electron
                    | UpdateSource::Github
                    | UpdateSource::Vendor
            )
        };
        if !sources.iter().any(|s| native(*s)) {
            return Ok(super::outcome::CheckOutcome::default());
        }
        self.check_native_with_rules(sources, apps, candidates, manual, self.rules().await)
            .await
    }
    pub(super) async fn check_native_with_rules(
        &self,
        sources: &[UpdateSource],
        apps: &[LocalApp],
        candidates: &mut Vec<Candidate>,
        manual: bool,
        rules: Result<Vec<FeedRule>, AppError>,
    ) -> Result<super::outcome::CheckOutcome, AppError> {
        let native = |s| {
            matches!(
                s,
                UpdateSource::Sparkle
                    | UpdateSource::Electron
                    | UpdateSource::Github
                    | UpdateSource::Vendor
            )
        };
        let mut outcome = super::outcome::CheckOutcome::default();
        let fresh_rules = rules.is_ok();
        let rules = match rules {
            Ok(rules) => rules,
            Err(error) => {
                outcome.rules_failed(error);
                super::service::cached_rules(&self.database)
            }
        };
        let (os, arch) = system()?;
        let env = Environment { os, arch, manual };
        let settings = self.settings()?;
        let running = self.apps.control.list()?;
        for app in apps {
            // An already installed/downloaded update awaits restart independently of
            // future routing. Do not replace it with a second source's installation.
            if let Some(pending) = candidates
                .iter()
                .find(|c| {
                    c.item.app_id.as_deref() == Some(&app.id)
                        && c.item.relaunch_pending
                        && c.app_path == Path::new(&app.path)
                        && c.app_path.is_dir()
                        && running.iter().any(|p| p.path.starts_with(&c.app_path))
                })
                .cloned()
            {
                candidates.retain(|c| c.item.app_id.as_deref() != Some(&app.id));
                candidates.push(pending);
                continue;
            }
            let selected = routing::select(&self.database, &settings, app, &rules)?;
            candidates.retain(|c| {
                c.item.app_id.as_deref() != Some(&app.id)
                    || !native(c.item.source)
                    || (!fresh_rules && selected.is_none())
                    || Some(c.item.source) == selected
            });
            let Some(source) = selected.filter(|s| native(*s) && sources.contains(s)) else {
                continue;
            };
            let pref = prefs::load(&self.database, &format!("app:{}", app.id))?;
            let rule = rules.iter().find(|r| {
                r.source == source
                    && Some(&r.bundle_id) == app.bundle_id.as_ref()
                    && r.channel.unwrap_or_default() == pref.channel
            });
            let result = if source == UpdateSource::Sparkle {
                self.sparkle_candidate(app, rule, &env).await
            } else {
                self.release_candidate(app, source, rule, &env).await
            };
            match result {
                Ok(new) => {
                    outcome.succeeded();
                    // Keep a verified pending download while the same application is running.
                    let old = candidates
                        .iter()
                        .find(|c| {
                            c.item.app_id.as_deref() == Some(&app.id) && c.item.relaunch_pending
                        })
                        .cloned();
                    candidates.retain(|c| c.item.app_id.as_deref() != Some(&app.id));
                    if let Some(candidate) = new {
                        candidates.push(candidate)
                    } else if let Some(mut candidate) = old.filter(|c| {
                        app.running
                            && c.item.source == UpdateSource::Electron
                            && c.artifact.as_ref().is_some_and(|a| {
                                disk_version(Path::new(&app.path), &a.version_key).is_some_and(
                                    |v| {
                                        version::compare_source(c.item.source, &v, &a.version)
                                            .is_ok_and(|o| o.is_eq())
                                    },
                                )
                            })
                    }) {
                        candidate.item.running = true;
                        candidates.push(candidate);
                    }
                }
                Err(e) => outcome.failed_app(source, app, e),
            }
        }
        Ok(outcome)
    }
    async fn release_candidate(
        &self,
        app: &LocalApp,
        source: UpdateSource,
        rule: Option<&FeedRule>,
        env: &Environment,
    ) -> Result<Option<Candidate>, AppError> {
        let path = Path::new(&app.path);
        let pref = prefs::load(&self.database, &format!("app:{}", app.id))?;
        let config = if source == UpdateSource::Electron {
            electron::Config::local(path).ok()
        } else {
            None
        };
        let release: Release = if source == UpdateSource::Electron {
            let url = match &config {
                Some(c) => c.manifest(pref.channel)?,
                None => rule
                    .and_then(|r| r.feed_url.clone())
                    .ok_or_else(|| AppError::new("E_UPDATE_FEED", "electron_rule_missing"))?,
            };
            let bytes = self
                .read_url(
                    &feed::https(&url)?,
                    &format!("electron_feed:{}", app.id),
                    electron::MAX_MANIFEST,
                )
                .await?;
            electron::parse(&bytes, &url)?
        } else {
            rule.and_then(|r| r.release.clone())
                .ok_or_else(|| AppError::new("E_UPDATE_FEED", "resolved_release_missing"))?
        };
        release.validate(source)?;
        let current = disk_version(path, &release.version_key)
            .ok_or_else(|| AppError::new("E_UPDATE_FEED", "installed_version_missing"))?;
        if !version::compare_source(source, &release.version, &current)?.is_gt()
            || pref.skipped_version.as_deref() == Some(&release.version)
        {
            return Ok(None);
        }
        let Some(file) = release.select(&env.arch, &env.os).cloned() else {
            return Ok(None);
        };
        let artifact = Artifact {
            file,
            version: release.version.clone(),
            version_key: release.version_key,
        };
        let (kind, token) = self.catalog_identity(app, rule)?;
        let expected_team = rule.and_then(|r| r.team_id.clone());
        if matches!(source, UpdateSource::Github | UpdateSource::Vendor) && expected_team.is_none()
        {
            return Err(AppError::new(
                "E_UPDATE_TEAM_MISMATCH",
                "rule_team_required",
            ));
        }
        let pending = if app.running && source == UpdateSource::Electron {
            let artifact = artifact.clone();
            crate::blocking(move || {
                Ok(config
                    .as_ref()
                    .is_some_and(|c| electron::pending(c, &artifact).unwrap_or(false)))
            })
            .await?
        } else {
            false
        };
        let item = OutdatedItem {
            kind,
            token,
            name: app.name.clone(),
            installed_version: app.version.clone().unwrap_or(current),
            current_version: release.version,
            auto_updates: source == UpdateSource::Electron,
            pinned: false,
            ignored: pref.ignored,
            dependents: 0,
            download_size: artifact.file.size,
            source,
            app_id: Some(app.id.clone()),
            bundle_id: app.bundle_id.clone(),
            channel: Some(pref.channel),
            major: version::major(app.version.as_deref().unwrap_or(""), &artifact.version),
            notes: release.notes,
            integrity: Some(artifact.integrity(source)),
            requires_admin: artifact.file.name.to_ascii_lowercase().ends_with(".pkg")
                || requires_admin(path),
            running: app.running,
            relaunch_pending: pending,
            skipped_version: pref.skipped_version,
            critical_update: false,
            informational_url: None,
            download_host: reqwest::Url::parse(&artifact.file.url)
                .ok()
                .and_then(|u| u.host_str().map(str::to_owned)),
            expected_team_match: expected_team
                .as_ref()
                .map(|t| app.team_id.as_ref() == Some(t)),
        };
        Ok(Some(Candidate {
            item,
            app_path: path.into(),
            snapshot: Snapshot::capture(path)?,
            feed_item: None,
            public_key: None,
            artifact: Some(artifact),
            expected_team,
            checked_at: chrono::Utc::now().timestamp_millis(),
        }))
    }
}
pub(super) fn disk_version(path: &Path, key: &str) -> Option<String> {
    plist::Value::from_file(path.join("Contents/Info.plist"))
        .ok()?
        .as_dictionary()?
        .get(key)?
        .as_string()
        .map(str::to_owned)
}

#[cfg(test)]
mod partial_tests {
    use super::*;
    use crate::{db::Database, queue::TaskQueue};
    use std::{
        fs,
        sync::{Arc, RwLock},
    };

    #[tokio::test]
    async fn workbuddy_vendor_without_catalog_token_still_produces_an_update() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path().canonicalize().unwrap();
        let database = Database::open(&root.join("db")).unwrap();
        let queue = TaskQueue::new(
            database.clone(),
            Arc::new(RwLock::new(Settings::default())),
            root.join("logs"),
            root.join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        let path = root.join("WorkBuddy.app");
        fs::create_dir_all(path.join("Contents")).unwrap();
        let mut info = plist::Dictionary::new();
        info.insert(
            "CFBundleIdentifier".into(),
            "com.tencent.workbuddy.mac".into(),
        );
        info.insert("CFBundleShortVersionString".into(), "5.6.2".into());
        plist::Value::Dictionary(info)
            .to_file_xml(path.join("Contents/Info.plist"))
            .unwrap();
        let app: LocalApp = serde_json::from_value(serde_json::json!({
            "id":"eba15506738312750657b7c56bceb0404900d0e3", "name":"WorkBuddy",
            "path":path, "bundleId":"com.tencent.workbuddy.mac", "version":"5.6.2",
            "teamId":"FN2V63AD2J", "source":"direct", "running":false, "removable":true
        }))
        .unwrap();
        // Exact public response cached by the isolated acceptance app. Unlike
        // older mock rules, a missing package association is token:"", not null.
        let rule: FeedRule = serde_json::from_str(include_str!(
            "../../tests/fixtures/updater/workbuddy-vendor-rule.json"
        ))
        .unwrap();
        assert_eq!(rule.token.as_deref(), Some(""));
        let mut candidates = vec![];
        let outcome = queue
            .updater
            .check_native_with_rules(
                &[UpdateSource::Vendor],
                std::slice::from_ref(&app),
                &mut candidates,
                true,
                Ok(vec![rule.clone()]),
            )
            .await
            .unwrap();
        outcome.result().unwrap();
        assert!(outcome.failed_sources().is_empty());
        assert_eq!(candidates.len(), 1);
        let candidate = &candidates[0];
        assert_eq!(candidate.item.source, UpdateSource::Vendor);
        assert_eq!(candidate.item.kind, Kind::Cask);
        assert_eq!(candidate.item.token, "");
        assert_eq!(candidate.item.current_version, "5.8.2");
        assert_eq!(candidate.item.app_id.as_deref(), Some(app.id.as_str()));
        assert_eq!(candidate.item.expected_team_match, Some(true));
        assert_eq!(
            candidate.item.download_host.as_deref(),
            Some("download.codebuddy.cn")
        );
        assert_eq!(
            candidate.item.task_target().unwrap(),
            TaskTarget {
                kind: TaskKind::App,
                token: app.id.clone(),
            }
        );
        assert!(database.tasks().unwrap().is_empty());

        // A directory hint stays optional; its absence must not weaken the
        // validator for nonempty, malformed tokens or turn them into brew args.
        for token in ["owner/tap/workbuddy", "WorkBuddy", " "] {
            let mut bad = rule.clone();
            bad.token = Some(token.into());
            let error = queue
                .updater
                .catalog_identity(&app, Some(&bad))
                .unwrap_err();
            assert_eq!(error.code, "E_INVALID_ARG");
            assert_eq!(error.message, "token");
        }

        // An empty remote hint must still allow a local bundle-ID match.
        let mut item = crate::catalog::links::tests::item();
        item.token = "workbuddy".into();
        item.bundle_ids = Some(vec![rule.bundle_id.clone()]);
        crate::catalog::links::apply(
            &database,
            &crate::catalog::links::LinkCatalog {
                etag: "fixture".into(),
                items: vec![item],
            },
            None,
            1,
        )
        .unwrap();
        assert_eq!(
            queue.updater.catalog_identity(&app, Some(&rule)).unwrap(),
            (Kind::Link, "workbuddy".into())
        );
        let mut explicit = rule.clone();
        explicit.token = Some("workbuddy".into());
        let mut no_bundle = app.clone();
        no_bundle.bundle_id = None;
        no_bundle.catalog_token = Some(String::new());
        assert_eq!(
            queue
                .updater
                .catalog_identity(&no_bundle, Some(&explicit))
                .unwrap(),
            (Kind::Link, "workbuddy".into())
        );
    }

    #[tokio::test]
    async fn mixed_native_apps_keep_results_and_report_source_even_when_a_peer_succeeds() {
        let tmp = tempfile::tempdir().unwrap();
        let root = tmp.path().canonicalize().unwrap();
        let database = Database::open(&root.join("db")).unwrap();
        let queue = TaskQueue::new(
            database.clone(),
            Arc::new(RwLock::new(Settings::default())),
            root.join("logs"),
            root.join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        let mut apps = vec![];
        let mut rules = vec![];
        for name in ["working", "broken", "current"] {
            let path = root.join(format!("{name}.app"));
            fs::create_dir_all(path.join("Contents")).unwrap();
            let mut info = plist::Dictionary::new();
            info.insert("CFBundleShortVersionString".into(), "1.0".into());
            plist::Value::Dictionary(info)
                .to_file_xml(path.join("Contents/Info.plist"))
                .unwrap();
            apps.push(serde_json::from_value::<LocalApp>(serde_json::json!({"id":name,"name":name,"path":path,"bundleId":format!("test.{name}"),"version":"1.0","teamId":"TESTTEAM01","source":"direct","running":false,"removable":true})).unwrap());
            let release = if name == "broken" {
                serde_json::Value::Null
            } else {
                serde_json::json!({"version":if name == "current" {"1.0"} else {"2.0"},"files":[{"name":"App.zip","url":"https://vendor.example.org/App.zip"}]})
            };
            rules.push(serde_json::from_value::<FeedRule>(serde_json::json!({"bundleId":format!("test.{name}"),"source":"vendor","teamId":"TESTTEAM01","release":release})).unwrap());
        }
        let mut candidates = vec![];
        let check = queue
            .updater
            .check_native_with_rules(
                &[UpdateSource::Vendor],
                &apps,
                &mut candidates,
                true,
                Ok(rules.clone()),
            )
            .await
            .unwrap();
        assert!(check.result().is_ok());
        assert_eq!(check.failed_sources(), [UpdateSource::Vendor]);
        assert_eq!(candidates.len(), 1);
        assert_eq!(candidates[0].item.app_id.as_deref(), Some("working"));
        assert_eq!(
            candidates[0].item.download_host.as_deref(),
            Some("vendor.example.org")
        );
        let failed = queue
            .updater
            .check_native_with_rules(
                &[UpdateSource::Vendor],
                &apps[1..2],
                &mut candidates,
                true,
                Ok(rules.clone()),
            )
            .await
            .unwrap();
        assert_eq!(failed.result().unwrap_err().code, "E_UPDATE_FEED");
        // Checking an up-to-date app is a success, even though no candidate is produced.
        let empty_success = queue
            .updater
            .check_native_with_rules(
                &[UpdateSource::Vendor],
                &apps[1..],
                &mut candidates,
                true,
                Ok(rules.clone()),
            )
            .await
            .unwrap();
        assert!(empty_success.result().is_ok());
        database.set_meta("update_feed_rules", &serde_json::json!({"url":"unused","body":serde_json::json!({"items":rules}).to_string()}).to_string()).unwrap();
        let outage = queue
            .updater
            .check_native_with_rules(
                &[UpdateSource::Github, UpdateSource::Vendor],
                &apps,
                &mut candidates,
                true,
                Err(AppError::new("E_UPDATE_FEED", "rules_404")),
            )
            .await
            .unwrap();
        assert!(outage.result().is_ok());
        assert_eq!(outage.failed_sources(), [UpdateSource::Vendor]);
        let retained = candidates.len();
        database.set_meta("update_feed_rules", "{}").unwrap();
        let no_cache = queue
            .updater
            .check_native_with_rules(
                &[UpdateSource::Vendor],
                &apps,
                &mut candidates,
                true,
                Err(AppError::new("E_UPDATE_FEED", "rules_404")),
            )
            .await
            .unwrap();
        assert!(no_cache.result().is_err());
        assert_eq!(candidates.len(), retained);
    }
}
