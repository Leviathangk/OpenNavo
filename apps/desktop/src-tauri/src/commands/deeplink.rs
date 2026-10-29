use crate::{
    AppError,
    confirmation::{
        ConfirmationListItem, ConfirmationRequest, ConfirmationService, ordered_targets_digest,
    },
    core::DesktopCore,
    deeplink::{CollectionInstallItem, CollectionInstallPlan, InstallStep, InstallTarget, Router},
    model::{InstalledItem, Task, TaskKind, TaskTarget, TaskTrigger},
};
use reqwest::header::{HeaderMap, HeaderValue};
use serde::Deserialize;
use std::sync::Arc;
use tauri::{AppHandle, Manager, State};

const MAX_COLLECTION_ITEMS: usize = 30;

#[derive(Debug, Clone, serde::Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct DeepLinkInstallResult {
    pub tasks: Vec<Task>,
}

#[derive(Debug, Deserialize)]
struct CollectionResponse {
    code: String,
    data: Option<CollectionDetail>,
}

#[derive(Debug, Deserialize)]
struct CollectionDetail {
    items: Vec<CollectionItem>,
}

#[derive(Debug, Deserialize)]
struct CollectionItem {
    package: CollectionPackage,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct CollectionPackage {
    kind: String,
    token: String,
    name: String,
    display_name: String,
    disabled: bool,
}

#[tauri::command]
#[specta::specta]
pub async fn deeplink_install_confirm(
    app: AppHandle,
    state: State<'_, Arc<DesktopCore>>,
    confirmation: State<'_, Arc<ConfirmationService>>,
    pending_id: String,
) -> Result<DeepLinkInstallResult, AppError> {
    let router = app
        .try_state::<Arc<Router>>()
        .ok_or_else(|| AppError::new("E_INVALID_ARG", "deeplink_router_missing"))?;

    let step = match router.begin_install(&pending_id)? {
        InstallStep::ResolveCollection { slug, .. } => {
            let plan = load_collection_plan(&state, &slug).await;
            match plan {
                Ok(plan) => {
                    router.set_collection_plan(&pending_id, plan)?;
                    router.begin_install(&pending_id)?
                }
                Err(error) => {
                    router.cancel_install(&pending_id);
                    return Err(error);
                }
            }
        }
        step => step,
    };

    match step {
        InstallStep::Target(InstallTarget {
            source,
            target,
            index: _,
            total: _,
        }) => {
            let context = InstallContext {
                app: &app,
                state: state.inner(),
                confirmation: confirmation.inner(),
                router: &router,
                pending_id: &pending_id,
            };
            confirm_single(&context, source, target).await
        }
        InstallStep::Collection { source, slug, plan } => {
            let context = InstallContext {
                app: &app,
                state: state.inner(),
                confirmation: confirmation.inner(),
                router: &router,
                pending_id: &pending_id,
            };
            confirm_collection(&context, source, slug, plan).await
        }
        InstallStep::ResolveCollection { .. } => {
            router.cancel_install(&pending_id);
            Err(AppError::new(
                "E_INVALID_ARG",
                "deeplink_collection_unresolved",
            ))
        }
    }
}

struct InstallContext<'a> {
    app: &'a AppHandle,
    state: &'a DesktopCore,
    confirmation: &'a Arc<ConfirmationService>,
    router: &'a Router,
    pending_id: &'a str,
}

async fn confirm_single(
    context: &InstallContext<'_>,
    source: String,
    target: TaskTarget,
) -> Result<DeepLinkInstallResult, AppError> {
    let request = ConfirmationRequest {
        caller: "deep link".into(),
        source,
        action: "install".into(),
        target: format!("{}/{}", target.kind.as_str(), target.token),
        plan_digest: None,
    };
    let grant = match context
        .confirmation
        .confirm(context.app, request.clone())
        .await
    {
        Ok(grant) => grant,
        Err(error) => {
            if error.code == "E_BUSY" {
                context.router.release_install(context.pending_id);
            } else {
                context.router.cancel_install(context.pending_id);
            }
            return Err(error);
        }
    };
    if let Err(error) = context.confirmation.consume(&grant, &request) {
        context.router.cancel_install(context.pending_id);
        return Err(error);
    }

    let queue = context.state.queue.clone();
    let task = match crate::blocking(move || {
        queue.enqueue(
            crate::model::TaskOp::Install,
            Some(target),
            crate::model::TaskOptions::default(),
            TaskTrigger::Deeplink,
        )
    })
    .await
    {
        Ok(task) => task,
        Err(error) => {
            context.router.cancel_install(context.pending_id);
            return Err(error);
        }
    };
    if let Err(error) = context.router.finish_install(context.pending_id) {
        context.router.cancel_install(context.pending_id);
        return Err(error);
    }
    Ok(DeepLinkInstallResult { tasks: vec![task] })
}

async fn confirm_collection(
    context: &InstallContext<'_>,
    source: String,
    slug: String,
    plan: CollectionInstallPlan,
) -> Result<DeepLinkInstallResult, AppError> {
    let request = ConfirmationRequest {
        caller: "deep link".into(),
        source,
        action: "install collection".into(),
        target: format!("collection/{slug}"),
        plan_digest: Some(ordered_targets_digest(&plan.targets)),
    };
    let items = plan
        .items
        .iter()
        .map(|item| ConfirmationListItem {
            name: item.name.clone(),
            disabled: item.disabled,
        })
        .collect();
    let grant = match context
        .confirmation
        .confirm_collection(context.app, request.clone(), items)
        .await
    {
        Ok(grant) => grant,
        Err(error) => {
            if error.code == "E_BUSY" {
                context.router.release_install(context.pending_id);
            } else {
                context.router.cancel_install(context.pending_id);
            }
            return Err(error);
        }
    };
    if let Err(error) = context.confirmation.consume(&grant, &request) {
        context.router.cancel_install(context.pending_id);
        return Err(error);
    }

    let queue = context.state.queue.clone();
    let targets = plan.targets;
    let tasks = match crate::blocking(move || {
        queue.enqueue_many(
            crate::model::TaskOp::Install,
            targets,
            crate::model::TaskOptions::default(),
            TaskTrigger::Deeplink,
        )
    })
    .await
    {
        Ok(tasks) => tasks,
        Err(error) => {
            context.router.cancel_install(context.pending_id);
            return Err(error);
        }
    };
    if let Err(error) = context.router.finish_install(context.pending_id) {
        context.router.cancel_install(context.pending_id);
        return Err(error);
    }
    Ok(DeepLinkInstallResult { tasks })
}

async fn load_collection_plan(
    state: &DesktopCore,
    slug: &str,
) -> Result<CollectionInstallPlan, AppError> {
    let base = crate::catalog::sync::configured_base()
        .trim_end_matches('/')
        .to_owned();
    let url = format!("{base}/collections/{slug}");
    let parsed =
        reqwest::Url::parse(&url).map_err(|_| AppError::new("E_NETWORK", "catalog_url"))?;
    if !matches!(parsed.scheme(), "http" | "https")
        || !parsed.username().is_empty()
        || parsed.password().is_some()
    {
        return Err(AppError::new("E_NETWORK", "catalog_url"));
    }
    let settings = state.current_settings()?;
    let mut headers = HeaderMap::new();
    headers.insert("X-Client-Platform", HeaderValue::from_static("desktop"));
    headers.insert(
        "X-Client-Version",
        HeaderValue::from_static(env!("CARGO_PKG_VERSION")),
    );
    headers.insert(
        "Accept-Language",
        HeaderValue::from_str(settings.locale.as_str())
            .map_err(|_| AppError::new("E_NETWORK", "client_header"))?,
    );
    headers.insert("User-Agent", HeaderValue::from_static("OpenNavo/Desktop"));
    let response = reqwest::Client::builder()
        .default_headers(headers)
        .connect_timeout(std::time::Duration::from_secs(5))
        .timeout(std::time::Duration::from_secs(15))
        .redirect(reqwest::redirect::Policy::limited(5))
        .build()?
        .get(parsed)
        .send()
        .await?
        .error_for_status()?;
    let body: CollectionResponse = response.json().await?;
    if body.code != "0000" {
        return Err(AppError::new("E_NETWORK", "collection_response"));
    }
    let detail = body
        .data
        .ok_or_else(|| AppError::new("E_NETWORK", "collection_response"))?;
    let installed = state
        .library
        .read()
        .map_err(|_| AppError::new("E_UNKNOWN", "library_state"))?;
    build_collection_plan(detail, &installed)
}

fn build_collection_plan(
    detail: CollectionDetail,
    installed: &[InstalledItem],
) -> Result<CollectionInstallPlan, AppError> {
    if detail.items.len() > MAX_COLLECTION_ITEMS {
        return Err(AppError::new(
            "E_INVALID_ARG",
            "This collection has more than 30 apps. Open the collection in OpenNavo to review and install it.",
        ));
    }
    let mut items = Vec::with_capacity(detail.items.len());
    let mut targets = Vec::new();
    for item in detail.items {
        let package = item.package;
        let name = if package.display_name.trim().is_empty() {
            if package.name.trim().is_empty() {
                package.token.clone()
            } else {
                package.name.clone()
            }
        } else {
            package.display_name.clone()
        };
        let installable_kind = match package.kind.as_str() {
            "cask" => Some(TaskKind::Cask),
            // Formulae and link packages remain visible in the reviewed collection, but ADR-018 keeps them out of
            // the desktop install queue.
            "formula" | "link" => None,
            _ => return Err(AppError::new("E_NETWORK", "collection_package_kind")),
        };
        let mut installable = false;
        if let Some(kind) = installable_kind {
            crate::brew::args::validate_token(&package.token)?;
            installable = !package.disabled
                && !installed
                    .iter()
                    .any(|entry| entry.kind == kind && entry.matches_token(&package.token));
            if installable {
                let target = TaskTarget {
                    kind,
                    token: package.token,
                };
                if !targets.contains(&target) {
                    targets.push(target);
                } else {
                    installable = false;
                }
            }
        }
        items.push(CollectionInstallItem {
            name,
            disabled: package.disabled,
            installable,
        });
    }
    Ok(CollectionInstallPlan { items, targets })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn package(kind: &str, token: &str, name: &str, disabled: bool) -> CollectionItem {
        CollectionItem {
            package: CollectionPackage {
                kind: kind.into(),
                token: token.into(),
                name: name.into(),
                display_name: name.into(),
                disabled,
            },
        }
    }

    #[test]
    fn collection_plan_preserves_display_order_and_excludes_disabled_or_non_casks() {
        let detail = CollectionDetail {
            items: vec![
                package("cask", "zed", "Zed", false),
                package("cask", "disabled", "Disabled App", true),
                package("formula", "ripgrep", "ripgrep", false),
                package("link", "external", "External App", false),
            ],
        };
        let plan = build_collection_plan(detail, &[]).unwrap();
        assert_eq!(
            plan.items
                .iter()
                .map(|item| item.name.as_str())
                .collect::<Vec<_>>(),
            ["Zed", "Disabled App", "ripgrep", "External App"]
        );
        assert_eq!(
            plan.targets,
            vec![TaskTarget {
                kind: TaskKind::Cask,
                token: "zed".into()
            }]
        );
        assert!(plan.items[1].disabled);
        assert!(!plan.items[1].installable);
    }

    #[test]
    fn collections_over_thirty_items_are_refused_before_confirmation() {
        let detail = CollectionDetail {
            items: (0..31)
                .map(|index| package("cask", &format!("app-{index}"), "App", false))
                .collect(),
        };
        let error = build_collection_plan(detail, &[]).unwrap_err();
        assert_eq!(error.code, "E_INVALID_ARG");
        assert!(error.message.contains("Open the collection in OpenNavo"));
    }
}
