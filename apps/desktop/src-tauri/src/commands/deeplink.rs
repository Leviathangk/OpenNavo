use crate::{
    AppError,
    confirmation::{ConfirmationRequest, ConfirmationService},
    core::DesktopCore,
    deeplink::{InstallStep, InstallTarget, Router},
    model::{Task, TaskKind, TaskTarget, TaskTrigger},
};
use reqwest::header::{HeaderMap, HeaderValue};
use serde::Deserialize;
use std::sync::Arc;
use tauri::{AppHandle, Manager, State};

#[derive(Debug, Clone, serde::Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct DeepLinkInstallResult {
    pub task: Task,
    pub remaining: u32,
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
struct CollectionPackage {
    kind: String,
    token: String,
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
            let targets = load_collection_targets(&state, &slug).await;
            match targets {
                Ok(targets) => {
                    router.set_collection_targets(&pending_id, targets)?;
                    router.begin_install(&pending_id)?
                }
                Err(error) => {
                    router.release_install(&pending_id);
                    return Err(error);
                }
            }
        }
        step => step,
    };
    let InstallStep::Target(InstallTarget {
        source,
        target,
        index: _,
        total: _,
    }) = step
    else {
        router.cancel_install(&pending_id);
        return Err(AppError::new("E_INVALID_ARG", "deeplink_collection_empty"));
    };

    let request = ConfirmationRequest {
        caller: "deep link".into(),
        source,
        action: "install".into(),
        target: format!("{}/{}", target.kind.as_str(), target.token),
        plan_digest: None,
    };
    let grant = match confirmation.confirm(&app, request.clone()).await {
        Ok(grant) => grant,
        Err(error) => {
            if error.code == "E_BUSY" {
                router.release_install(&pending_id);
            } else {
                router.cancel_install(&pending_id);
            }
            return Err(error);
        }
    };
    if let Err(error) = confirmation.consume(&grant, &request) {
        router.cancel_install(&pending_id);
        return Err(error);
    }

    let queue = state.queue.clone();
    let queue_target = target;
    let task = match crate::blocking(move || {
        queue.enqueue(
            crate::model::TaskOp::Install,
            Some(queue_target),
            crate::model::TaskOptions::default(),
            TaskTrigger::Deeplink,
        )
    })
    .await
    {
        Ok(task) => task,
        Err(error) => {
            router.cancel_install(&pending_id);
            return Err(error);
        }
    };
    let remaining = match router.finish_install(&pending_id) {
        Ok(remaining) => remaining,
        Err(error) => {
            router.cancel_install(&pending_id);
            return Err(error);
        }
    };
    Ok(DeepLinkInstallResult {
        task,
        remaining: remaining as u32,
    })
}

async fn load_collection_targets(
    state: &DesktopCore,
    slug: &str,
) -> Result<Vec<TaskTarget>, AppError> {
    let base = crate::catalog::sync::configured_base()
        .trim_end_matches('/')
        .to_owned();
    let url = format!("{base}/collections/{slug}");
    let parsed =
        reqwest::Url::parse(&url).map_err(|_| AppError::new("E_NETWORK", "catalog_url"))?;
    if !matches!(parsed.scheme(), "http" | "https")
        || parsed.username() != ""
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
    let mut targets = Vec::new();
    for item in detail.items {
        if item.package.disabled {
            continue;
        }
        let kind = match item.package.kind.as_str() {
            "cask" => TaskKind::Cask,
            "formula" => TaskKind::Formula,
            // Link packages are external downloads and are deliberately excluded from installs.
            "link" => continue,
            _ => return Err(AppError::new("E_NETWORK", "collection_package_kind")),
        };
        crate::brew::args::validate_token(&item.package.token)?;
        if installed
            .iter()
            .any(|entry| entry.kind == kind && entry.matches_token(&item.package.token))
        {
            continue;
        }
        let target = TaskTarget {
            kind,
            token: item.package.token,
        };
        if !targets.contains(&target) {
            targets.push(target);
        }
    }
    Ok(targets)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn collection_payload_only_accepts_installable_kinds() {
        let body: CollectionResponse = serde_json::from_str(
            r#"{"code":"0000","data":{"items":[
              {"package":{"kind":"cask","token":"zed","disabled":false}},
              {"package":{"kind":"link","token":"external","disabled":false}},
              {"package":{"kind":"formula","token":"ripgrep","disabled":false}},
              {"package":{"kind":"cask","token":"disabled","disabled":true}}
            ]}}"#,
        )
        .unwrap();
        let kinds = body
            .data
            .unwrap()
            .items
            .into_iter()
            .filter(|item| !item.package.disabled && item.package.kind != "link")
            .map(|item| item.package.kind)
            .collect::<Vec<_>>();
        assert_eq!(kinds, ["cask", "formula"]);
    }
}
