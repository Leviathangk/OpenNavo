//! Confirmation gate for deep-link installs and Local MCP writes, answered in OpenNavo's own window.
//!
//! Requests made by the app's own UI do not pass through this gate. The service deliberately keeps
//! the policy state separate from the window adapter (`confirm_window`). That makes the one-slot rule
//! and grant lifecycle testable without creating a window in a libtest worker; the window itself is
//! created and destroyed on Tauri's main thread.

use crate::AppError;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    sync::Mutex,
    time::{Duration, Instant},
};
use tauri::Runtime;
use tokio::sync::oneshot;

const DIALOG_TIMEOUT: Duration = Duration::from_secs(60);
const GRANT_TTL: Duration = Duration::from_secs(30);

// Every confirmation is shown in OpenNavo's own window (see `confirm_window`). A timeout revokes that
// confirmation's grants; the window is then closed and the slot released, and a late click never produces a grant.

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

/// One row in a collection confirmation. The confirmation window shows the complete list so the user approves the
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
    /// Set by [`ConfirmationService::revoke_caller`]: the request was withdrawn while its dialog was open.
    revoked: bool,
    _cancel: Option<oneshot::Sender<()>>,
}

#[derive(Default)]
struct State {
    next_id: u64,
    active: Option<ActiveConfirmation>,
    grants: HashMap<String, ConfirmationGrant>,
    prompt: Option<WindowPrompt>,
}

/// The confirmation shown in the OpenNavo window, answerable once through its one-time id.
#[derive(Debug)]
struct WindowPrompt {
    dialog_id: u64,
    view: McpConfirmView,
    /// When the window first read its content; Allow is refused until [`ALLOW_ARM_DELAY`] after it.
    shown_at: Option<Instant>,
    reply: Option<oneshot::Sender<Result<ConfirmationGrant, AppError>>>,
}

/// Label of the Local MCP confirmation window; only it may read or answer a prompt.
pub const MCP_CONFIRM_LABEL: &str = "mcp-confirm";

/// Allow is not accepted until this long after the window showed its content (checked here, not in the UI).
pub const ALLOW_ARM_DELAY: Duration = Duration::from_millis(1000);

/// Trusted content of the Local MCP confirmation window, built in Rust and filtered for display.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct McpConfirmView {
    /// One-time id the window answers with.
    pub id: String,
    /// `install`, `uninstall`, `installCollection`, or `brewfileInstall`, `cleanup`, `uninstallDelete`, `purge`, `brewUninstall`.
    pub action: String,
    pub name: String,
    /// Catalog target (`cask/iina`) or the app path.
    pub target: String,
    /// App bundle moved to the Trash (uninstall only).
    pub path: Option<String>,
    /// Signing authority of the requesting process, only when it is validly signed.
    pub signer: Option<String>,
    /// Team ID of that signature, always shown beside the signer so the name can
    /// be checked against a team rather than taken on trust.
    pub team_id: Option<String>,
    /// Executable of the requesting process.
    pub caller_path: Option<String>,
    /// Nearest `.app` ancestor, unverified.
    pub may_come_from: Option<String>,
    /// Self-reported client name.
    pub claimed_name: Option<String>,
    /// Milliseconds until Allow is accepted.
    pub allow_after_ms: u32,
    /// Who asked: `mcp` (an AI app over Local MCP), `deepLink` (an opennavo:// web link) or `app` (OpenNavo itself).
    pub origin: String,
    /// The opennavo:// link that asked (deep links only). macOS hands the app only the URL, so
    /// there is no referring page to show.
    pub source: Option<String>,
    /// Every app of a collection, in plan order (`installCollection` only).
    pub items: Vec<ConfirmItemView>,
    /// How many of `items` will be queued; the rest are skipped with their reason.
    pub install_count: u32,
    /// Total size of what is removed (`cleanup`, `uninstall`, `uninstallDelete`, `purge`).
    pub bytes: Option<u64>,
    /// Something is deleted for good instead of moved to the Trash.
    pub permanent: bool,
}

/// One app of a collection confirmation, filtered for display.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct ConfirmItemView {
    pub name: String,
    /// `cask/token`, plus ` [owner/tap]` for a third-party tap.
    pub target: String,
    /// Why the app is skipped: `disabled`, `installed`, `queued`, `duplicate`, `unsupported` or
    /// `declined`; `None` when it will be installed.
    pub skip: Option<String>,
    /// `adopt` when the install takes over an app that is already in /Applications;
    /// `cleanupGroup` when `name` is a group key the window translates (`temp`, `trash`, `installers`, `logs`).
    pub note: Option<String>,
}

impl McpConfirmView {
    /// Build a view, filtering every caller-influenced string for display.
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        action: &str,
        name: &str,
        target: &str,
        path: Option<&str>,
        signer: Option<&str>,
        team_id: Option<&str>,
        caller_path: Option<&str>,
        may_come_from: Option<&str>,
        claimed_name: Option<&str>,
    ) -> Self {
        Self {
            id: String::new(),
            action: action.to_owned(),
            name: display_value(name),
            target: display_value(target),
            path: path.map(display_value),
            signer: signer.map(display_value),
            team_id: team_id.map(display_value),
            caller_path: caller_path.map(display_value),
            may_come_from: may_come_from.map(display_value),
            claimed_name: claimed_name.map(display_value),
            allow_after_ms: 0,
            origin: "mcp".into(),
            source: None,
            items: Vec::new(),
            install_count: 0,
            bytes: None,
            permanent: false,
        }
    }

    /// A destructive command of OpenNavo's own window (`brewfileInstall`, `cleanup`, `uninstall`,
    /// `uninstallDelete`, `purge` or `brewUninstall`): the complete list of what runs, in order, and its total size.
    pub fn app_action(
        action: &str,
        name: &str,
        items: Vec<ConfirmItemView>,
        install_count: usize,
        bytes: Option<u64>,
    ) -> Self {
        Self {
            origin: "app".into(),
            items,
            install_count: u32::try_from(install_count).unwrap_or(u32::MAX),
            bytes,
            permanent: matches!(action, "uninstallDelete" | "purge" | "brewUninstall"),
            ..Self::new(action, name, "", None, None, None, None, None, None)
        }
        .without_empty_name(name)
    }

    /// OpenNavo's own requests have no target line, and some (cleanup, purge) no single name.
    fn without_empty_name(mut self, name: &str) -> Self {
        self.target = String::new();
        if name.trim().is_empty() {
            self.name = String::new();
        }
        self
    }

    /// A deep-link install of one app.
    pub fn deep_link_install(request: &ConfirmationRequest, name: &str) -> Self {
        Self {
            origin: "deepLink".into(),
            source: Some(display_value(&request.source)),
            ..Self::new(
                "install",
                name,
                &request.target,
                None,
                None,
                None,
                None,
                None,
                None,
            )
        }
    }

    /// A deep-link collection install: every app is listed with its skip reason, and the counts
    /// must add up to the list the grant binds.
    pub fn deep_link_collection(
        request: &ConfirmationRequest,
        name: &str,
        items: &[ConfirmationListItem],
        install_count: usize,
    ) -> Result<Self, AppError> {
        let installable = items
            .iter()
            .filter(|item| item.skip_reason().is_none())
            .count();
        if install_count > items.len() || installable != install_count {
            return Err(AppError::new("E_INVALID_ARG", "collection_counts"));
        }
        Ok(Self {
            items: items.iter().map(ConfirmationListItem::view).collect(),
            install_count: u32::try_from(install_count)
                .map_err(|_| AppError::new("E_INVALID_ARG", "collection_counts"))?,
            ..Self {
                action: "installCollection".into(),
                ..Self::deep_link_install(request, name)
            }
        })
    }
}

impl ConfirmItemView {
    /// One row of a destructive confirmation: what it is and the path that is removed.
    pub fn row(name: &str, target: &str) -> Self {
        Self {
            name: display_value(name),
            target: display_value(target),
            skip: None,
            note: None,
        }
    }
}

impl ConfirmationListItem {
    pub(crate) fn skip_reason(&self) -> Option<&'static str> {
        if self.disabled {
            Some("disabled")
        } else if self.installed {
            Some("installed")
        } else if self.already_queued {
            Some("queued")
        } else if self.duplicate {
            Some("duplicate")
        } else if self.unsupported {
            Some("unsupported")
        } else {
            None
        }
    }

    pub(crate) fn view(&self) -> ConfirmItemView {
        let identity = format!("{}/{}", self.kind, self.token);
        ConfirmItemView {
            name: display_value(&self.name),
            target: display_value(
                &self
                    .tap
                    .as_ref()
                    .map_or(identity.clone(), |tap| format!("{identity} [{tap}]")),
            ),
            skip: self.skip_reason().map(str::to_owned),
            note: None,
        }
    }
}

/// Single-slot confirmation service. Keep this in app state so the later MCP broker can reuse the same
/// gate instead of adding another dialog or grant implementation.
#[derive(Default)]
pub struct ConfirmationService {
    state: Mutex<State>,
}

#[derive(Debug)]
struct PendingConfirmation {
    id: u64,
    cancelled: oneshot::Receiver<()>,
}

/// Clears the single active slot when the command future is cancelled (for example when its webview is
/// destroyed). Window answers can arrive later; `finish` checks the id and ignores those stale clicks.
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
    /// Ask about a deep-link install of one app in OpenNavo's confirmation window; a grant comes
    /// back only after the user explicitly allows it.
    pub async fn confirm<R: Runtime>(
        self: &std::sync::Arc<Self>,
        app: &tauri::AppHandle<R>,
        request: ConfirmationRequest,
        name: &str,
    ) -> Result<ConfirmationGrant, AppError> {
        let view = McpConfirmView::deep_link_install(&request, name);
        self.confirm_window(app, request, view).await
    }

    /// Ask in OpenNavo's own confirmation window (Local MCP writes). The window is created here, in
    /// the app process, and its content is `view`, filtered for display. The same single slot, timeout
    /// and one-shot grant rules apply; `request` is what the grant binds to. However the wait ends
    /// (answer, timeout, revocation), the window is closed and the slot released.
    pub async fn confirm_window<R: Runtime>(
        self: &std::sync::Arc<Self>,
        app: &tauri::AppHandle<R>,
        request: ConfirmationRequest,
        view: McpConfirmView,
    ) -> Result<ConfirmationGrant, AppError> {
        let service = std::sync::Arc::clone(self);
        self.confirm_prompt(
            request,
            view,
            DIALOG_TIMEOUT,
            |dialog_id, tall| confirm_window::open(app, service, dialog_id, tall),
            || confirm_window::close(app),
        )
        .await
    }

    /// The window-independent part of [`Self::confirm_window`]: `open` shows the prompt, `close`
    /// removes it. Tests drive the same prompt through [`Self::window_view`] and
    /// [`Self::answer_window`] without creating a window.
    pub(crate) async fn confirm_prompt(
        self: &std::sync::Arc<Self>,
        request: ConfirmationRequest,
        view: McpConfirmView,
        timeout: Duration,
        open: impl FnOnce(u64, bool) -> Result<(), AppError>,
        close: impl FnOnce(),
    ) -> Result<ConfirmationGrant, AppError> {
        let pending = self.begin(request)?;
        let dialog_id = pending.id;
        let tall = !view.items.is_empty();
        let receiver = self.open_prompt(dialog_id, view)?;
        let guard = ConfirmationDropGuard::new(std::sync::Arc::clone(self), dialog_id);
        let result = match open(dialog_id, tall) {
            Ok(()) => {
                self.wait_for_reply_within(pending, receiver, guard, timeout)
                    .await
            }
            Err(error) => {
                drop(guard);
                Err(error)
            }
        };
        // Close before freeing the slot so a later request's window is never the one destroyed.
        close();
        self.release_prompt(dialog_id);
        result
    }

    /// Ask about a whole collection in one confirmation window. The request's plan digest binds the
    /// displayed list to the ordered execution plans that the caller enqueues after the grant is consumed.
    pub async fn confirm_collection<R: Runtime>(
        self: &std::sync::Arc<Self>,
        app: &tauri::AppHandle<R>,
        request: ConfirmationRequest,
        name: &str,
        items: Vec<ConfirmationListItem>,
        install_count: usize,
    ) -> Result<ConfirmationGrant, AppError> {
        let view = McpConfirmView::deep_link_collection(&request, name, &items, install_count)?;
        self.confirm_window(app, request, view).await
    }

    #[cfg(test)]
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
            _ = pending.cancelled => if self.is_revoked(id) {
                Err(AppError::new("E_CONFIRMATION_INVALIDATED", "confirmation_invalidated"))
            } else {
                Err(AppError::new("E_CONFIRMATION_DENIED", "confirmation_denied"))
            },
        }
    }

    /// A destroyed webview cannot keep a command waiting on a window answer. Clearing the slot also
    /// drops timed-out confirmations on purpose: once the window is gone, any later interaction
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
            cancelled,
        };
        state.active = Some(ActiveConfirmation {
            id: pending.id,
            request,
            digest,
            timed_out: false,
            revoked: false,
            _cancel: Some(cancel),
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

    fn open_prompt(
        &self,
        dialog_id: u64,
        mut view: McpConfirmView,
    ) -> Result<oneshot::Receiver<Result<ConfirmationGrant, AppError>>, AppError> {
        let (sender, receiver) = oneshot::channel();
        view.id = uuid::Uuid::new_v4().to_string();
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "confirmation_state"))?;
        state.prompt = Some(WindowPrompt {
            dialog_id,
            view,
            shown_at: None,
            reply: Some(sender),
        });
        Ok(receiver)
    }

    /// Content for the confirmation window. The first read starts the Allow delay.
    pub fn window_view(&self) -> Option<McpConfirmView> {
        let mut state = self.state.lock().ok()?;
        let prompt = state.prompt.as_mut()?;
        let shown_at = *prompt.shown_at.get_or_insert_with(Instant::now);
        let mut view = prompt.view.clone();
        view.allow_after_ms = ALLOW_ARM_DELAY
            .saturating_sub(shown_at.elapsed())
            .as_millis()
            .try_into()
            .unwrap_or(u32::MAX);
        Some(view)
    }

    /// Answer the window's prompt once. Allow is refused before the window showed its content for
    /// [`ALLOW_ARM_DELAY`]; a stale or reused id is refused.
    pub fn answer_window(&self, id: &str, allow: bool) -> Result<(), AppError> {
        let (dialog_id, reply) = {
            let mut state = self
                .state
                .lock()
                .map_err(|_| AppError::new("E_UNKNOWN", "confirmation_state"))?;
            let Some(prompt) = state.prompt.as_mut().filter(|prompt| prompt.view.id == id) else {
                return Err(AppError::new(
                    "E_CONFIRMATION_EXPIRED",
                    "confirmation_expired",
                ));
            };
            if allow
                && prompt
                    .shown_at
                    .is_none_or(|shown| shown.elapsed() < ALLOW_ARM_DELAY)
            {
                return Err(AppError::new(
                    "E_CONFIRMATION_TOO_EARLY",
                    "confirmation_too_early",
                ));
            }
            let Some(reply) = prompt.reply.take() else {
                return Err(AppError::new(
                    "E_CONFIRMATION_EXPIRED",
                    "confirmation_expired",
                ));
            };
            (prompt.dialog_id, reply)
        };
        self.finish_reply(dialog_id, allow, |result| {
            let _ = reply.send(result);
        });
        Ok(())
    }

    /// The window was closed (or failed): that is a refusal.
    /// Deny the window prompt `dialog_id`; events from an older window never touch a newer request.
    pub fn deny_window(&self, dialog_id: u64) {
        let pending = self.state.lock().ok().and_then(|mut state| {
            let prompt = state
                .prompt
                .as_mut()
                .filter(|prompt| prompt.dialog_id == dialog_id)?;
            Some((prompt.dialog_id, prompt.reply.take()?))
        });
        if let Some((dialog_id, reply)) = pending {
            self.finish_reply(dialog_id, false, |result| {
                let _ = reply.send(result);
            });
        }
    }

    /// Forget the prompt and free the slot it holds; its window is being closed, so no late answer
    /// can arrive.
    fn release_prompt(&self, dialog_id: u64) {
        if let Ok(mut state) = self.state.lock() {
            if state
                .prompt
                .as_ref()
                .is_some_and(|prompt| prompt.dialog_id == dialog_id)
            {
                state.prompt = None;
            }
            if state
                .active
                .as_ref()
                .is_some_and(|active| active.id == dialog_id)
            {
                state.active = None;
            }
        }
    }

    /// Withdraw the open confirmation from `caller` (Local MCP when a switch is turned off). The waiting
    /// request ends at once with `E_CONFIRMATION_INVALIDATED` and its grants are revoked. The window is then
    /// closed by the waiting `confirm_window`, which frees the slot; an answer that still arrives never
    /// produces a grant.
    pub fn revoke_caller(&self, caller: &str) -> bool {
        let Ok(mut state) = self.state.lock() else {
            return false;
        };
        let Some(active) = state
            .active
            .as_mut()
            .filter(|active| active.request.caller == caller)
        else {
            return false;
        };
        active.timed_out = true;
        active.revoked = true;
        let id = active.id;
        let cancel = active._cancel.take();
        state.grants.retain(|_, grant| grant.confirmation_id != id);
        drop(state);
        drop(cancel);
        true
    }

    fn is_revoked(&self, id: u64) -> bool {
        self.state.lock().is_ok_and(|state| {
            state
                .active
                .as_ref()
                .is_some_and(|active| active.id == id && active.revoked)
        })
    }

    /// A timed-out confirmation keeps the single slot until its window is closed and the slot
    /// released (or a late answer arrives), but its already-issued grants must not stay usable.
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

    /// Tests only: pretend the window has shown its prompt for longer than [`ALLOW_ARM_DELAY`].
    #[cfg(test)]
    pub(crate) fn test_arm(&self) {
        if let Ok(mut state) = self.state.lock()
            && let Some(prompt) = state.prompt.as_mut()
        {
            prompt.shown_at = Some(Instant::now() - ALLOW_ARM_DELAY);
        }
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

pub(crate) fn display_value(value: &str) -> String {
    const MAX_DISPLAY_CHARS: usize = 512;
    let mut chars = value.chars().filter(|character| {
        !character.is_control() && !crate::mcp::protocol::is_hidden_text(*character)
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

/// Creates and closes the Local MCP confirmation window (Tauri side).
mod confirm_window {
    use super::{ConfirmationService, MCP_CONFIRM_LABEL};
    use crate::AppError;
    use std::sync::Arc;
    use tauri::{Manager, Runtime, WebviewUrl, WebviewWindowBuilder};

    /// Only the bundled app page may load; no remote content, no outside links.
    pub(super) fn allowed_navigation(url: &tauri::Url) -> bool {
        match url.scheme() {
            "tauri" => url.host_str() == Some("localhost"),
            "http" | "https" => {
                url.host_str() == Some("tauri.localhost")
                    || (cfg!(debug_assertions) && url.host_str() == Some("localhost"))
            }
            _ => false,
        }
    }

    pub(super) fn open<R: Runtime>(
        app: &tauri::AppHandle<R>,
        service: Arc<ConfirmationService>,
        dialog_id: u64,
        tall: bool,
    ) -> Result<(), AppError> {
        let handle = app.clone();
        app.run_on_main_thread(move || {
            if let Some(stale) = handle.get_webview_window(MCP_CONFIRM_LABEL) {
                let _ = stale.destroy();
            }
            let built = WebviewWindowBuilder::new(
                &handle,
                MCP_CONFIRM_LABEL,
                WebviewUrl::App("index.html#/mcp-confirm".into()),
            )
            .title("OpenNavo")
            .inner_size(480.0, if tall { 620.0 } else { 460.0 })
            .resizable(false)
            .minimizable(false)
            .maximizable(false)
            .always_on_top(true)
            .visible_on_all_workspaces(true)
            .skip_taskbar(true)
            .center()
            .focused(true)
            .on_navigation(allowed_navigation)
            .build();
            match built {
                Ok(window) => {
                    let events = Arc::clone(&service);
                    let focused_once = std::sync::atomic::AtomicBool::new(false);
                    window.on_window_event(move |event| {
                        if denies(event, &focused_once) {
                            events.deny_window(dialog_id);
                        }
                    });
                    let _ = window.set_focus();
                }
                Err(error) => {
                    log::warn!("Local MCP confirmation window unavailable: {error}");
                    service.deny_window(dialog_id);
                }
            }
        })
        .map_err(|_| AppError::new("E_UNKNOWN", "confirmation_window"))
    }

    /// Closing the window is a refusal, and so is leaving it once it had focus: the user turned to
    /// something else without allowing. Losing focus before it was ever focused (the window opened
    /// behind another app) is not an answer.
    pub(super) fn denies(
        event: &tauri::WindowEvent,
        focused_once: &std::sync::atomic::AtomicBool,
    ) -> bool {
        use std::sync::atomic::Ordering;
        match event {
            tauri::WindowEvent::CloseRequested { .. } | tauri::WindowEvent::Destroyed => true,
            tauri::WindowEvent::Focused(true) => {
                focused_once.store(true, Ordering::SeqCst);
                false
            }
            tauri::WindowEvent::Focused(false) => focused_once.load(Ordering::SeqCst),
            _ => false,
        }
    }

    pub(super) fn close<R: Runtime>(app: &tauri::AppHandle<R>) {
        let handle = app.clone();
        let _ = app.run_on_main_thread(move || {
            if let Some(window) = handle.get_webview_window(MCP_CONFIRM_LABEL) {
                let _ = window.destroy();
            }
        });
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

    fn item(name: &str, token: &str) -> ConfirmationListItem {
        ConfirmationListItem {
            name: name.into(),
            kind: "cask".into(),
            token: token.into(),
            tap: None,
            disabled: false,
            installed: false,
            already_queued: false,
            unsupported: false,
            duplicate: false,
        }
    }

    #[test]
    fn deep_link_view_names_the_web_link_and_target() {
        let request = request("cask/ghostty");
        let view = McpConfirmView::deep_link_install(&request, "Ghostty\u{202e}");
        assert_eq!(view.origin, "deepLink");
        assert_eq!(view.action, "install");
        assert_eq!(
            view.name, "Ghostty",
            "display_value strips hidden characters"
        );
        assert_eq!(view.target, "cask/ghostty");
        assert_eq!(view.source.as_deref(), Some(request.source.as_str()));
        assert!(view.items.is_empty() && view.signer.is_none());
    }

    #[test]
    fn collection_view_lists_every_app_with_its_skip_reason_and_counts() {
        let mut request = request("collection/dev-setup");
        request.action = "install collection".into();
        let items = vec![
            item("Ghostty", "ghostty"),
            ConfirmationListItem {
                disabled: true,
                ..item("Legacy app", "legacy")
            },
            ConfirmationListItem {
                installed: true,
                ..item("Installed app", "installed")
            },
            ConfirmationListItem {
                already_queued: true,
                ..item("Queued app", "queued")
            },
            ConfirmationListItem {
                duplicate: true,
                ..item("Duplicate app", "dup")
            },
            ConfirmationListItem {
                unsupported: true,
                kind: "formula".into(),
                ..item("Formula", "ripgrep")
            },
            ConfirmationListItem {
                tap: Some("owner/third-party".into()),
                ..item("Same App", "different")
            },
        ];
        let view = McpConfirmView::deep_link_collection(&request, "Dev setup", &items, 2).unwrap();
        assert_eq!(view.action, "installCollection");
        assert_eq!(view.install_count, 2);
        let rows: Vec<_> = view
            .items
            .iter()
            .map(|item| (item.target.as_str(), item.skip.as_deref()))
            .collect();
        assert_eq!(
            rows,
            [
                ("cask/ghostty", None),
                ("cask/legacy", Some("disabled")),
                ("cask/installed", Some("installed")),
                ("cask/queued", Some("queued")),
                ("cask/dup", Some("duplicate")),
                ("formula/ripgrep", Some("unsupported")),
                ("cask/different [owner/third-party]", None),
            ]
        );
        // Counts must match the list the grant binds.
        for (items, count) in [(&items[..], 3), (&items[..0], 1), (&items[..1], 0)] {
            assert!(McpConfirmView::deep_link_collection(&request, "x", items, count).is_err());
        }
        assert!(McpConfirmView::deep_link_collection(&request, "x", &[], 0).is_ok());
    }

    #[test]
    fn leaving_the_window_after_focusing_it_denies() {
        use std::sync::atomic::AtomicBool;
        let focused = AtomicBool::new(false);
        let denies = |event| confirm_window::denies(&event, &focused);
        assert!(
            !denies(tauri::WindowEvent::Focused(false)),
            "never focused: not an answer"
        );
        assert!(!denies(tauri::WindowEvent::Focused(true)));
        assert!(denies(tauri::WindowEvent::Focused(false)));
        assert!(denies(tauri::WindowEvent::Destroyed));
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

    /// A destroyed webview clears the slot of a confirmation that already timed out, and it must also
    /// drop every grant that survived the timeout: an earlier approval would otherwise still be
    /// usable after the window is gone (#85). The same confirmation id is never reusable, and a
    /// second cancel_active is a no-op.
    #[test]
    fn webview_destruction_after_a_timeout_revokes_earlier_grants() {
        let service = ConfirmationService::default();
        let approved = request("cask/ghostty");
        let pending = service.test_begin(approved.clone()).unwrap();
        let grant = service.test_finish(pending.id, true).unwrap();
        let timed_out = service.test_begin(request("cask/zed")).unwrap();
        let timed_out_id = timed_out.id;
        service.test_timeout(timed_out_id);
        assert!(
            service
                .state
                .lock()
                .unwrap()
                .grants
                .contains_key(&grant.token),
            "a timeout keeps grants issued by an earlier confirmation"
        );

        service.cancel_active();

        assert_eq!(
            service.consume(&grant, &approved).unwrap_err().code,
            "E_CONFIRMATION_REUSED"
        );
        let state = service.state.lock().unwrap();
        assert!(
            state.grants.is_empty(),
            "no grant survives the destroyed webview"
        );
        assert!(state.active.is_none(), "the slot is free, not left busy");
        drop(state);
        assert_eq!(
            service.test_finish(timed_out_id, true).unwrap_err().code,
            "E_CONFIRMATION_TIMEOUT",
            "the timed-out id cannot be answered after the window is gone"
        );
        assert!(service.test_begin(request("cask/zed")).is_ok());

        // The webview can report its destruction more than once.
        service.cancel_active();
        assert!(service.state.lock().unwrap().grants.is_empty());
        assert!(service.state.lock().unwrap().active.is_none());
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

    #[tokio::test]
    async fn revoking_a_caller_ends_its_wait_and_never_grants() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let mut request = request("cask/iina");
        request.caller = "local mcp".into();
        let pending = service.begin(request.clone()).expect("begin");
        let id = pending.id;
        let (sender, receiver) = oneshot::channel();
        let guard = ConfirmationDropGuard::new(std::sync::Arc::clone(&service), id);
        let waiting = {
            let service = std::sync::Arc::clone(&service);
            tokio::spawn(async move { service.wait_for_reply(pending, receiver, guard).await })
        };
        assert!(!service.revoke_caller("deep link"));
        assert!(service.revoke_caller("local mcp"));
        let result = tokio::time::timeout(Duration::from_secs(1), waiting)
            .await
            .expect("ended")
            .expect("join");
        assert_eq!(
            result.expect_err("revoked").code,
            "E_CONFIRMATION_INVALIDATED"
        );
        // A late Allow on the still-visible dialog only frees the slot.
        service.finish_reply(id, true, |late| {
            assert!(late.is_err());
            let _ = sender.send(late);
        });
        assert!(service.begin(request).is_ok());
    }

    fn mcp_request() -> ConfirmationRequest {
        let mut request = request("cask/iina");
        request.caller = "local mcp".into();
        request
    }

    fn view() -> McpConfirmView {
        McpConfirmView::new(
            "install",
            "IINA\u{202E}",
            "cask/iina",
            None,
            None,
            None,
            Some("/opt/homebrew/bin/node\u{2028}"),
            Some("Cursor"),
            Some("cursor\u{200B}-vscode"),
        )
    }

    #[test]
    fn window_view_is_filtered_and_has_a_one_time_id() {
        let service = ConfirmationService::default();
        let pending = service.begin(mcp_request()).expect("begin");
        let _receiver = service.open_prompt(pending.id, view()).expect("prompt");
        let shown = service.window_view().expect("view");
        assert_eq!(shown.name, "IINA");
        assert_eq!(shown.caller_path.as_deref(), Some("/opt/homebrew/bin/node"));
        assert_eq!(shown.claimed_name.as_deref(), Some("cursor-vscode"));
        assert!(!shown.id.is_empty());
        assert!(shown.allow_after_ms > 0 && shown.allow_after_ms <= 1000);
    }

    #[tokio::test]
    async fn allow_is_refused_before_the_arm_delay_and_ids_are_single_use() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let pending = service.begin(mcp_request()).expect("begin");
        let receiver = service.open_prompt(pending.id, view()).expect("prompt");
        // Not shown yet: Allow is never accepted.
        let id = service
            .state
            .lock()
            .unwrap()
            .prompt
            .as_ref()
            .unwrap()
            .view
            .id
            .clone();
        assert_eq!(
            service.answer_window(&id, true).unwrap_err().code,
            "E_CONFIRMATION_TOO_EARLY"
        );
        let shown = service.window_view().expect("view");
        assert_eq!(
            service.answer_window(&shown.id, true).unwrap_err().code,
            "E_CONFIRMATION_TOO_EARLY"
        );
        assert_eq!(
            service.answer_window("forged", false).unwrap_err().code,
            "E_CONFIRMATION_EXPIRED"
        );
        service
            .state
            .lock()
            .unwrap()
            .prompt
            .as_mut()
            .unwrap()
            .shown_at = Some(Instant::now() - ALLOW_ARM_DELAY);
        service.answer_window(&shown.id, true).expect("allowed");
        let grant = receiver.await.expect("reply").expect("grant");
        service.consume(&grant, &mcp_request()).expect("consume");
        assert_eq!(
            service.answer_window(&shown.id, true).unwrap_err().code,
            "E_CONFIRMATION_EXPIRED"
        );
    }

    #[tokio::test]
    async fn events_from_an_older_window_never_deny_a_newer_request() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let old = service.begin(mcp_request()).expect("begin").id;
        let old_reply = service.open_prompt(old, view()).expect("prompt");
        service.deny_window(old);
        assert!(old_reply.await.expect("reply").is_err());
        service.release_prompt(old);

        let newer = service.begin(mcp_request()).expect("begin again").id;
        let mut reply = service.open_prompt(newer, view()).expect("prompt again");
        service.deny_window(old);
        assert!(
            reply.try_recv().is_err(),
            "stale Destroyed must not answer the new request"
        );
        let shown = service.window_view().expect("still open");
        service
            .state
            .lock()
            .unwrap()
            .prompt
            .as_mut()
            .unwrap()
            .shown_at = Some(Instant::now() - ALLOW_ARM_DELAY);
        service.answer_window(&shown.id, true).expect("allowed");
        assert!(reply.await.expect("reply").is_ok());
    }

    #[tokio::test]
    async fn closing_the_window_denies_and_release_frees_the_slot() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let pending = service.begin(mcp_request()).expect("begin");
        let id = pending.id;
        let receiver = service.open_prompt(id, view()).expect("prompt");
        service.deny_window(id);
        assert_eq!(
            receiver.await.expect("reply").unwrap_err().code,
            "E_CONFIRMATION_DENIED"
        );
        service.release_prompt(id);
        assert!(service.window_view().is_none());
        assert!(
            service.begin(request("cask/zed")).is_ok(),
            "slot released for deep links"
        );
    }

    #[tokio::test]
    async fn revoking_with_the_window_open_frees_the_slot_once_released() {
        let service = std::sync::Arc::new(ConfirmationService::default());
        let pending = service.begin(mcp_request()).expect("begin");
        let id = pending.id;
        let _receiver = service.open_prompt(id, view()).expect("prompt");
        let shown = service.window_view().expect("view");
        assert!(service.revoke_caller("local mcp"));
        service.release_prompt(id);
        assert_eq!(
            service.answer_window(&shown.id, true).unwrap_err().code,
            "E_CONFIRMATION_EXPIRED"
        );
        assert!(service.begin(request("cask/zed")).is_ok());
    }

    #[test]
    fn the_window_only_navigates_to_the_bundled_app() {
        use super::confirm_window::allowed_navigation;
        let ok = |url: &str| allowed_navigation(&tauri::Url::parse(url).unwrap());
        assert!(ok("tauri://localhost/index.html#/mcp-confirm"));
        assert!(ok("http://tauri.localhost/index.html"));
        assert!(!ok("https://example.com/"));
        assert!(!ok("file:///etc/passwd"));
        assert!(!ok("https://localhost.evil.com/"));
    }
}
