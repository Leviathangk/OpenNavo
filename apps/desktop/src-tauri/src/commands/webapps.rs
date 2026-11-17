use crate::error::AppError;
use crate::webapps::{self, CreateWebApp, WebApp};

#[tauri::command]
#[specta::specta]
pub fn webapps_list() -> Result<Vec<WebApp>, AppError> {
    webapps::list()
}

#[tauri::command]
#[specta::specta]
pub async fn webapps_create(input: CreateWebApp) -> Result<WebApp, AppError> {
    webapps::create(input).await
}

#[tauri::command]
#[specta::specta]
pub fn webapps_delete(id: String) -> Result<(), AppError> {
    webapps::delete(&id)
}

#[tauri::command]
#[specta::specta]
pub fn webapps_open(id: String) -> Result<(), AppError> {
    webapps::open(&id)
}

#[tauri::command]
#[specta::specta]
pub fn webapps_reveal(id: String) -> Result<(), AppError> {
    webapps::reveal(&id)
}
