//! App-side Local MCP write tools: `install_app`, `uninstall_app` and
//! `clean_cache`.
//!
//! Every write goes through the same steps, in order:
//! 1. The audit log must be writable, otherwise the write is refused.
//! 2. The request is planned (catalog lookup / installed-app lookup) and
//!    anything that cannot be done is refused before the user is asked.
//! 3. One native confirmation dialog (the shared single-slot
//!    `ConfirmationService`; another open dialog means `E_BUSY`). The dialog
//!    names the direct parent of the `mcp` subprocess by its code signature,
//!    or "Unsigned · path", then the nearest `.app` ancestor marked
//!    unverified, then the self-reported client name.
//! 4. The grant is consumed at once and is only honoured while the
//!    connection is open and both switches are on.
//! 5. The approval is recorded; if that fails nothing runs.
//! 6. The install is queued like a deep-link install, or the `.app` bundle
//!    alone is moved to the Trash through the uninstall engine.

use std::sync::Arc;

use serde_json::{Value, json};

use super::{
    audit::{self, Audit, AuditCaller, AuditDecision, AuditEntry},
    caller::{self, SystemProcesses},
    tools::{BUSY_MESSAGE, ToolError, WriteContext, WriteRequest},
};
use crate::{
    AppError,
    confirmation::{ConfirmationRequest, ConfirmationService, McpConfirmView, display_value},
    core::DesktopCore,
    model::{TaskKind, TaskTarget, TaskTrigger},
    queue::ResolvedInstallPlan,
    security::{
        signature,
        types::{SignatureInfo, SignatureStatus},
    },
    uninstall::{AppSource, DeleteMode, ItemCategory, UninstallChoice},
};

/// Everything a write needs from the running app.
pub struct Writes {
    pub app: tauri::AppHandle,
    pub core: Arc<DesktopCore>,
    pub audit: Audit,
}

/// A planned write, ready to show and execute.
enum Plan {
    Install {
        plan: Box<ResolvedInstallPlan>,
        name: String,
    },
    Uninstall {
        plan_id: String,
        app_id: String,
        item_ids: Vec<String>,
        path: String,
        name: String,
    },
    Clean {
        report_id: String,
        item_ids: Vec<String>,
        digest: String,
        view: Box<McpConfirmView>,
    },
}

/// The cleanup rule, applied to a selection that arrives with a report id.
///
/// The scan tool already left unreachable items out of its answer, but a report
/// id outlives one conversation: an older report (from before a rule change, or
/// from a scan the client only half read) is still selectable by id. So the rule
/// runs again here, on the report the grant will actually execute against.
fn plan_clean_selection(
    report: &crate::cleanup::types::CleanupReport,
    ids: &[String],
) -> Result<(), AppError> {
    let chosen = super::cleanup::check_selection(report, ids).map_err(selection_error)?;
    if chosen.is_empty() {
        return Err(AppError::new("E_INVALID_ARG", "item_ids"));
    }
    Ok(())
}

fn tool_error(error: AppError) -> ToolError {
    ToolError::new(error.code, error.message)
}

/// A selection the cleanup rule refuses, as a tool error. The message names no
/// path: the caller is told which id it may not use, not what is on disk.
fn selection_error(error: super::cleanup::SelectionError) -> AppError {
    use super::cleanup::SelectionError;
    match error {
        SelectionError::NotRemovable(id) => AppError::new(
            "E_NOT_REMOVABLE",
            format!(
                "clean_cache cannot remove item {id}: it is not a regenerable cache, or the scan could not measure it completely."
            ),
        ),
        SelectionError::Duplicate(id) => {
            AppError::new("E_INVALID_ARG", format!("item {id} is named twice"))
        }
        SelectionError::Empty => AppError::new("E_INVALID_ARG", "no items selected"),
    }
}

fn block_on<T>(future: impl std::future::Future<Output = T>) -> T {
    tauri::async_runtime::block_on(future)
}

impl Writes {
    pub fn run(&self, request: WriteRequest, context: WriteContext) -> Result<Value, ToolError> {
        self.audit.check().map_err(|_| audit_unavailable())?;
        let identity = self.identify(&context);
        // The entry exists before anything can fail, so every refusal is recorded with its reason.
        let mut entry = AuditEntry {
            id: uuid::Uuid::now_v7().to_string(),
            at: audit::now_ms(),
            tool: request.tool().to_owned(),
            object: request.object(),
            caller: identity.caller.clone(),
            decision: AuditDecision::Rejected,
            result: None,
        };
        let reject = |entry: &mut AuditEntry, decision, error: ToolError| {
            refuse(&self.audit, entry, decision, error)
        };
        let plan = match block_on(self.plan(&request)) {
            Ok(plan) => plan,
            Err(error) => return Err(reject(&mut entry, AuditDecision::Rejected, error)),
        };
        let view = confirm_view(&plan, &identity);
        let confirmation_request = ConfirmationRequest {
            caller: MCP_CALLER.into(),
            source: identity.caller.path.clone().unwrap_or_default(),
            action: request.tool().into(),
            target: request.object(),
            plan_digest: match &plan {
                Plan::Install { plan, .. } => Some(plan.digest()),
                Plan::Uninstall { plan_id, .. } => Some(plan_id.clone()),
                Plan::Clean { digest, .. } => Some(digest.clone()),
            },
        };
        let service = match self.confirmation() {
            Ok(service) => service,
            Err(error) => return Err(reject(&mut entry, AuditDecision::Rejected, error)),
        };
        // Planning can take a while; do not open a dialog for a request that is already void.
        if !(context.still_valid)() {
            return Err(reject(
                &mut entry,
                AuditDecision::Invalidated,
                invalidated(),
            ));
        }
        let decision =
            block_on(service.confirm_window(&self.app, confirmation_request.clone(), view));
        let grant = match decision {
            Ok(grant) => grant,
            Err(error) => {
                let (decision, error) = match error.code.as_str() {
                    "E_BUSY" => (
                        AuditDecision::Rejected,
                        ToolError::new("E_BUSY", BUSY_MESSAGE),
                    ),
                    "E_CONFIRMATION_INVALIDATED" => (AuditDecision::Invalidated, invalidated()),
                    "E_CONFIRMATION_TIMEOUT" => (
                        AuditDecision::TimedOut,
                        ToolError::new(
                            "E_CONFIRMATION_TIMEOUT",
                            "The user did not answer the confirmation in time.",
                        ),
                    ),
                    _ => (
                        AuditDecision::Denied,
                        ToolError::new(
                            "E_CONFIRMATION_DENIED",
                            "The user denied this request in OpenNavo.",
                        ),
                    ),
                };
                return Err(reject(&mut entry, decision, error));
            }
        };
        if let Err(error) = service.consume(&grant, &confirmation_request) {
            return Err(reject(
                &mut entry,
                AuditDecision::Invalidated,
                tool_error(error),
            ));
        }
        if !(context.still_valid)() {
            return Err(reject(
                &mut entry,
                AuditDecision::Invalidated,
                invalidated(),
            ));
        }
        entry.decision = AuditDecision::Allowed;
        self.audit
            .record(&entry, audit::now_ms())
            .map_err(|_| audit_unavailable())?;
        let outcome = block_on(self.execute(plan, &context));
        let result = match &outcome {
            Ok((label, _)) => label.clone(),
            Err(error) => error.code.clone(),
        };
        let _ = self
            .audit
            .record_result(&entry.id, &result, audit::now_ms());
        outcome.map(|(_, value)| value)
    }

    /// A switch was turned off: withdraw the open Local MCP dialog, if any.
    pub fn cancel(&self) {
        if let Ok(service) = self.confirmation() {
            service.revoke_caller(MCP_CALLER);
        }
    }

    fn confirmation(&self) -> Result<Arc<ConfirmationService>, ToolError> {
        use tauri::Manager;
        self.app
            .try_state::<Arc<ConfirmationService>>()
            .map(|state| state.inner().clone())
            .ok_or_else(|| ToolError::internal("confirmation service unavailable"))
    }

    async fn plan(&self, request: &WriteRequest) -> Result<Plan, ToolError> {
        match request {
            WriteRequest::Install { kind, token } => self.plan_install(kind, token).await,
            WriteRequest::Uninstall { path } => self.plan_uninstall(path).await,
            WriteRequest::Clean {
                report_id,
                item_ids,
            } => self.plan_clean(report_id, item_ids).await,
        }
    }

    /// Plan a cleanup. The engine builds the very view the user will see, and
    /// the digest of that selection travels with the grant, so the list in the
    /// dialog is the list that runs.
    async fn plan_clean(&self, report_id: &str, item_ids: &[String]) -> Result<Plan, ToolError> {
        let engine = self.core.queue.cleanup_engine.clone();
        let report_id = report_id.to_owned();
        let item_ids = item_ids.to_vec();
        let (digest, view) = {
            let engine = engine.clone();
            let report_id = report_id.clone();
            let item_ids = item_ids.clone();
            crate::blocking(move || {
                let entry = engine.cached(&report_id, crate::uninstall::now())?;
                // The same rule the scan tool applied, checked again here: a client
                // could have kept ids from an older report, or never seen this one.
                plan_clean_selection(&entry.report, &item_ids)?;
                engine.confirm_plan(&report_id, &item_ids)
            })
            .await
            .map_err(tool_error)?
        };
        Ok(Plan::Clean {
            report_id,
            item_ids,
            digest,
            view: Box::new(view),
        })
    }

    async fn plan_install(&self, kind: &str, token: &str) -> Result<Plan, ToolError> {
        let (catalog_kind, task_kind) = match kind {
            "cask" => (crate::model::Kind::Cask, TaskKind::Cask),
            "formula" => (crate::model::Kind::Formula, TaskKind::Formula),
            _ => return Err(ToolError::invalid("kind must be cask or formula")),
        };
        let core = self.core.clone();
        let owned = token.to_owned();
        let item =
            crate::blocking(move || crate::catalog::get(&core.database, catalog_kind, &owned))
                .await
                .map_err(tool_error)?
                .ok_or_else(|| {
                    ToolError::new(
                        "E_NOT_FOUND",
                        "No catalog app with that kind and token; use search_catalog first.",
                    )
                })?;
        if item.disabled {
            return Err(ToolError::new(
                "E_UNSUPPORTED",
                "This app is disabled in the catalog and cannot be installed.",
            ));
        }
        let target = TaskTarget {
            kind: task_kind,
            token: token.to_owned(),
        };
        let core = self.core.clone();
        let check = target.clone();
        let available = crate::blocking(move || {
            Ok(crate::commands::deeplink::target_available(
                &check,
                &core.library_list()?,
                &core.queue.active()?,
            ))
        })
        .await
        .map_err(tool_error)?;
        if !available {
            return Err(ToolError::new(
                "E_ALREADY_INSTALLED",
                "This app is already installed or already queued.",
            ));
        }
        crate::commands::deeplink::ensure_install_environment(&self.core)
            .await
            .map_err(tool_error)?;
        let core = self.core.clone();
        let plan = crate::blocking(move || {
            ResolvedInstallPlan::resolve(&core.database, target, Default::default())
        })
        .await
        .map_err(tool_error)?;
        let name = item_name(&item).unwrap_or_else(|| token.to_owned());
        Ok(Plan::Install {
            plan: Box::new(plan),
            name,
        })
    }

    async fn plan_uninstall(&self, path: &str) -> Result<Plan, ToolError> {
        let engine = self.core.queue.uninstall_engine.clone();
        let apps = crate::blocking(move || engine.list())
            .await
            .map_err(tool_error)?;
        let app = apps
            .into_iter()
            .find(|app| app.path.trim_end_matches('/') == path)
            .ok_or_else(|| {
                ToolError::new(
                    "E_NOT_FOUND",
                    "No installed app at that path; use list_installed_apps.",
                )
            })?;
        if app.running {
            return Err(app_running());
        }
        // The order matters: Homebrew before the generic protected check, so a
        // Homebrew app gets the reason that says what to do instead of a bare
        // "OpenNavo does not remove this app".
        uninstall_refusal(&app)?;
        let permissions = self.core.permissions().await.map_err(tool_error)?;
        let request_id = format!("mcp-{}", uuid::Uuid::new_v4());
        let plan = self
            .core
            .queue
            .uninstall_engine
            .plan(request_id, vec![app.id.clone()], permissions)
            .await
            .map_err(tool_error)?;
        let app_plan = plan
            .apps
            .iter()
            .find(|candidate| candidate.app_id == app.id)
            .ok_or_else(|| ToolError::internal("uninstall plan is missing the app"))?;
        if app_plan.running {
            return Err(app_running());
        }
        if app_plan.blocked_reason.is_some() {
            return Err(ToolError::new(
                "E_PROTECTED",
                "OpenNavo cannot remove this app right now.",
            ));
        }
        // Only the bundle itself: related files are never part of an MCP uninstall.
        let item_ids: Vec<String> = app_plan
            .items
            .iter()
            .filter(|item| {
                item.category == ItemCategory::App
                    && item.path.trim_end_matches('/') == app.path.trim_end_matches('/')
            })
            .map(|item| item.id.clone())
            .collect();
        if item_ids.len() != 1 {
            return Err(ToolError::new(
                "E_UNSUPPORTED",
                "OpenNavo could not isolate the app bundle for this app.",
            ));
        }
        Ok(Plan::Uninstall {
            plan_id: plan.plan_id,
            app_id: app.id,
            item_ids,
            path: app.path,
            name: app.name,
        })
    }

    async fn execute(
        &self,
        plan: Plan,
        context: &WriteContext,
    ) -> Result<(String, Value), ToolError> {
        match plan {
            Plan::Install { plan, .. } => {
                crate::commands::deeplink::ensure_install_environment(&self.core)
                    .await
                    .map_err(tool_error)?;
                // The environment check awaits; a switch may have gone off meanwhile.
                if !(context.still_valid)() {
                    return Err(invalidated());
                }
                let queue = self.core.queue.clone();
                let task = crate::blocking(move || {
                    queue.enqueue_resolved_install(*plan, TaskTrigger::Mcp)
                })
                .await
                .map_err(tool_error)?;
                Ok((
                    format!("queued:{}", task.id),
                    json!({ "status": "queued", "task": task }),
                ))
            }
            Plan::Uninstall {
                plan_id,
                app_id,
                item_ids,
                path,
                ..
            } => {
                let queue = self.core.queue.clone();
                let engine = queue.uninstall_engine.clone();
                let tasks = crate::blocking(move || {
                    engine.execute_choices_with(
                        &queue,
                        &plan_id,
                        vec![UninstallChoice {
                            app_id,
                            item_ids,
                            quit_running: false,
                        }],
                        DeleteMode::Trash,
                        true,
                    )
                })
                .await
                .map_err(tool_error)?;
                let task = tasks.into_iter().next();
                let label = task
                    .as_ref()
                    .map_or_else(|| "queued".to_owned(), |task| format!("queued:{}", task.id));
                Ok((
                    label,
                    json!({ "status": "queued", "movesToTrash": [path], "task": task }),
                ))
            }
            Plan::Clean {
                report_id,
                item_ids,
                digest,
                ..
            } => {
                if !(context.still_valid)() {
                    return Err(invalidated());
                }
                let queue = self.core.queue.clone();
                let engine = queue.cleanup_engine.clone();
                let task = crate::blocking(move || {
                    // The grant the user gave is the digest of the list the
                    // dialog showed; the engine refuses if the selection moved.
                    engine.enqueue_confirmed(&queue, &report_id, item_ids, &digest)
                })
                .await
                .map_err(tool_error)?;
                Ok((
                    format!("queued:{}", task.id),
                    json!({ "status": "queued", "task": task }),
                ))
            }
        }
    }

    fn identify(&self, context: &WriteContext) -> Identity {
        let process = caller::resolve(&SystemProcesses, context.peer);
        let signature = process
            .parent_path
            .as_deref()
            .and_then(|path| signature::inspect(path).ok());
        identity(process, signature, context.client_name.clone())
    }
}

/// Turn a resolved process and its signature into what the dialog and the audit
/// log are allowed to say.
///
/// A caller is "signed" only when all three hold: the process tree still matches
/// the stamped peer, the executable carries a signature macOS trusts, and that
/// signature names a Team ID. The third is what makes the claim checkable — the
/// dialog shows the Team ID next to the signer name, so a process cannot borrow
/// a name without also showing the team behind it. A signature with no Team ID
/// (an ad-hoc or a self-signed one) is not trusted.
fn identity(
    process: caller::CallerProcess,
    signature: Option<SignatureInfo>,
    claimed_name: Option<String>,
) -> Identity {
    // No resolved executable means the peer could not be named at all (a
    // recycled pid, an orphan, a platform that hides the parent). A signature
    // can only describe a path, so there is nothing to trust.
    let trusted = process.parent_path.is_some()
        && signature.as_ref().is_some_and(|info| {
            matches!(
                info.status,
                SignatureStatus::Apple | SignatureStatus::AppStore | SignatureStatus::DeveloperId
            ) && info.team_id.as_deref().is_some_and(|team| !team.is_empty())
        });
    let may_come_from = process.may_come_from.as_deref().map(caller::bundle_name);
    Identity {
        caller: AuditCaller {
            path: process
                .parent_path
                .as_ref()
                .map(|path| path.to_string_lossy().into_owned()),
            signer: signature
                .as_ref()
                .filter(|_| trusted)
                .and_then(|info| info.signer.clone()),
            team_id: signature
                .as_ref()
                .filter(|_| trusted)
                .and_then(|info| info.team_id.clone()),
            signed: trusted,
            may_come_from,
            claimed_name,
            pid: process.parent_pid,
        },
    }
}

struct Identity {
    caller: AuditCaller,
}

pub(crate) fn item_name(item: &crate::model::CatalogItem) -> Option<String> {
    serde_json::to_value(item)
        .ok()?
        .get("name")
        .and_then(Value::as_str)
        .filter(|name| !name.is_empty())
        .map(str::to_owned)
}

/// Record a write that ends without running, then hand back its error. The
/// write is refused either way, so a failed record changes nothing.
fn refuse(
    audit: &Audit,
    entry: &mut AuditEntry,
    decision: AuditDecision,
    error: ToolError,
) -> ToolError {
    entry.decision = decision;
    entry.result = Some(error.code.clone());
    let _ = audit.record(entry, audit::now_ms());
    error
}

/// Confirmation caller for every Local MCP write; switch-off revokes by it.
const MCP_CALLER: &str = "local mcp";

fn invalidated() -> ToolError {
    ToolError::new(
        "E_CONFIRMATION_INVALIDATED",
        "The request was withdrawn because the connection closed or Local MCP writes were turned off.",
    )
}

fn audit_unavailable() -> ToolError {
    ToolError::new(
        "E_AUDIT_UNAVAILABLE",
        "OpenNavo could not write its audit log, so it refused this request.",
    )
}

fn app_running() -> ToolError {
    ToolError::new(
        "E_APP_RUNNING",
        "The app is running. Ask the user to quit it, then try again.",
    )
}

/// Whether an installed app may be uninstalled through Local MCP at all, before
/// any plan is built. Kept apart from `plan_uninstall` so the order of the two
/// refusals is testable without an app inventory on disk.
fn uninstall_refusal(app: &crate::uninstall::LocalApp) -> Result<(), ToolError> {
    if app.source == AppSource::Homebrew {
        return Err(homebrew_managed(&app.name));
    }
    if !app.removable || app.protected_reason.is_some() {
        return Err(ToolError::new(
            "E_PROTECTED",
            "OpenNavo does not remove this app. Its Uninstall page can show why.",
        ));
    }
    Ok(())
}

/// A Homebrew-managed app is never uninstalled through Local MCP.
///
/// `brew uninstall` also removes that cask's dependencies and anything else the
/// formula owns, which reaches past the one app the confirmation dialog names.
/// The app's own Uninstall page can show the whole footprint; a tool call cannot,
/// so this stays closed. The message tells the client what to do next, and it
/// does not name the cask: that is Homebrew's bookkeeping, not the user's app.
fn homebrew_managed(name: &str) -> ToolError {
    let app = display_value(name);
    ToolError::new(
        "E_UNSUPPORTED",
        format!(
            "OpenNavo does not uninstall Homebrew-managed apps over Local MCP: Homebrew also removes that package's dependencies, which is more than this confirmation can show. Ask the user to uninstall {app} in OpenNavo's Uninstall page, which lists everything that would be removed."
        ),
    )
}

/// Content of the confirmation window. `McpConfirmView::new` filters every string for display.
fn confirm_view(plan: &Plan, identity: &Identity) -> McpConfirmView {
    let caller = &identity.caller;
    let signer = caller.signer.as_deref().filter(|_| caller.signed);
    match plan {
        // The cleanup engine builds its own list (paths, sizes, irreversibility),
        // the same one OpenNavo's own window shows. An AI app asked for it, so the
        // origin says `mcp` and the caller is added here: the window keeps the
        // file-list layout and still names who asked.
        Plan::Clean { view, .. } => McpConfirmView {
            origin: "mcp".into(),
            signer: signer.map(display_value),
            team_id: caller.team_id.as_deref().map(display_value),
            caller_path: caller.path.as_deref().map(display_value),
            may_come_from: caller.may_come_from.as_deref().map(display_value),
            claimed_name: caller.claimed_name.as_deref().map(display_value),
            ..view.as_ref().clone()
        },
        Plan::Install { plan, name } => {
            let target = plan.command_target();
            McpConfirmView::new(
                "install",
                name,
                &format!("{}/{}", target.kind.as_str(), target.token),
                None,
                signer,
                caller.team_id.as_deref(),
                caller.path.as_deref(),
                caller.may_come_from.as_deref(),
                caller.claimed_name.as_deref(),
            )
        }
        Plan::Uninstall { path, name, .. } => McpConfirmView::new(
            "uninstall",
            name,
            path,
            Some(path),
            signer,
            caller.team_id.as_deref(),
            caller.path.as_deref(),
            caller.may_come_from.as_deref(),
            caller.claimed_name.as_deref(),
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::uninstall::ProtectedReason;

    fn caller(signed: bool) -> AuditCaller {
        AuditCaller {
            path: Some("/opt/homebrew/bin/node".into()),
            signer: signed.then(|| "Developer ID Application: Example Inc (ABCDE12345)".into()),
            team_id: signed.then(|| "ABCDE12345".into()),
            signed,
            may_come_from: Some("Cursor".into()),
            claimed_name: Some("cursor-vscode".into()),
            pid: Some(42),
        }
    }

    fn process(path: &str) -> caller::CallerProcess {
        caller::CallerProcess {
            parent_pid: Some(42),
            parent_path: Some(std::path::PathBuf::from(path)),
            may_come_from: None,
        }
    }

    fn signed_as(status: SignatureStatus, team: Option<&str>) -> SignatureInfo {
        SignatureInfo {
            status,
            team_id: team.map(str::to_owned),
            signer: Some("Developer ID Application: Example Inc".into()),
            notarized: None,
        }
    }

    #[test]
    fn a_signature_without_a_team_id_is_not_a_trusted_caller() {
        // The point of showing a Team ID is that the name can be checked against
        // it. A signature that names no team cannot be checked, so the dialog
        // must not present it as a verified signer.
        for status in [
            SignatureStatus::DeveloperId,
            SignatureStatus::Apple,
            SignatureStatus::AppStore,
        ] {
            let anonymous = identity(
                process("/opt/homebrew/bin/node"),
                Some(signed_as(status, None)),
                None,
            );
            assert!(!anonymous.caller.signed, "{status:?} without a team");
            assert_eq!(anonymous.caller.team_id, None);
            assert_eq!(anonymous.caller.signer, None);

            let empty = identity(
                process("/opt/homebrew/bin/node"),
                Some(signed_as(status, Some(""))),
                None,
            );
            assert!(!empty.caller.signed, "{status:?} with an empty team");
            assert_eq!(empty.caller.team_id, None, "an empty team is not shown");
        }
        let named = identity(
            process("/opt/homebrew/bin/node"),
            Some(signed_as(SignatureStatus::DeveloperId, Some("ABCDE12345"))),
            None,
        );
        assert!(named.caller.signed);
        assert_eq!(named.caller.team_id.as_deref(), Some("ABCDE12345"));
    }

    #[test]
    fn an_untrusted_status_is_not_a_trusted_caller_even_with_a_team_id() {
        for status in [
            SignatureStatus::Adhoc,
            SignatureStatus::Modified,
            SignatureStatus::Invalid,
            SignatureStatus::Unknown,
            SignatureStatus::Unsigned,
            SignatureStatus::NotCode,
        ] {
            let untrusted = identity(
                process("/opt/homebrew/bin/node"),
                Some(signed_as(status, Some("ABCDE12345"))),
                None,
            );
            assert!(!untrusted.caller.signed, "{status:?} must not be trusted");
            assert_eq!(untrusted.caller.team_id, None);
        }
        // No signature at all is the same answer, not a crash.
        let missing = identity(process("/opt/homebrew/bin/node"), None, None);
        assert!(!missing.caller.signed);
        assert_eq!(missing.caller.team_id, None);
    }

    #[test]
    fn a_peer_the_tree_could_not_resolve_yields_no_caller() {
        // A recycled pid, an orphan, or a platform that hides the parent: the
        // caller is unknown, so nothing may be claimed about who it is.
        let unknown = identity(
            caller::CallerProcess::default(),
            Some(signed_as(SignatureStatus::DeveloperId, Some("ABCDE12345"))),
            Some("claude-ai".into()),
        );
        assert!(!unknown.caller.signed);
        assert_eq!(unknown.caller.path, None);
        assert_eq!(unknown.caller.team_id, None);
        assert_eq!(unknown.caller.pid, None);
        // The self-reported name is still recorded, but only as a claim.
        assert_eq!(unknown.caller.claimed_name.as_deref(), Some("claude-ai"));
    }

    #[test]
    fn refusals_are_recorded_with_decision_and_result() {
        let dir = tempfile::tempdir().expect("tempdir");
        let audit = Audit::new(dir.path());
        let mut entry = AuditEntry {
            id: "a".into(),
            at: audit::now_ms(),
            tool: "install_app".into(),
            object: "cask/iina".into(),
            caller: caller(false),
            decision: AuditDecision::Allowed,
            result: None,
        };
        let error = refuse(
            &audit,
            &mut entry,
            AuditDecision::Rejected,
            ToolError::new("E_BUSY", BUSY_MESSAGE),
        );
        assert_eq!(error.code, "E_BUSY");
        let listed = audit.list(audit::now_ms()).expect("list");
        assert_eq!(listed.len(), 1);
        assert_eq!(listed[0].decision, AuditDecision::Rejected);
        assert_eq!(listed[0].result.as_deref(), Some("E_BUSY"));
    }

    #[test]
    fn the_window_shows_the_signer_only_when_signed_and_never_the_pid() {
        let identity = |signed| Identity {
            caller: caller(signed),
        };
        let plan = Plan::Uninstall {
            plan_id: "p".into(),
            app_id: "a".into(),
            item_ids: vec!["i".into()],
            path: "/Applications/IINA.app".into(),
            name: "IINA\u{202E}".into(),
        };
        let signed = confirm_view(&plan, &identity(true));
        assert_eq!(signed.action, "uninstall");
        assert_eq!(signed.name, "IINA");
        assert_eq!(signed.path.as_deref(), Some("/Applications/IINA.app"));
        assert!(
            signed
                .signer
                .as_deref()
                .is_some_and(|s| s.contains("Example Inc"))
        );
        assert_eq!(signed.team_id.as_deref(), Some("ABCDE12345"));
        assert_eq!(signed.may_come_from.as_deref(), Some("Cursor"));
        assert_eq!(signed.claimed_name.as_deref(), Some("cursor-vscode"));
        let unsigned = confirm_view(&plan, &identity(false));
        assert_eq!(unsigned.signer, None);
        assert_eq!(
            unsigned.team_id, None,
            "an unsigned caller has no team to show"
        );
        assert_eq!(
            unsigned.caller_path.as_deref(),
            Some("/opt/homebrew/bin/node")
        );
        let json = serde_json::to_string(&unsigned).expect("json");
        assert!(!json.contains("\"pid\""));
    }

    #[test]
    fn a_cleanup_asked_for_over_mcp_still_names_who_asked() {
        // The engine builds the confirmation as OpenNavo's own request (`origin:
        // "app"`), with no caller fields. Shown unchanged, an AI app's cleanup
        // would look like something OpenNavo decided to do by itself.
        let report = cleanup_report();
        // Exactly what `Engine::confirm_plan` builds: OpenNavo's own layout.
        let engine_view = McpConfirmView::app_action(
            "cleanup",
            "",
            vec![crate::confirmation::ConfirmItemView::row(
                "g",
                "/Users/me/Library/Caches/cache",
            )],
            1,
            Some(1024),
        );
        assert_eq!(engine_view.origin, "app");
        let plan = Plan::Clean {
            report_id: report.report_id.clone(),
            item_ids: vec!["cache".into()],
            digest: "d".into(),
            view: Box::new(engine_view),
        };
        let view = confirm_view(
            &plan,
            &Identity {
                caller: caller(true),
            },
        );
        assert_eq!(view.origin, "mcp", "an AI app asked for this cleanup");
        assert!(
            view.signer
                .as_deref()
                .is_some_and(|s| s.contains("Example Inc")),
            "the window must still name the caller"
        );
        assert_eq!(view.team_id.as_deref(), Some("ABCDE12345"));
        assert_eq!(view.action, "cleanup");
    }

    #[test]
    fn a_homebrew_app_is_refused_with_a_reason_the_client_can_act_on() {
        // Homebrew uninstalls reach past the one app the dialog names, so this
        // stays closed. What matters is that the refusal says so and points the
        // client at the app's own Uninstall page instead of a bare "unsupported".
        let app = uninstall_app(AppSource::Homebrew, true, None);
        let error = uninstall_refusal(&app).expect_err("Homebrew stays closed");
        assert_eq!(error.code, "E_UNSUPPORTED");
        assert!(error.message.contains("Homebrew"), "{}", error.message);
        assert!(
            error.message.contains("Uninstall page"),
            "the client must be told where the user can do this: {}",
            error.message
        );
        assert!(
            error.message.contains("Firefox"),
            "the message names the app so the client can relay it: {}",
            error.message
        );
    }

    #[test]
    fn a_homebrew_app_gets_the_homebrew_reason_not_the_generic_one() {
        // A Homebrew app is also `removeable == false` in some inventories; the
        // specific reason has to win, or the client is told nothing useful.
        let app = uninstall_app(AppSource::Homebrew, false, Some(ProtectedReason::System));
        assert_eq!(
            uninstall_refusal(&app).expect_err("refused").code,
            "E_UNSUPPORTED"
        );
        let protected = uninstall_app(AppSource::Direct, false, Some(ProtectedReason::System));
        assert_eq!(
            uninstall_refusal(&protected).expect_err("refused").code,
            "E_PROTECTED"
        );
        assert!(uninstall_refusal(&uninstall_app(AppSource::Direct, true, None)).is_ok());
    }

    #[test]
    fn a_homebrew_refusal_filters_the_app_name_it_echoes() {
        // The name comes from the bundle on disk, not from the client, but it is
        // still echoed into the tool result: bidi controls must not survive.
        let app = uninstall_app(AppSource::Homebrew, true, None);
        let mut named = app.clone();
        named.name = "Fire\u{202E}fox".into();
        let error = uninstall_refusal(&named).expect_err("refused");
        assert!(!error.message.contains('\u{202E}'), "{}", error.message);
    }

    fn uninstall_app(
        source: AppSource,
        removable: bool,
        protected_reason: Option<ProtectedReason>,
    ) -> crate::uninstall::LocalApp {
        let mut app: crate::uninstall::LocalApp = serde_json::from_value(serde_json::json!({
            "id": "one",
            "path": "/Applications/Firefox.app",
            "name": "Firefox",
            "source": "direct",
            "running": false,
            "removable": true
        }))
        .expect("fixture");
        app.source = source;
        app.removable = removable;
        app.protected_reason = protected_reason;
        app
    }

    fn cleanup_report() -> crate::cleanup::types::CleanupReport {
        use crate::cleanup::types::{
            CategoryReport, CleanupCategory, CleanupGroup, CleanupItem, CleanupItemKind,
            CleanupReason, CleanupRisk, CleanupVolume,
        };
        let item = |id: &str, reason, mode| CleanupItem {
            id: id.into(),
            path: format!("/Users/me/Library/Caches/{id}"),
            kind: CleanupItemKind::Dir,
            command: None,
            size_bytes: Some(1024),
            size_complete: true,
            kept_open_files: 0,
            modified_at: None,
            reason,
            selected: false,
            delete_mode: mode,
            needs_admin: false,
            note: None,
        };
        let category = |key, risk, items| CategoryReport {
            key,
            risk,
            bytes: 1024,
            complete: true,
            groups: vec![CleanupGroup {
                id: "g".into(),
                title: "g".into(),
                subtitle: None,
                app_id: None,
                bundle_id: None,
                items,
            }],
            skipped: vec![],
        };
        crate::cleanup::types::CleanupReport {
            report_id: "r".into(),
            scanned_at: 0,
            expires_at: i64::MAX,
            volume: CleanupVolume {
                name: "synthetic".into(),
                total_bytes: 1,
                free_bytes: 0,
            },
            categories: vec![
                category(
                    CleanupCategory::AppCaches,
                    CleanupRisk::Rebuildable,
                    vec![item("cache", CleanupReason::CacheDir, DeleteMode::Trash)],
                ),
                category(
                    CleanupCategory::Trash,
                    CleanupRisk::Review,
                    vec![item("trash", CleanupReason::Trash, DeleteMode::Trash)],
                ),
            ],
            incomplete: vec![],
        }
    }

    #[test]
    fn a_cleanup_selection_is_checked_against_the_report_that_will_execute() {
        // The scan tool hides unreachable items, but a report id outlives the
        // conversation: a client can name an id it never saw. The rule must run
        // again here, against the same report the grant is bound to.
        let report = cleanup_report();
        assert!(plan_clean_selection(&report, &["cache".into()]).is_ok());
        assert_eq!(
            plan_clean_selection(&report, &["trash".into()])
                .unwrap_err()
                .code,
            "E_NOT_REMOVABLE"
        );
        assert_eq!(
            plan_clean_selection(&report, &["cache".into(), "cache".into()])
                .unwrap_err()
                .code,
            "E_INVALID_ARG"
        );
        assert_eq!(
            plan_clean_selection(&report, &[]).unwrap_err().code,
            "E_INVALID_ARG"
        );
    }
}
