//! Mac App Store browsing. All network traffic goes directly to Apple.
//! No purchase, helper execution or administrator action is reachable here.
mod api;
mod countries;
pub mod install;
pub mod native;
pub mod progress;
pub mod region;
pub mod types;
pub mod updates;

use crate::{
    AppError,
    db::Database,
    events::{CoreEvent, EventSink},
    model::Settings,
};
use native::{Hints, Host};
use std::{
    collections::BTreeSet,
    sync::{Arc, Mutex, RwLock},
};
use tokio::sync::Mutex as AsyncMutex;
pub use types::*;

pub fn valid_adam_id(id: u64) -> bool {
    (1..=9_007_199_254_740_991).contains(&id)
}
pub fn valid_bundle_id(id: &str) -> bool {
    id.len() <= 255
        && id.contains('.')
        && id.split('.').all(|part| {
            !part.is_empty()
                && part
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || b"-_".contains(&byte))
        })
}
fn invalid(message: &str) -> AppError {
    AppError::new("E_INVALID_ARG", message)
}
fn poisoned<T>(_: T) -> AppError {
    AppError::new("E_MAS_ENGINE", "app_store_lock")
}

#[derive(Default)]
struct Observations {
    region: Option<(String, MasCountrySource)>,
    installed: Option<Vec<MasInstalled>>,
    updates: Option<Vec<MasUpdate>>,
}
struct Context {
    settings: Settings,
    hints: Hints,
    country: String,
    source: MasCountrySource,
}
pub struct Service {
    database: Database,
    settings: Arc<RwLock<Settings>>,
    sink: EventSink,
    host: Arc<dyn Host>,
    api: AsyncMutex<api::Api>,
    hints: AsyncMutex<Option<(i64, Hints)>>,
    observed: Mutex<Observations>,
    clock: Arc<dyn Fn() -> i64 + Send + Sync>,
}
impl Service {
    pub fn new(
        database: Database,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
    ) -> Result<Self, AppError> {
        Self::with_host(
            database,
            settings,
            sink,
            Arc::new(native::NativeHost),
            reqwest::Url::parse("https://itunes.apple.com/").expect("fixed Apple endpoint"),
            Arc::new(|| chrono::Utc::now().timestamp_millis()),
        )
    }
    fn with_host(
        database: Database,
        settings: Arc<RwLock<Settings>>,
        sink: EventSink,
        host: Arc<dyn Host>,
        base: reqwest::Url,
        clock: Arc<dyn Fn() -> i64 + Send + Sync>,
    ) -> Result<Self, AppError> {
        Ok(Self {
            api: AsyncMutex::new(api::Api::new(database.clone(), base, clock.clone())?),
            database,
            settings,
            sink,
            host,
            clock,
            hints: AsyncMutex::new(None),
            observed: Mutex::new(Observations::default()),
        })
    }
    async fn context(&self) -> Result<Context, AppError> {
        let settings = self.settings.read().map_err(poisoned)?.clone();
        let mut cache = self.hints.lock().await;
        let now = (self.clock)();
        if cache
            .as_ref()
            .is_none_or(|(at, _)| *at > now || now - at >= 30_000)
        {
            let host = self.host.clone();
            *cache = Some((now, crate::blocking(move || host.hints()).await?));
        }
        let hints = cache.as_ref().expect("populated hints").1.clone();
        drop(cache);
        let (country, source) = region::resolve(
            &settings.app_store_region,
            hints.storefront.as_deref(),
            &hints.locale,
        )?;
        let mut observed = self.observed.lock().map_err(poisoned)?;
        let changed = observed
            .region
            .as_ref()
            .is_some_and(|old| old != &(country.clone(), source));
        observed.region = Some((country.clone(), source));
        if changed {
            observed.updates = None;
        }
        drop(observed);
        if changed {
            self.changed(AppStoreChangeReason::Region);
        }
        Ok(Context {
            settings,
            hints,
            country,
            source,
        })
    }
    fn changed(&self, reason: AppStoreChangeReason) {
        (self.sink)(CoreEvent::AppStore(AppStoreChange { reason }));
    }
    pub fn settings_changed(&self) -> Result<(), AppError> {
        let mut observed = self.observed.lock().map_err(poisoned)?;
        observed.region = None;
        observed.updates = None;
        drop(observed);
        self.changed(AppStoreChangeReason::Region);
        Ok(())
    }
    pub async fn status(&self) -> Result<MasStatus, AppError> {
        let context = self.context().await?;
        Ok(MasStatus {
            country: context.country,
            country_source: context.source,
            engine: context.hints.engine,
            admin_install: region::admin_install(&context.hints.os_version),
        })
    }
    pub async fn search(&self, term: String, limit: u32) -> Result<Vec<MasApp>, AppError> {
        if !(1..=50).contains(&limit)
            || term.chars().count() > 200
            || term.chars().any(char::is_control)
        {
            return Err(invalid("mas_search"));
        }
        let term = term.trim();
        if term.is_empty() {
            return Ok(vec![]);
        }
        let context = self.context().await?;
        if !context.settings.app_store_search {
            return Ok(vec![]);
        }
        let mut api = self.api.lock().await;
        api.search(
            &context.country,
            context.settings.locale,
            term,
            limit,
            (self.clock)(),
        )
        .await
    }
    pub async fn lookup(&self, mut adam_ids: Vec<u64>) -> Result<Vec<MasApp>, AppError> {
        if adam_ids.len() > 100 || adam_ids.iter().any(|id| !valid_adam_id(*id)) {
            return Err(invalid("mas_adam_ids"));
        }
        if adam_ids.is_empty() {
            return Ok(vec![]);
        }
        let mut seen = BTreeSet::new();
        adam_ids.retain(|id| seen.insert(*id));
        let context = self.context().await?;
        let mut api = self.api.lock().await;
        api.lookup_ids(
            &context.country,
            context.settings.locale,
            &adam_ids,
            (self.clock)(),
        )
        .await
    }
    pub async fn lookup_bundles(
        &self,
        bundle_ids: Vec<String>,
    ) -> Result<MasBundleLookup, AppError> {
        if bundle_ids.len() > 100 || bundle_ids.iter().any(|id| !valid_bundle_id(id)) {
            return Err(invalid("mas_bundle_ids"));
        }
        if bundle_ids.is_empty() {
            return Ok(MasBundleLookup::default());
        }
        let ids: Vec<_> = bundle_ids
            .into_iter()
            .collect::<BTreeSet<_>>()
            .into_iter()
            .collect();
        let context = self.context().await?;
        let mut api = self.api.lock().await;
        Ok(MasBundleLookup(
            api.lookup_bundles(
                &context.country,
                context.settings.locale,
                &ids,
                (self.clock)(),
            )
            .await?,
        ))
    }
    async fn installed_in(&self, context: &Context) -> Result<Vec<MasInstalled>, AppError> {
        let host = self.host.clone();
        let candidates = crate::blocking(move || host.installed()).await?;
        // Identity mappings outlive metadata TTLs; update detection still requires
        // fresh Apple metadata with both the same Adam ID and bundle ID.
        let mut identities: std::collections::BTreeMap<String, u64> = self
            .database
            .meta("mas_installed_ids_v1")?
            .and_then(|text| serde_json::from_str(&text).ok())
            .unwrap_or_default();
        identities.retain(|bundle, id| {
            valid_adam_id(*id) && candidates.iter().any(|app| &app.bundle_id == bundle)
        });
        for app in &candidates {
            if let Some(id) = app.adam_id {
                identities.insert(app.bundle_id.clone(), id);
            }
        }
        let missing: BTreeSet<_> = candidates
            .iter()
            .filter(|app| app.adam_id.is_none() && !identities.contains_key(&app.bundle_id))
            .map(|app| app.bundle_id.clone())
            .collect();
        if !missing.is_empty() {
            let mut api = self.api.lock().await;
            for bundle in missing {
                if let Some(app) = api
                    .installed_bundle(&context.country, context.settings.locale, &bundle)
                    .await?
                {
                    identities.insert(bundle, app.adam_id);
                    // Keep progress across interruption/restart during cold resolution.
                    self.database
                        .set_meta("mas_installed_ids_v1", &serde_json::to_string(&identities)?)?;
                }
            }
        }
        self.database
            .set_meta("mas_installed_ids_v1", &serde_json::to_string(&identities)?)?;
        let result: Vec<_> = candidates
            .into_iter()
            .filter_map(|app| {
                Some(MasInstalled {
                    adam_id: app
                        .adam_id
                        .or_else(|| identities.get(&app.bundle_id).copied())?,
                    bundle_id: app.bundle_id,
                    name: app.name,
                    version: app.version,
                    path: app.path,
                    installed_at: app.installed_at,
                })
            })
            .collect();
        let mut observed = self.observed.lock().map_err(poisoned)?;
        let changed = observed.installed.as_ref() != Some(&result);
        observed.installed = Some(result.clone());
        drop(observed);
        if changed {
            self.changed(AppStoreChangeReason::Installed);
        }
        Ok(result)
    }
    pub async fn installed(&self) -> Result<Vec<MasInstalled>, AppError> {
        let context = self.context().await?;
        self.installed_in(&context).await
    }
    pub async fn updates(&self) -> Result<Vec<MasUpdate>, AppError> {
        let context = self.context().await?;
        let result = if context
            .settings
            .update_sources
            .enabled(crate::model::UpdateSource::Mas)
        {
            let local = self.installed_in(&context).await?;
            let ids: Vec<_> = local
                .iter()
                .map(|app| app.adam_id)
                .collect::<BTreeSet<_>>()
                .into_iter()
                .collect();
            let mut api = self.api.lock().await;
            let mut records = std::collections::BTreeMap::new();
            for ids in ids.chunks(100) {
                records.extend(
                    api.update_ids(&context.country, context.settings.locale, ids)
                        .await?
                        .into_iter()
                        .map(|app| (app.bundle_id.clone(), app)),
                );
            }
            updates::detect(&local, &records, &context.hints.os_version)
        } else {
            vec![]
        };
        let key = format!("mas_updates_{}", context.country);
        self.database
            .set_meta(&key, &serde_json::to_string(&result)?)?;
        let mut observed = self.observed.lock().map_err(poisoned)?;
        let changed = observed.updates.as_ref() != Some(&result);
        observed.updates = Some(result.clone());
        drop(observed);
        if changed {
            self.changed(AppStoreChangeReason::Updates);
        }
        Ok(result)
    }
    pub async fn open_store(&self, adam_id: u64) -> Result<(), AppError> {
        if !valid_adam_id(adam_id) {
            return Err(invalid("mas_adam_id"));
        }
        let country = self.context().await?.country;
        let url = format!("macappstore://apps.apple.com/{country}/app/id{adam_id}?mt=12");
        let host = self.host.clone();
        crate::blocking(move || host.open_store(&url)).await
    }
}

#[cfg(test)]
pub(crate) mod tests;
