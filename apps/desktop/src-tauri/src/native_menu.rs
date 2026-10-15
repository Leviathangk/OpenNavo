use crate::{AppError, model::Locale};
use tauri::menu::{
    AboutMetadata, HELP_SUBMENU_ID, Menu, PredefinedMenuItem as P, Submenu, WINDOW_SUBMENU_ID,
};
fn error(e: tauri::Error) -> AppError {
    AppError::new("E_UNKNOWN", "native_menu").with_detail(e.to_string())
}
pub fn install(app: &tauri::AppHandle, locale: Locale) -> Result<(), AppError> {
    let t = |key| crate::native_i18n::text(locale, key);
    let metadata = AboutMetadata {
        name: Some("OpenNavo".into()),
        version: Some(app.package_info().version.to_string()),
        copyright: app.config().bundle.copyright.clone(),
        authors: app
            .config()
            .bundle
            .publisher
            .clone()
            .map(|value| vec![value]),
        ..Default::default()
    };
    let menu = Menu::with_items(
        app,
        &[
            &Submenu::with_items(
                app,
                "OpenNavo",
                true,
                &[
                    &P::about(app, Some(&t("menu.about")?), Some(metadata)).map_err(error)?,
                    &P::separator(app).map_err(error)?,
                    &P::services(app, Some(&t("menu.services")?)).map_err(error)?,
                    &P::separator(app).map_err(error)?,
                    &P::hide(app, Some(&t("menu.hide")?)).map_err(error)?,
                    &P::hide_others(app, Some(&t("menu.hideOthers")?)).map_err(error)?,
                    &P::separator(app).map_err(error)?,
                    // Keep the native quit action: lifecycle answers its applicationShouldTerminate: with the same confirmation flow as ExitRequested.
                    &P::quit(app, Some(&t("menu.quit")?)).map_err(error)?,
                ],
            )
            .map_err(error)?,
            &Submenu::with_items(
                app,
                t("menu.file")?,
                true,
                &[&P::close_window(app, Some(&t("menu.close")?)).map_err(error)?],
            )
            .map_err(error)?,
            &Submenu::with_items(
                app,
                t("menu.edit")?,
                true,
                &[
                    &P::undo(app, Some(&t("menu.undo")?)).map_err(error)?,
                    &P::redo(app, Some(&t("menu.redo")?)).map_err(error)?,
                    &P::separator(app).map_err(error)?,
                    &P::cut(app, Some(&t("menu.cut")?)).map_err(error)?,
                    &P::copy(app, Some(&t("menu.copy")?)).map_err(error)?,
                    &P::paste(app, Some(&t("menu.paste")?)).map_err(error)?,
                    &P::select_all(app, Some(&t("menu.selectAll")?)).map_err(error)?,
                ],
            )
            .map_err(error)?,
            &Submenu::with_items(
                app,
                t("menu.view")?,
                true,
                &[&P::fullscreen(app, Some(&t("menu.fullscreen")?)).map_err(error)?],
            )
            .map_err(error)?,
            &Submenu::with_id_and_items(
                app,
                WINDOW_SUBMENU_ID,
                t("menu.window")?,
                true,
                &[
                    &P::minimize(app, Some(&t("menu.minimize")?)).map_err(error)?,
                    &P::maximize(app, Some(&t("menu.maximize")?)).map_err(error)?,
                    &P::separator(app).map_err(error)?,
                    &P::close_window(app, Some(&t("menu.close")?)).map_err(error)?,
                ],
            )
            .map_err(error)?,
            &Submenu::with_id_and_items(app, HELP_SUBMENU_ID, t("menu.help")?, true, &[])
                .map_err(error)?,
        ],
    )
    .map_err(error)?;
    app.set_menu(menu).map_err(error)?;
    Ok(())
}
