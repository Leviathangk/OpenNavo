//! Native confirmation gate for deep-link-initiated installs (and future MCP callers).
//!
//! Requests made by the app's own UI do not pass through this gate. The service deliberately keeps
//! the policy state separate from the dialog adapter. That makes the one-dialog rule and grant
//! lifecycle testable without creating an AppKit window in a libtest worker, while confirm always
//! delegates rendering to the dialog plugin, which marshals the dialog to Tauri's main thread on
//! desktop.

use crate::{AppError, model::Locale, native_i18n};
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    sync::Mutex,
    time::{Duration, Instant},
};
use tauri::{Manager, Runtime};
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tokio::sync::oneshot;

const DIALOG_TIMEOUT: Duration = Duration::from_secs(60);
const GRANT_TTL: Duration = Duration::from_secs(30);

// tauri-plugin-dialog's async `MessageDialogBuilder::show` returns no dialog handle (rfd exposes only `show`), so
// a timeout cannot programmatically dismiss the native dialog. A timeout revokes that confirmation's grants but
// keeps the single slot occupied until the native callback arrives; a late click only releases the slot and never
// produces a grant.

/// A deep-link install request presented to the user. Install callers bind an immutable resolved
/// execution plan; future non-UI callers may bind their own plan digest.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfirmationRequest {
    pub caller: String,
    pub source: String,
    pub action: String,
    pub target: String,
    pub plan_digest: Option<String>,
}

/// One row in a collection confirmation. The native dialog owns the complete list so the user approves the
/// same ordered collection that the guarded command will enqueue.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfirmationListItem {
    pub name: String,
    pub kind: String,
    pub token: String,
    pub tap: Option<String>,
    pub disabled: bool,
    pub installed: bool,
    pub already_queued: bool,
    pub unsupported: bool,
    pub duplicate: bool,
}

impl ConfirmationRequest {
    pub fn digest(&self) -> String {
        // A tuple gives us a stable, unambiguous canonical representation without relying on map ordering.
        let canonical = serde_json::to_vec(&(
            &self.caller,
            &self.source,
            &self.action,
            &self.target,
            &self.plan_digest,
        ))
        .expect("confirmation request fields are serializable");
        format!("{:x}", Sha256::digest(canonical))
    }

    fn message(&self, locale: Locale) -> Result<String, AppError> {
        let caller = if self.caller == "deep link" {
            text(locale, "deepLink")?
        } else {
            self.caller.clone()
        };
        let action = match self.action.as_str() {
            "install" => text(locale, "install")?,
            "install collection" => text(locale, "installCollection")?,
            _ => self.action.clone(),
        };
        Ok(text(locale, "request")?
            .replace("{caller}", &display_value(&caller))
            .replace("{source}", &display_value(&self.source))
            .replace("{action}", &display_value(&action))
            .replace("{target}", &display_value(&self.target)))
    }

    fn collection_message(
        &self,
        items: &[ConfirmationListItem],
        install_count: usize,
        locale: Locale,
    ) -> Result<String, AppError> {
        let skip_count = items
            .len()
            .checked_sub(install_count)
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "collection_counts"))?;
        let mut message = self.message(locale)?;
        message.push_str("\n\n");
        message.push_str(
            &text(locale, "collection")?
                .replace("{install}", &install_count.to_string())
                .replace("{skip}", &skip_count.to_string()),
        );
        for item in items {
            message.push_str("• ");
            message.push_str(&display_value(&item.name));
            message.push_str(&format!(
                " [{}/{}]",
                display_value(&item.kind),
                display_value(&item.token)
            ));
            if let Some(tap) = &item.tap {
                message.push_str(&format!(" [{}]", display_value(tap)));
            }
            let status = if item.disabled {
                Some("disabled")
            } else if item.installed {
                Some("installed")
            } else if item.already_queued {
                Some("queued")
            } else if item.duplicate {
                Some("duplicate")
            } else if item.unsupported {
                Some("unsupported")
            } else {
                None
            };
            if let Some(status) = status {
                message.push_str(&format!(" ({})", text(locale, status)?));
            }
            message.push('\n');
        }
        message.push('\n');
        message.push_str(&text(locale, "footer")?);
        Ok(message)
    }
}

fn text(locale: Locale, key: &str) -> Result<String, AppError> {
    native_i18n::text(locale, &format!("confirmation.{key}"))
}

fn app_locale<R: Runtime>(app: &tauri::AppHandle<R>) -> Locale {
    app.try_state::<std::sync::Arc<crate::core::DesktopCore>>()
        .and_then(|core| core.current_settings().ok())
        .map_or(Locale::EnUs, |settings| settings.locale)
}

/// A grant is intentionally opaque to callers. It can only be consumed through this service, and consumption
/// removes it before checking the supplied digest so a mismatched attempt cannot be retried or used as an oracle.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfirmationGrant {
    token: String,
    confirmation_id: u64,
    digest: String,
    plan_digest: Option<String>,
    expires_at: Instant,
}

#[derive(Debug)]
struct ActiveConfirmation {
    id: u64,
    request: ConfirmationRequest,
    digest: String,
    timed_out: bool,
    _cancel: oneshot::Sender<()>,
}

#[derive(Default)]
struct State {
    next_id: u64,
    active: Option<ActiveConfirmation>,
    grants: HashMap<String, ConfirmationGrant>,
}

/// Single-slot native confirmation service. Keep this in app state so the later MCP broker can reuse the same
/// gate instead of adding another dialog or grant implementation.
#[derive(Default)]
pub struct ConfirmationService {
    state: Mutex<State>,
}

#[derive(Debug)]
struct PendingConfirmation {
    id: u64,
    request: ConfirmationRequest,
    cancelled: oneshot::Receiver<()>,
}

/// Clears the single active slot when the command future is cancelled (for example when its webview is
/// destroyed). Native dialog callbacks can arrive later; `finish` checks the id and ignores those stale clicks.
struct ConfirmationDropGuard {
    service: std::sync::Arc<ConfirmationService>,
    id: u64,
}

impl ConfirmationDropGuard {
    fn new(service: std::sync::Arc<ConfirmationService>, id: u64) -> Self {
        Self { service, id }
    }
}

impl Drop for ConfirmationDropGuard {
    fn drop(&mut self) {
        self.service.expire(self.id);
    }
}

impl ConfirmationService {
    /// Show a native dialog and return a grant only after the user explicitly allows it.
    pub async fn confirm<R: Runtime>(
        self: &std::sync::Arc<Self>,
        app: &tauri::AppHandle<R>,
        request: ConfirmationRequest,
    ) -> Result<ConfirmationGrant, AppError> {
        let locale = app_locale(app);
        let pending = self.begin(request)?;
        let (sender, receiver) = oneshot::channel();
        let service = std::sync::Arc::clone(self);
        let dialog_id = pending.id;
        let guard = ConfirmationDropGuard::new(std::sync::Arc::clone(self), dialog_id);
        app.dialog()
            .message(pending.request.message(locale)?)
            .title(text(locale, "title")?)
            .kind(MessageDialogKind::Warning)
            .buttons(MessageDialogButtons::OkCancelCustom(
                text(locale, "allow")?,
                text(locale, "deny")?,
            ))
            // tauri-plugin-dialog's desktop adapter calls run_on_main_thread before showing AppKit/rfd.
            .show(move |allowed| {
                service.finish_reply(dialog_id, allowed, |result| {
                    let _ = sender.send(result);
                });
            });

        self.wait_for_reply(pending, receiver, guard).await
    }

    /// Show one native dialog for a collection. The request's plan digest binds the displayed plan to the
    /// ordered execution plans that the caller enqueues after the grant is consumed.
    pub async fn confirm_collection<R: Runtime>(
        self: &std::sync::Arc<Self>,
        app: &tauri::AppHandle<R>,
        request: ConfirmationRequest,
        items: Vec<ConfirmationListItem>,
        install_count: usize,
    ) -> Result<ConfirmationGrant, AppError> {
        let locale = app_locale(app);
        let pending = self.begin(request.clone())?;
        let (sender, receiver) = oneshot::channel();
        let service = std::sync::Arc::clone(self);
        let dialog_id = pending.id;
        let guard = ConfirmationDropGuard::new(std::sync::Arc::clone(self), dialog_id);
        app.dialog()
            .message(request.collection_message(&items, install_count, locale)?)
            .title(text(locale, "title")?)
            .kind(MessageDialogKind::Warning)
            .buttons(MessageDialogButtons::OkCancelCustom(
                text(locale, "installAll")?,
                text(locale, "cancel")?,
            ))
            .show(move |allowed| {
                service.finish_reply(dialog_id, allowed, |result| {
                    let _ = sender.send(result);
                });
            });

        self.wait_for_reply(pending, receiver, guard).await
    }

    async fn wait_for_reply(
        &self,
        pending: PendingConfirmation,
        receiver: oneshot::Receiver<Result<ConfirmationGrant, AppError>>,
        guard: ConfirmationDropGuard,
    ) -> Result<ConfirmationGrant, AppError> {
        self.wait_for_reply_within(pending, receiver, guard, DIALOG_TIMEOUT)
            .await
    }

    async fn wait_for_reply_within(
        &self,
        pending: PendingConfirmation,
        receiver: oneshot::Receiver<Result<ConfirmationGrant, AppError>>,
        _guard: ConfirmationDropGuard,
        timeout: Duration,
    ) -> Result<ConfirmationGrant, AppError> {
        let id = pending.id;
        tokio::select! {
            biased;
            result = tokio::time::timeout(timeout, receiver) => match result {
                Ok(Ok(result)) => result,
                Ok(Err(_)) => Err(AppError::new("E_CONFIRMATION_DENIED", "confirmation_denied")),
                Err(_) => {
                    self.timeout(id);
                    Err(AppError::new("E_CONFIRMATION_TIMEOUT", "confirmation_timeout"))
                }
            },
            _ = pending.cancelled => Err(AppError::new("E_CONFIRMATION_DENIED", "confirmation_denied")),
        }
    }

    /// A destroyed webview cannot keep a command waiting on a native callback. Clearing the slot also
    /// drops timed-out confirmations on purpose: once the window is gone, any later native interaction
    /// is invalid.
    pub fn cancel_active(&self) {
        if let Ok(mut state) = self.state.lock() {
            state.active = None;
            state.grants.clear();
        }
    }

    fn begin(&self, request: ConfirmationRequest) -> Result<PendingConfirmation, AppError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "confirmation_state"))?;
        Self::prune_grants(&mut state);
        if state.active.is_some() {
            return Err(AppError::new("E_BUSY", "confirmation_busy"));
        }
        state.next_id = state.next_id.wrapping_add(1);
        let digest = request.digest();
        let (cancel, cancelled) = oneshot::channel();
        let pending = PendingConfirmation {
            id: state.next_id,
            request: request.clone(),
            cancelled,
        };
        state.active = Some(ActiveConfirmation {
            id: pending.id,
            request,
            digest,
            timed_out: false,
            _cancel: cancel,
        });
        Ok(pending)
    }

    fn finish_reply(
        &self,
        id: u64,
        allowed: bool,
        reply: impl FnOnce(Result<ConfirmationGrant, AppError>),
    ) {
        let Ok(mut state) = self.state.lock() else {
            reply(Err(AppError::new("E_UNKNOWN", "confirmation_state")));
            return;
        };
        if state.active.as_ref().is_none_or(|active| active.id != id) {
            drop(state);
            reply(Err(AppError::new(
                "E_CONFIRMATION_TIMEOUT",
                "confirmation_expired",
            )));
            return;
        }
        let active = state
            .active
            .take()
            .expect("active confirmation id was checked above");
        let result = if active.timed_out {
            Err(AppError::new(
                "E_CONFIRMATION_TIMEOUT",
                "confirmation_timeout",
            ))
        } else if !allowed {
            Err(AppError::new(
                "E_CONFIRMATION_DENIED",
                "confirmation_denied",
            ))
        } else {
            let grant = ConfirmationGrant {
                token: uuid::Uuid::new_v4().to_string(),
                confirmation_id: active.id,
                digest: active.digest.clone(),
                plan_digest: active.request.plan_digest.clone(),
                expires_at: Instant::now() + GRANT_TTL,
            };
            state.grants.insert(grant.token.clone(), grant.clone());
            Ok(grant)
        };
        drop(state);
        // Keep _cancel alive through delivery. Even if the waiting future is polled between these
        // statements, cancellation cannot win against an allow that has not been sent yet.
        reply(result);
        drop(active);
    }

    /// A timed-out confirmation keeps the single slot until its native callback releases it, but its
    /// already-issued grants must not stay usable.
    fn timeout(&self, id: u64) {
        if let Ok(mut state) = self.state.lock() {
            if let Some(active) = state.active.as_mut()
                && active.id == id
            {
                active.timed_out = true;
            }
            state.grants.retain(|_, grant| grant.confirmation_id != id);
        }
    }

    fn expire(&self, id: u64) {
        if let Ok(mut state) = self.state.lock()
            && state
                .active
                .as_ref()
                .is_some_and(|active| active.id == id && !active.timed_out)
        {
            state.active = None;
        }
    }

    /// Consume a grant exactly once. The request and plan digest must both match the values approved by the user.
    pub fn consume(
        &self,
        grant: &ConfirmationGrant,
        request: &ConfirmationRequest,
    ) -> Result<(), AppError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "confirmation_state"))?;
        let Some(stored) = state.grants.remove(&grant.token) else {
            return Err(AppError::new(
                "E_CONFIRMATION_REUSED",
                "confirmation_reused",
            ));
        };
        if stored.expires_at <= Instant::now() {
            return Err(AppError::new(
                "E_CONFIRMATION_EXPIRED",
                "confirmation_expired",
            ));
        }
        Self::prune_grants(&mut state);
        if stored.digest != request.digest()
            || stored.digest != grant.digest
            || stored.plan_digest != request.plan_digest
        {
            return Err(AppError::new(
                "E_CONFIRMATION_MISMATCH",
                "confirmation_mismatch",
            ));
        }
        Ok(())
    }

    fn prune_grants(state: &mut State) {
        let now = Instant::now();
        state.grants.retain(|_, grant| grant.expires_at > now);
    }

    #[cfg(test)]
    fn test_begin(&self, request: ConfirmationRequest) -> Result<PendingConfirmation, AppError> {
        self.begin(request)
    }

    #[cfg(test)]
    fn test_finish(&self, id: u64, allowed: bool) -> Result<ConfirmationGrant, AppError> {
        let mut result = None;
        self.finish_reply(id, allowed, |reply| result = Some(reply));
        result.expect("terminal reply")
    }

    #[cfg(test)]
    fn test_expire(&self, id: u64) {
        self.expire(id)
    }

    #[cfg(test)]
    fn test_timeout(&self, id: u64) {
        self.timeout(id)
    }

    #[cfg(test)]
    fn test_issue_expired(&self, request: ConfirmationRequest) -> ConfirmationGrant {
        let pending = self.begin(request).expect("test confirmation starts");
        let mut grant = self
            .test_finish(pending.id, true)
            .expect("test confirmation grants");
        grant.expires_at = Instant::now() - Duration::from_secs(1);
        if let Ok(mut state) = self.state.lock() {
            state.grants.insert(grant.token.clone(), grant.clone());
        }
        grant
    }

    #[cfg(test)]
    async fn test_waiting(self: std::sync::Arc<Self>, request: ConfirmationRequest) {
        let pending = self.begin(request).expect("test confirmation starts");
        let guard = ConfirmationDropGuard::new(self.clone(), pending.id);
        let (_sender, receiver) = oneshot::channel();
        let _ = self.wait_for_reply(pending, receiver, guard).await;
    }
}

fn display_value(value: &str) -> String {
    const MAX_DISPLAY_CHARS: usize = 512;
    let mut chars = value.chars().filter(|character| {
        !character.is_control()
            && !matches!(
                *character,
                '\u{202A}'..='\u{202E}'
                    | '\u{2066}'..='\u{2069}'
                    | '\u{200E}'
                    | '\u{200F}'
                    | '\u{061C}'
            )
    });
    let mut display: String = chars.by_ref().take(MAX_DISPLAY_CHARS).collect();
    if chars.next().is_some() {
        display = display.chars().take(MAX_DISPLAY_CHARS - 1).collect();
        display.push('…');
    }
    if display.is_empty() {
        "(unknown)".into()
    } else {
        display
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn request(target: &str) -> ConfirmationRequest {
        ConfirmationRequest {
            caller: "deep link".into(),
            source: "opennavo://package/cask/ghostty?action=install".into(),
            action: "install".into(),
            target: target.into(),
            plan_digest: None,
        }
    }

    async fn wait_until_timeout(
        service: &std::sync::Arc<ConfirmationService>,
        pending: PendingConfirmation,
        timeout: Duration,
    ) -> Result<ConfirmationGrant, AppError> {
        let guard = ConfirmationDropGuard::new(std::sync::Arc::clone(service), pending.id);
        let (_sender, receiver) = oneshot::channel();
        service
            .wait_for_reply_within(pending, receiver, guard, timeout)
            .await
    }

    #[test]
    fn only_one_dialog_slot_is_available() {
        let service = ConfirmationService::default();
        let first = service.test_begin(request("cask/ghostty")).unwrap();
        assert_eq!(
            service.test_begin(request("cask/zed")).unwrap_err().code,
            "E_BUSY"
        );
        service.test_expire(first.id);
        assert!(service.test_begin(request("cask/zed")).is_ok());
    }

    #[test]
    fn native_prompt_contains_caller_source_action_and_target() {
        let prompt = request("cask/ghostty").message(Locale::EnUs).unwrap();
        for value in [
            "Caller: deep link",
            "Source: opennavo://package/cask/ghostty?action=install",
            "Action: install",
            "Target: cask/ghostty",
        ] {
            assert!(prompt.contains(value), "missing {value}");
        }
    }

    #[test]
    fn stale_dialog_callback_does_not_clear_a_new_confirmation() {
        let service = ConfirmationService::default();
        let first = service.test_begin(request("cask/ghostty")).unwrap();
        service.test_expire(first.id);
        let second = service.test_begin(request("cask/zed")).unwrap();

        assert_eq!(
            service.test_finish(first.id, true).unwrap_err().code,
            "E_CONFIRMATION_TIMEOUT"
        );
        assert!(service.test_finish(second.id, true).is_ok());
    }

    #[test]
    fn collection_prompt_counts_planned_installs_and_marks_every_skip_reason() {
        let request = request("collection/dev-setup");
        let message = request
            .collection_message(
                &[
                    ConfirmationListItem {
                        name: "Ghostty".into(),
                        kind: "cask".into(),
                        token: "fixture".into(),
                        tap: None,
                        disabled: false,
                        installed: false,
                        already_queued: false,
                        unsupported: false,
                        duplicate: false,
                    },
                    ConfirmationListItem {
                        name: "Legacy app".into(),
                        kind: "cask".into(),
                        token: "fixture".into(),
                        tap: None,
                        disabled: true,
                        installed: false,
                        already_queued: false,
                        unsupported: false,
                        duplicate: false,
                    },
                    ConfirmationListItem {
                        name: "Installed app".into(),
                        kind: "cask".into(),
                        token: "fixture".into(),
                        tap: None,
                        disabled: false,
                        installed: true,
                        already_queued: false,
                        unsupported: false,
                        duplicate: false,
                    },
                    ConfirmationListItem {
                        name: "Queued app".into(),
                        kind: "cask".into(),
                        token: "fixture".into(),
                        tap: None,
                        disabled: false,
                        installed: false,
                        already_queued: true,
                        unsupported: false,
                        duplicate: false,
                    },
                    ConfirmationListItem {
                        name: "Duplicate app".into(),
                        kind: "cask".into(),
                        token: "fixture".into(),
                        tap: None,
                        disabled: false,
                        installed: false,
                        already_queued: false,
                        unsupported: false,
                        duplicate: true,
                    },
                    ConfirmationListItem {
                        name: "Formula".into(),
                        kind: "formula".into(),
                        token: "ripgrep".into(),
                        tap: None,
                        disabled: false,
                        installed: false,
                        already_queued: false,
                        unsupported: true,
                        duplicate: false,
                    },
                ],
                1,
                Locale::EnUs,
            )
            .unwrap();
        assert!(message.contains("will install 1, skip 5"));
        assert!(message.contains("Ghostty"));
        assert!(message.contains("Legacy app [cask/fixture] (disabled)"));
        assert!(message.contains("Installed app [cask/fixture] (already installed)"));
        assert!(message.contains("Queued app [cask/fixture] (already queued)"));
        assert!(message.contains("Duplicate app [cask/fixture] (duplicate)"));
        assert!(message.contains("Formula [formula/ripgrep] (not a cask)"));
    }

    #[test]
    fn collection_prompt_handles_zero_and_all_installable_targets() {
        let request = request("collection/dev-setup");
        let item = ConfirmationListItem {
            name: "Ghostty".into(),
            kind: "cask".into(),
            token: "fixture".into(),
            tap: None,
            disabled: false,
            installed: false,
            already_queued: false,
            unsupported: false,
            duplicate: false,
        };
        for (items, install_count, summary) in [
            (vec![], 0, "will install 0, skip 0"),
            (vec![item.clone(); 2], 2, "will install 2, skip 0"),
            (
                vec![ConfirmationListItem {
                    installed: true,
                    ..item
                }],
                0,
                "will install 0, skip 1",
            ),
        ] {
            let message = request
                .collection_message(&items, install_count, Locale::EnUs)
                .unwrap();
            assert!(message.contains(summary), "{message}");
        }
        assert!(request.collection_message(&[], 1, Locale::EnUs).is_err());
    }

    #[test]
    fn collection_grant_binds_the_exact_ordered_target_list() {
        let directory = tempfile::tempdir().unwrap();
        let database = crate::db::Database::open(&directory.path().join("test.db")).unwrap();
        let targets = vec![
            crate::model::TaskTarget {
                kind: crate::model::TaskKind::Cask,
                token: "zed".into(),
            },
            crate::model::TaskTarget {
                kind: crate::model::TaskKind::Cask,
                token: "ghostty".into(),
            },
        ];
        let mut original = request("collection/dev-setup");
        original.action = "install collection".into();
        original.plan_digest = Some(crate::queue::ordered_install_plans_digest(
            &targets
                .iter()
                .map(|target| {
                    crate::queue::ResolvedInstallPlan::resolve(
                        &database,
                        target.clone(),
                        Default::default(),
                    )
                    .unwrap()
                })
                .collect::<Vec<_>>(),
        ));
        let service = ConfirmationService::default();
        let pending = service.test_begin(original.clone()).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        let mut reordered = original.clone();
        reordered.plan_digest = Some(crate::queue::ordered_install_plans_digest(
            &targets
                .into_iter()
                .rev()
                .map(|target| {
                    crate::queue::ResolvedInstallPlan::resolve(
                        &database,
                        target,
                        Default::default(),
                    )
                    .unwrap()
                })
                .collect::<Vec<_>>(),
        ));
        assert_eq!(
            service.consume(&grant, &reordered).unwrap_err().code,
            "E_CONFIRMATION_MISMATCH"
        );
    }

    #[tokio::test]
    async fn timed_out_dialog_keeps_slot_until_native_callback() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let pending = service.test_begin(request("cask/ghostty")).unwrap();
        let id = pending.id;
        assert_eq!(
            wait_until_timeout(&service, pending, Duration::from_millis(50))
                .await
                .unwrap_err()
                .code,
            "E_CONFIRMATION_TIMEOUT"
        );
        assert_eq!(
            service.test_begin(request("cask/zed")).unwrap_err().code,
            "E_BUSY"
        );

        assert_eq!(
            service.test_finish(id, true).unwrap_err().code,
            "E_CONFIRMATION_TIMEOUT"
        );
        assert!(service.test_begin(request("cask/zed")).is_ok());
    }

    #[tokio::test]
    async fn late_allow_after_timeout_never_issues_a_grant() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let pending = service.test_begin(request("cask/ghostty")).unwrap();
        let id = pending.id;
        assert_eq!(
            wait_until_timeout(&service, pending, Duration::from_millis(50))
                .await
                .unwrap_err()
                .code,
            "E_CONFIRMATION_TIMEOUT"
        );

        let late = service.test_finish(id, true).unwrap_err();
        assert_eq!(late.code, "E_CONFIRMATION_TIMEOUT");
        assert_eq!(late.message, "confirmation_timeout");
        assert!(service.state.lock().unwrap().grants.is_empty());
        assert!(service.test_begin(request("cask/zed")).is_ok());
    }

    #[test]
    fn timeout_revokes_grant_from_the_same_confirmation() {
        let service = ConfirmationService::default();
        let original = request("cask/ghostty");
        let pending = service.test_begin(original.clone()).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        service.test_timeout(pending.id);
        assert_eq!(
            service.consume(&grant, &original).unwrap_err().code,
            "E_CONFIRMATION_REUSED"
        );
    }

    #[test]
    fn denial_and_timeout_never_create_a_grant() {
        let service = ConfirmationService::default();
        let denied = service.test_begin(request("cask/ghostty")).unwrap();
        assert_eq!(
            service.test_finish(denied.id, false).unwrap_err().code,
            "E_CONFIRMATION_DENIED"
        );
        let timed_out = service.test_begin(request("cask/zed")).unwrap();
        service.test_timeout(timed_out.id);
        assert_eq!(
            service.test_finish(timed_out.id, true).unwrap_err().code,
            "E_CONFIRMATION_TIMEOUT"
        );
    }

    #[test]
    fn grants_are_single_use_and_digest_bound() {
        let service = ConfirmationService::default();
        let original = request("cask/ghostty");
        let pending = service.test_begin(original.clone()).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        assert_eq!(
            service
                .consume(&grant, &request("cask/zed"))
                .unwrap_err()
                .code,
            "E_CONFIRMATION_MISMATCH"
        );
        assert_eq!(
            service.consume(&grant, &original).unwrap_err().code,
            "E_CONFIRMATION_REUSED"
        );

        let pending = service.test_begin(original.clone()).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        assert!(service.consume(&grant, &original).is_ok());
        assert_eq!(
            service.consume(&grant, &original).unwrap_err().code,
            "E_CONFIRMATION_REUSED"
        );
    }

    #[test]
    fn deletion_plan_digest_is_part_of_the_grant() {
        let service = ConfirmationService::default();
        let mut original = request("app/com.example.Editor");
        original.plan_digest = Some("plan-a".into());
        let pending = service.test_begin(original.clone()).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        let mut changed = original.clone();
        changed.plan_digest = Some("plan-b".into());
        assert_eq!(
            service.consume(&grant, &changed).unwrap_err().code,
            "E_CONFIRMATION_MISMATCH"
        );
    }

    #[test]
    fn expired_grants_are_denied_and_removed() {
        let service = ConfirmationService::default();
        let original = request("cask/ghostty");
        let grant = service.test_issue_expired(original.clone());
        assert_eq!(
            service.consume(&grant, &original).unwrap_err().code,
            "E_CONFIRMATION_EXPIRED"
        );
    }

    #[tokio::test]
    async fn cancelling_a_waiting_confirmation_releases_the_slot() {
        use std::{
            future::Future,
            task::{Context, Poll, Waker},
        };
        let service = std::sync::Arc::new(ConfirmationService::default());
        let mut waiting = Box::pin(service.clone().test_waiting(request("cask/ghostty")));
        assert!(matches!(
            waiting
                .as_mut()
                .poll(&mut Context::from_waker(Waker::noop())),
            Poll::Pending
        ));
        assert_eq!(
            service.test_begin(request("cask/zed")).unwrap_err().code,
            "E_BUSY"
        );
        drop(waiting);
        assert!(service.test_begin(request("cask/zed")).is_ok());
    }

    #[tokio::test]
    async fn webview_destruction_cancels_waiting_without_a_native_click() {
        use std::{
            future::Future,
            task::{Context, Poll, Waker},
        };
        let service = std::sync::Arc::new(ConfirmationService::default());
        let mut waiting = Box::pin(service.clone().test_waiting(request("cask/ghostty")));
        assert!(matches!(
            waiting
                .as_mut()
                .poll(&mut Context::from_waker(Waker::noop())),
            Poll::Pending
        ));
        service.cancel_active();
        tokio::time::timeout(Duration::from_secs(1), waiting)
            .await
            .unwrap();
        assert!(service.test_begin(request("cask/zed")).is_ok());
    }

    #[test]
    fn display_values_strip_bidi_controls_and_use_an_ellipsis() {
        let controls = [
            '\u{202A}', '\u{202B}', '\u{202C}', '\u{202D}', '\u{202E}', '\u{2066}', '\u{2067}',
            '\u{2068}', '\u{2069}', '\u{200E}', '\u{200F}', '\u{061C}',
        ];
        let value = format!(
            "safe{}value{}",
            controls.iter().collect::<String>(),
            "x".repeat(600)
        );
        let displayed = display_value(&value);
        assert!(controls.iter().all(|control| !displayed.contains(*control)));
        assert!(displayed.ends_with('…'));
        assert_eq!(displayed.chars().count(), 512);
    }

    #[test]
    fn caller_source_and_action_each_change_the_digest() {
        let original = request("cask/ghostty");
        let mut caller = original.clone();
        caller.caller.push_str(" changed");
        let mut source = original.clone();
        source.source.push_str(" changed");
        let mut action = original.clone();
        action.action.push_str(" changed");
        assert_ne!(original.digest(), caller.digest());
        assert_ne!(original.digest(), source.digest());
        assert_ne!(original.digest(), action.digest());
    }

    fn reject_changed_request(changed: ConfirmationRequest) {
        let service = ConfirmationService::default();
        let pending = service.test_begin(request("cask/ghostty")).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        assert_eq!(
            service.consume(&grant, &changed).unwrap_err().code,
            "E_CONFIRMATION_MISMATCH"
        );
    }

    #[test]
    fn grant_binds_caller() {
        let mut changed = request("cask/ghostty");
        changed.caller = "another caller".into();
        reject_changed_request(changed);
    }

    #[test]
    fn grant_binds_source() {
        let mut changed = request("cask/ghostty");
        changed.source = "opennavo://collection/another?action=install".into();
        reject_changed_request(changed);
    }

    #[test]
    fn grant_binds_action() {
        let mut changed = request("cask/ghostty");
        changed.action = "uninstall".into();
        reject_changed_request(changed);
    }

    #[test]
    fn single_app_grant_binds_the_target() {
        reject_changed_request(request("cask/zed"));
    }

    #[test]
    fn truncation_handles_the_exact_boundary() {
        assert_eq!(display_value(&"x".repeat(512)), "x".repeat(512));
        assert_eq!(
            display_value(&"x".repeat(513)),
            format!("{}…", "x".repeat(511))
        );
    }

    #[tokio::test]
    async fn allow_is_delivered_before_cancellation_channel_closes() {
        use std::{
            future::Future,
            task::{Context, Poll, Waker},
        };
        let service = std::sync::Arc::new(ConfirmationService::default());
        let request = request("cask/ghostty");
        let pending = service.begin(request.clone()).unwrap();
        let id = pending.id;
        let guard = ConfirmationDropGuard::new(service.clone(), id);
        let (sender, receiver) = oneshot::channel();
        let mut waiting = Box::pin(service.wait_for_reply(pending, receiver, guard));
        let mut context = Context::from_waker(Waker::noop());
        assert!(matches!(waiting.as_mut().poll(&mut context), Poll::Pending));
        service.finish_reply(id, true, |result| {
            // Force the exact old race: poll after grant creation but before sending the allow.
            // Closure of _cancel here must never turn that allow into a denial.
            assert!(matches!(waiting.as_mut().poll(&mut context), Poll::Pending));
            sender.send(result).unwrap();
        });
        let grant = waiting.await.expect("allow must always return allow");
        service.consume(&grant, &request).unwrap();
    }

    #[test]
    fn collection_prompt_distinguishes_same_name_with_different_tokens_and_taps() {
        let mut request = request("collection/dev-setup");
        request.source = "opennavo://collection/dev-setup?action=install".into();
        request.action = "install collection".into();
        let first = ConfirmationListItem {
            name: "Same App".into(),
            kind: "cask".into(),
            token: "official".into(),
            tap: None,
            disabled: false,
            installed: false,
            already_queued: false,
            unsupported: false,
            duplicate: false,
        };
        let second = ConfirmationListItem {
            token: "different".into(),
            tap: Some("owner/third-party".into()),
            ..first.clone()
        };
        let message = request
            .collection_message(&[first, second], 2, Locale::EnUs)
            .unwrap();
        let (header, rows) = message.split_once('•').unwrap();
        assert!(header.contains("collection/dev-setup"));
        assert!(header.contains("will install 2, skip 0"));
        assert!(rows.contains("Same App [cask/official]\n"));
        assert!(rows.contains("Same App [cask/different] [owner/third-party]\n"));
    }

    #[tokio::test]
    async fn completed_native_reply_wins_over_closed_cancellation_channel() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let request = request("cask/ghostty");
        let pending = service.begin(request.clone()).unwrap();
        let guard = ConfirmationDropGuard::new(service.clone(), pending.id);
        let (sender, receiver) = oneshot::channel();
        sender.send(service.test_finish(pending.id, true)).unwrap();
        let grant = service
            .wait_for_reply(pending, receiver, guard)
            .await
            .unwrap();
        service.consume(&grant, &request).unwrap();
    }

    #[test]
    fn native_confirmation_copy_is_complete_in_all_ten_locales() {
        assert_eq!(crate::locales_gen::LOCALES.len(), 10);
        for locale in crate::locales_gen::LOCALES {
            for key in [
                "title",
                "allow",
                "deny",
                "installAll",
                "cancel",
                "disabled",
                "installed",
                "queued",
                "duplicate",
                "unsupported",
                "deepLink",
                "install",
                "installCollection",
            ] {
                assert!(!text(locale, key).unwrap().trim().is_empty());
            }
            let request = request("cask/ghostty");
            let message = request.message(locale).unwrap();
            assert!(message.contains(&request.source));
            assert!(message.contains("cask/ghostty"));
            for field in ["{caller}", "{source}", "{action}", "{target}"] {
                assert!(!message.contains(field));
            }
            let items = vec![
                ConfirmationListItem {
                    name: "Fixture".into(),
                    kind: "cask".into(),
                    token: "fixture".into(),
                    tap: None,
                    disabled: false,
                    installed: false,
                    already_queued: false,
                    unsupported: false,
                    duplicate: false,
                };
                12
            ];
            let message = request.collection_message(&items, 7, locale).unwrap();
            assert!(message.contains('7'), "{locale:?}: {message}");
            assert!(message.contains('5'), "{locale:?}: {message}");
            assert!(!message.contains("12"), "{locale:?}: {message}");
            for field in ["{install}", "{skip}", "{count}"] {
                assert!(!message.contains(field), "{locale:?}: {message}");
            }
        }
    }
}
