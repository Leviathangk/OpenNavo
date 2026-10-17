use crate::{AppError, catalog, core::DesktopCore, model::*};
use std::sync::Arc;
use tauri::State;
#[tauri::command]
#[specta::specta]
pub async fn catalog_status(state: State<'_, Arc<DesktopCore>>) -> Result<CatalogStatus, AppError> {
    state.catalog.status().await
}
#[tauri::command]
#[specta::specta]
pub async fn catalog_sync(
    state: State<'_, Arc<DesktopCore>>,
    force: bool,
) -> Result<SyncResult, AppError> {
    state.inner().sync_catalog(force).await
}
#[tauri::command]
#[specta::specta]
pub async fn catalog_search(
    state: State<'_, Arc<DesktopCore>>,
    query: SearchQuery,
) -> Result<Page<SearchHit>, AppError> {
    let database = state.database.clone();
    crate::blocking(move || {
        catalog::search(&database, &query, chrono::Utc::now().timestamp_millis())
    })
    .await
}
#[tauri::command]
#[specta::specta]
pub async fn catalog_list(
    state: State<'_, Arc<DesktopCore>>,
    query: ListQuery,
) -> Result<Page<CatalogItem>, AppError> {
    let database = state.database.clone();
    let locale = state.current_settings()?.locale;
    crate::blocking(move || catalog::list_locale(&database, &query, locale)).await
}
#[tauri::command]
#[specta::specta]
pub async fn catalog_get(
    state: State<'_, Arc<DesktopCore>>,
    kind: Kind,
    token: String,
) -> Result<Option<CatalogItem>, AppError> {
    let database = state.database.clone();
    crate::blocking(move || catalog::get(&database, kind, &token)).await
}
#[tauri::command]
#[specta::specta]
pub async fn catalog_categories(
    state: State<'_, Arc<DesktopCore>>,
) -> Result<Vec<CategoryItem>, AppError> {
    let database = state.database.clone();
    let locale = state.current_settings()?.locale;
    crate::blocking(move || catalog::categories(&database, locale)).await
}

#[tauri::command]
#[specta::specta]
pub async fn link_open(
    state: State<'_, Arc<DesktopCore>>,
    token: String,
    target: catalog::links::LinkOpenTarget,
) -> Result<(), AppError> {
    catalog::links::open(&state.database, token, target, |url| async move {
        crate::system::open(&[url]).await
    })
    .await
}
