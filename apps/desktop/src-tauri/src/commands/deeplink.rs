use crate::{
    AppError,
    confirmation::{
        ConfirmationListItem, ConfirmationRequest, ConfirmationService, ordered_targets_digest,
    },
    core::DesktopCore,
    deeplink::{CollectionInstallItem, CollectionInstallPlan, InstallStep, InstallTarget, Router},
    model::{InstalledItem, PermissionState, Permissions, Task, TaskKind, TaskTarget, TaskTrigger},
};
use reqwest::header::{HeaderMap, HeaderValue};
use serde::Deserialize;
use std::sync::Arc;
use tauri::{AppHandle, Manager, State};

const MAX_COLLECTION_ITEMS: usize = 30;
const MAX_COLLECTION_RESPONSE_BYTES: usize = 256 * 1024;

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
    #[serde(default)]
    name: String,
    #[serde(default)]
    display_name: String,
    #[serde(default)]
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

    let initial = router.begin_install(&pending_id)?;
    let mut reservation = InstallReservationGuard::new(&router, &pending_id);
    let step = match initial {
        InstallStep::ResolveCollection { slug, .. } => {
            match load_collection_plan(&state, &slug).await {
                Ok(plan) => {
                    router.set_collection_plan(&pending_id, plan)?;
                    router.begin_install(&pending_id)
                }
                Err(error) => Err(error),
            }
        }
        step => Ok(step),
    };
    let result = match step {
        Ok(InstallStep::Target(InstallTarget {
            source,
            target,
            index: _,
            total: _,
        })) => {
            let context = InstallContext {
                app: &app,
                state: state.inner(),
                confirmation: confirmation.inner(),
                router: &router,
                pending_id: &pending_id,
            };
            confirm_single(&context, source, target).await
        }
        Ok(InstallStep::Collection { source, slug, plan }) => {
            let context = InstallContext {
                app: &app,
                state: state.inner(),
                confirmation: confirmation.inner(),
                router: &router,
                pending_id: &pending_id,
            };
            confirm_collection(&context, source, slug, plan).await
        }
        Ok(InstallStep::ResolveCollection { .. }) => Err(AppError::new(
            "E_INVALID_ARG",
            "deeplink_collection_unresolved",
        )),
        Err(error) => Err(error),
    };
    match &result {
        Ok(_) => reservation.disarm(),
        Err(error) if error.code == "E_BUSY" => reservation.release(),
        Err(_) => {}
    }
    result
}

struct InstallContext<'a> {
    app: &'a AppHandle,
    state: &'a DesktopCore,
    confirmation: &'a Arc<ConfirmationService>,
    router: &'a Router,
    pending_id: &'a str,
}

/// Owns the router's pending install reservation for the duration of the command. If the command future is
/// cancelled while waiting for the native dialog or the collection response, the opaque id is removed instead of
/// leaving a permanently busy placeholder in the router.
struct InstallReservationGuard<'a> {
    router: &'a Router,
    pending_id: &'a str,
    armed: bool,
}

impl<'a> InstallReservationGuard<'a> {
    fn new(router: &'a Router, pending_id: &'a str) -> Self {
        Self {
            router,
            pending_id,
            armed: true,
        }
    }

    fn disarm(&mut self) {
        self.armed = false;
    }

    fn release(&mut self) {
        if self.armed {
            self.router.release_install(self.pending_id);
            self.armed = false;
        }
    }
}

impl Drop for InstallReservationGuard<'_> {
    fn drop(&mut self) {
        if self.armed {
            self.router.cancel_install(self.pending_id);
        }
    }
}

async fn confirm_single(
    context: &InstallContext<'_>,
    source: String,
    target: TaskTarget,
) -> Result<DeepLinkInstallResult, AppError> {
    if !ensure_single_target_available(context.state, &target)? {
        context.router.finish_install(context.pending_id)?;
        return Ok(DeepLinkInstallResult { tasks: vec![] });
    }
    ensure_install_environment(context.state).await?;
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

    ensure_install_environment(context.state).await?;

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
    ensure_install_environment(context.state).await?;
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
            installed: item.installed,
            already_queued: item.already_queued,
            unsupported: item.unsupported,
            duplicate: item.duplicate,
        })
        .collect();
    let grant = match context
        .confirmation
        .confirm_collection(context.app, request.clone(), items, plan.targets.len())
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

    ensure_install_environment(context.state).await?;

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

async fn ensure_install_environment(state: &DesktopCore) -> Result<(), AppError> {
    // Resolve brew in Rust so a deep link cannot bypass the same Homebrew gate used by the package button.
    let brew = state.brew_info().await?;
    if !tokio::fs::metadata(&brew.path)
        .await
        .is_ok_and(|metadata| metadata.is_file())
    {
        return Err(AppError::new("E_BREW_NOT_FOUND", "brew_not_found"));
    }
    ensure_permission_gate(state.permissions().await?)
}

fn ensure_permission_gate(permissions: Permissions) -> Result<(), AppError> {
    if permissions.relaunch_required
        || permissions.app_management_switch != PermissionState::Granted
        || permissions.full_disk_access != PermissionState::Granted
        || permissions.app_management == PermissionState::Denied
    {
        return Err(AppError::new(
            "E_PERMISSION",
            "deep_link_install_permission",
        ));
    }
    Ok(())
}

fn ensure_single_target_available(
    state: &DesktopCore,
    target: &TaskTarget,
) -> Result<bool, AppError> {
    if target.kind != TaskKind::Cask {
        return Err(AppError::new("E_INVALID_ARG", "cask_only"));
    }
    if crate::catalog::get(&state.database, crate::model::Kind::Cask, &target.token)?
        .is_some_and(|item| item.disabled)
    {
        return Ok(false);
    }
    Ok(target_available(
        target,
        &state.library_list()?,
        &state.queue.active()?,
    ))
}

fn target_available(target: &TaskTarget, installed: &[InstalledItem], active: &[Task]) -> bool {
    !installed
        .iter()
        .any(|item| item.kind == target.kind && item.matches_token(&target.token))
        && !active
            .iter()
            .any(|task| task.target.as_ref() == Some(target))
}

async fn load_collection_plan(
    state: &DesktopCore,
    slug: &str,
) -> Result<CollectionInstallPlan, AppError> {
    ensure_install_environment(state).await?;
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
    let mut response = reqwest::Client::builder()
        .default_headers(headers)
        .connect_timeout(std::time::Duration::from_secs(5))
        .timeout(std::time::Duration::from_secs(15))
        .redirect(reqwest::redirect::Policy::limited(5))
        .build()?
        .get(parsed)
        .send()
        .await?
        .error_for_status()?;
    if response
        .content_length()
        .is_some_and(|length| length > MAX_COLLECTION_RESPONSE_BYTES as u64)
    {
        return Err(AppError::new("E_NETWORK", "collection_response_too_large"));
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await? {
        if bytes.len().saturating_add(chunk.len()) > MAX_COLLECTION_RESPONSE_BYTES {
            return Err(AppError::new("E_NETWORK", "collection_response_too_large"));
        }
        bytes.extend_from_slice(&chunk);
    }
    let body = decode_collection_response(&bytes)?;
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
    let active = state.queue.active()?;
    build_collection_plan(detail, &installed, &active)
}

fn decode_collection_response(bytes: &[u8]) -> Result<CollectionResponse, AppError> {
    if bytes.len() > MAX_COLLECTION_RESPONSE_BYTES {
        return Err(AppError::new("E_NETWORK", "collection_response_too_large"));
    }
    Ok(serde_json::from_slice(bytes)?)
}

fn build_collection_plan(
    detail: CollectionDetail,
    installed: &[InstalledItem],
    active: &[Task],
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
        let unsupported = installable_kind.is_none();
        let mut installable = false;
        let mut installed_match = false;
        let mut already_queued = false;
        let mut duplicate = false;
        if let Some(kind) = installable_kind {
            crate::brew::args::validate_token(&package.token)?;
            installed_match = installed
                .iter()
                .any(|entry| entry.kind == kind && entry.matches_token(&package.token));
            let target = TaskTarget {
                kind,
                token: package.token,
            };
            already_queued = active
                .iter()
                .any(|task| task.target.as_ref() == Some(&target));
            installable = !package.disabled && !installed_match && !already_queued;
            if installable {
                if !targets.contains(&target) {
                    targets.push(target);
                } else {
                    installable = false;
                    duplicate = true;
                }
            }
        }
        items.push(CollectionInstallItem {
            name,
            disabled: package.disabled,
            installed: installed_match,
            already_queued,
            unsupported,
            duplicate,
            installable,
        });
    }
    Ok(CollectionInstallPlan { items, targets })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::TaskOp;

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
        let plan = build_collection_plan(detail, &[], &[]).unwrap();
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
        assert!(plan.items[2].unsupported);
        assert!(plan.items[3].unsupported);
    }

    #[test]
    fn collections_over_thirty_items_are_refused_before_confirmation() {
        let detail = CollectionDetail {
            items: (0..31)
                .map(|index| package("cask", &format!("app-{index}"), "App", false))
                .collect(),
        };
        let error = build_collection_plan(detail, &[], &[]).unwrap_err();
        assert_eq!(error.code, "E_INVALID_ARG");
        assert!(error.message.contains("Open the collection in OpenNavo"));
    }

    fn installed(token: &str) -> InstalledItem {
        InstalledItem {
            kind: crate::model::Kind::Cask,
            token: token.into(),
            full_token: None,
            name: token.into(),
            installed_version: "1".into(),
            actual_version: None,
            latest_version: None,
            status: crate::model::InstalledStatus::UpToDate,
            on_request: true,
            required_by: vec![],
            installed_at: None,
            size_bytes: None,
            app_paths: vec![],
            icon_path: None,
            auto_updates: false,
            pinned: false,
        }
    }

    fn active_install(token: &str) -> Task {
        Task {
            target_name: None,
            outcome: None,
            id: token.into(),
            op: TaskOp::Install,
            target: Some(TaskTarget {
                kind: TaskKind::Cask,
                token: token.into(),
            }),
            options: Default::default(),
            trigger: TaskTrigger::Deeplink,
            state: crate::model::TaskState::Queued,
            phase: None,
            percent: None,
            bytes_done: None,
            bytes_total: None,
            speed_bps: None,
            step_index: None,
            step_count: None,
            from_version: None,
            to_version: None,
            error: None,
            exit_code: None,
            log_path: None,
            created_at: 0,
            started_at: None,
            finished_at: None,
        }
    }

    #[test]
    fn collection_plan_marks_installed_and_queued_apps_and_excludes_them_from_digest_targets() {
        let detail = CollectionDetail {
            items: vec![
                package("cask", "installed", "Installed", false),
                package("cask", "queued", "Queued", false),
                package("cask", "fresh", "Fresh", false),
            ],
        };
        let plan = build_collection_plan(
            detail,
            &[installed("installed")],
            &[active_install("queued")],
        )
        .unwrap();
        assert!(plan.items[0].installed);
        assert!(plan.items[1].already_queued);
        assert!(!plan.items[0].installable);
        assert!(!plan.items[1].installable);
        assert_eq!(plan.targets.len(), 1);
        assert_eq!(plan.targets[0].token, "fresh");
    }

    #[test]
    fn collection_response_is_bounded_before_json_parsing() {
        let bytes = vec![b' '; MAX_COLLECTION_RESPONSE_BYTES + 1];
        assert_eq!(
            decode_collection_response(&bytes).unwrap_err().message,
            "collection_response_too_large"
        );
    }

    #[test]
    fn deep_link_install_requires_the_same_permission_gate_as_the_frontend() {
        let granted = Permissions {
            app_management: PermissionState::Granted,
            app_management_switch: PermissionState::Granted,
            full_disk_access: PermissionState::Granted,
            relaunch_required: false,
            relaunch_ineffective: false,
            app_management_pending: false,
            full_disk_access_pending: false,
        };
        assert!(ensure_permission_gate(granted).is_ok());
        for status in [PermissionState::Denied, PermissionState::Unknown] {
            assert!(
                ensure_permission_gate(Permissions {
                    app_management_switch: status,
                    ..granted
                })
                .is_err()
            );
            assert!(
                ensure_permission_gate(Permissions {
                    full_disk_access: status,
                    ..granted
                })
                .is_err()
            );
        }
        assert_eq!(
            ensure_permission_gate(Permissions {
                full_disk_access: PermissionState::Denied,
                ..granted
            })
            .unwrap_err()
            .code,
            "E_PERMISSION"
        );
        assert_eq!(
            ensure_permission_gate(Permissions {
                relaunch_required: true,
                ..granted
            })
            .unwrap_err()
            .code,
            "E_PERMISSION"
        );
    }

    #[test]
    fn dropping_the_install_command_reservation_clears_the_router_placeholder() {
        use crate::events::CoreEvent;
        use std::sync::Mutex;
        let events = std::sync::Arc::new(Mutex::new(Vec::new()));
        let saved = events.clone();
        let router = Router::new(
            Arc::new(move |event| saved.lock().unwrap().push(event)),
            Arc::new(|| {}),
        );
        router.ready();
        router.receive("opennavo://package/cask/ghostty?action=install");
        let pending_id = match &events.lock().unwrap()[0] {
            CoreEvent::Deeplink(event) => event.pending_id.clone().unwrap(),
            _ => panic!("expected deeplink"),
        };
        let _ = router.begin_install(&pending_id).unwrap();
        let guard = InstallReservationGuard::new(&router, &pending_id);
        drop(guard);
        assert_eq!(
            router.begin_install(&pending_id).unwrap_err().code,
            "E_INVALID_ARG"
        );
    }

    fn core_fixture() -> (tempfile::TempDir, Arc<DesktopCore>) {
        let tmp = tempfile::tempdir().unwrap();
        let core = DesktopCore::open(
            tmp.path().join("data"),
            tmp.path().join("cache"),
            tmp.path().join("askpass"),
            Arc::new(|_| {}),
        )
        .unwrap();
        (tmp, core)
    }

    #[test]
    fn single_targets_skip_disabled_installed_and_queued_before_confirmation() {
        let (_tmp, core) = core_fixture();
        let target = |token: &str| TaskTarget {
            kind: TaskKind::Cask,
            token: token.into(),
        };
        let mut disabled = crate::catalog::links::tests::item();
        disabled.kind = crate::model::Kind::Cask;
        disabled.link = None;
        disabled.token = "disabled".into();
        disabled.disabled = true;
        crate::catalog::apply_changes(
            &core.database,
            &crate::catalog::Changes {
                changes: vec![crate::catalog::Change {
                    cursor: 1,
                    op: "upsert".into(),
                    kind: crate::model::Kind::Cask,
                    token: disabled.token.clone(),
                    item: Some(disabled),
                }],
                next_cursor: 1,
                has_more: false,
                categories: None,
                category_names: None,
            },
            0,
        )
        .unwrap();
        core.library.write().unwrap().push(installed("installed"));
        core.queue
            .enqueue(
                TaskOp::Upgrade,
                Some(target("queued")),
                Default::default(),
                TaskTrigger::Manual,
            )
            .unwrap();
        for token in ["disabled", "installed", "queued"] {
            assert!(
                !ensure_single_target_available(&core, &target(token)).unwrap(),
                "{token}"
            );
        }
        assert!(ensure_single_target_available(&core, &target("fresh")).unwrap());
        assert_eq!(
            ensure_single_target_available(
                &core,
                &TaskTarget {
                    kind: TaskKind::Formula,
                    token: "fzf".into()
                }
            )
            .unwrap_err()
            .message,
            "cask_only"
        );
    }

    #[tokio::test]
    async fn homebrew_gate_rechecks_cached_executable_before_confirmation() {
        let (tmp, core) = core_fixture();
        *core.brew.write().unwrap() = Some(crate::model::BrewInfo {
            path: tmp.path().join("missing-brew").to_string_lossy().into(),
            prefix: tmp.path().to_string_lossy().into(),
            version: "7".into(),
        });
        assert_eq!(
            ensure_install_environment(&core).await.unwrap_err().code,
            "E_BREW_NOT_FOUND"
        );
    }
}
