//! Native confirmation gates for writes that originate outside the app UI.
//!
//! The service deliberately keeps the policy state separate from the dialog adapter. That makes the
//! one-dialog rule and grant lifecycle testable without creating an AppKit window in a libtest worker,
//! while confirm always delegates rendering to the dialog plugin, which marshals the dialog to Tauri's
//! main thread on desktop.

use crate::AppError;
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    sync::Mutex,
    time::{Duration, Instant},
};
use tauri::Runtime;
use tauri_plugin_dialog::{DialogExt, MessageDialogButtons, MessageDialogKind};
use tokio::sync::oneshot;

const DIALOG_TIMEOUT: Duration = Duration::from_secs(60);
const GRANT_TTL: Duration = Duration::from_secs(30);

/// A write request presented to the user. plan_digest is used by uninstall/cleanup callers and is part of
/// the request digest even when the caller also passes it separately to consume.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfirmationRequest {
    pub caller: String,
    pub source: String,
    pub action: String,
    pub target: String,
    pub plan_digest: Option<String>,
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

    fn message(&self) -> String {
        format!(
            "OpenNavo received a request to change this Mac.\n\nCaller: {}\nSource: {}\nAction: {}\nTarget: {}\n\nAllow this one operation?",
            display_value(&self.caller),
            display_value(&self.source),
            display_value(&self.action),
            display_value(&self.target),
        )
    }
}

/// A grant is intentionally opaque to callers. It can only be consumed through this service, and consumption
/// removes it before checking the supplied digest so a mismatched attempt cannot be retried or used as an oracle.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfirmationGrant {
    token: String,
    digest: String,
    plan_digest: Option<String>,
    expires_at: Instant,
}

#[derive(Debug)]
struct ActiveConfirmation {
    id: u64,
    request: ConfirmationRequest,
    digest: String,
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

#[derive(Debug, Clone)]
struct PendingConfirmation {
    id: u64,
    request: ConfirmationRequest,
}

impl ConfirmationService {
    /// Show a native dialog and return a grant only after the user explicitly allows it.
    pub async fn confirm<R: Runtime>(
        self: &std::sync::Arc<Self>,
        app: &tauri::AppHandle<R>,
        request: ConfirmationRequest,
    ) -> Result<ConfirmationGrant, AppError> {
        let pending = self.begin(request)?;
        let (sender, receiver) = oneshot::channel();
        let service = std::sync::Arc::clone(self);
        let dialog_id = pending.id;
        app.dialog()
            .message(pending.request.message())
            .title("OpenNavo confirmation")
            .kind(MessageDialogKind::Warning)
            .buttons(MessageDialogButtons::OkCancelCustom(
                "Allow".into(),
                "Deny".into(),
            ))
            // tauri-plugin-dialog's desktop adapter calls run_on_main_thread before showing AppKit/rfd.
            .show(move |allowed| {
                let result = service.finish(dialog_id, allowed);
                let _ = sender.send(result);
            });

        match tokio::time::timeout(DIALOG_TIMEOUT, receiver).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) => {
                self.expire(dialog_id);
                Err(AppError::new(
                    "E_CONFIRMATION_DENIED",
                    "confirmation_denied",
                ))
            }
            Err(_) => {
                self.expire(dialog_id);
                Err(AppError::new(
                    "E_CONFIRMATION_TIMEOUT",
                    "confirmation_timeout",
                ))
            }
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
        let pending = PendingConfirmation {
            id: state.next_id,
            request: request.clone(),
        };
        state.active = Some(ActiveConfirmation {
            id: pending.id,
            request,
            digest,
        });
        Ok(pending)
    }

    fn finish(&self, id: u64, allowed: bool) -> Result<ConfirmationGrant, AppError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "confirmation_state"))?;
        let Some(active) = state.active.take() else {
            return Err(AppError::new(
                "E_CONFIRMATION_TIMEOUT",
                "confirmation_expired",
            ));
        };
        if active.id != id {
            return Err(AppError::new(
                "E_CONFIRMATION_TIMEOUT",
                "confirmation_expired",
            ));
        }
        if !allowed {
            return Err(AppError::new(
                "E_CONFIRMATION_DENIED",
                "confirmation_denied",
            ));
        }
        let grant = ConfirmationGrant {
            token: uuid::Uuid::new_v4().to_string(),
            digest: active.digest,
            plan_digest: active.request.plan_digest,
            expires_at: Instant::now() + GRANT_TTL,
        };
        state.grants.insert(grant.token.clone(), grant.clone());
        Ok(grant)
    }

    fn expire(&self, id: u64) {
        if let Ok(mut state) = self.state.lock()
            && state.active.as_ref().is_some_and(|active| active.id == id)
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
        self.finish(id, allowed)
    }

    #[cfg(test)]
    fn test_expire(&self, id: u64) {
        self.expire(id)
    }

    #[cfg(test)]
    fn test_issue_expired(&self, request: ConfirmationRequest) -> ConfirmationGrant {
        let pending = self.begin(request).expect("test confirmation starts");
        let mut grant = self
            .finish(pending.id, true)
            .expect("test confirmation grants");
        grant.expires_at = Instant::now() - Duration::from_secs(1);
        if let Ok(mut state) = self.state.lock() {
            state.grants.insert(grant.token.clone(), grant.clone());
        }
        grant
    }
}

fn display_value(value: &str) -> String {
    let value: String = value
        .chars()
        .filter(|character| !character.is_control())
        .take(512)
        .collect();
    if value.is_empty() {
        "(unknown)".into()
    } else {
        value
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
        let prompt = request("cask/ghostty").message();
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
    fn denial_and_timeout_never_create_a_grant() {
        let service = ConfirmationService::default();
        let denied = service.test_begin(request("cask/ghostty")).unwrap();
        assert_eq!(
            service.test_finish(denied.id, false).unwrap_err().code,
            "E_CONFIRMATION_DENIED"
        );
        let timed_out = service.test_begin(request("cask/zed")).unwrap();
        service.test_expire(timed_out.id);
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
}
