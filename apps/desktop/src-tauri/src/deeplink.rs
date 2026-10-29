use crate::{
    brew::args::validate_token,
    error::AppError,
    events::{CoreEvent, EventSink},
    model::*,
};
use std::{
    collections::{HashMap, VecDeque},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
use tauri::Manager;
use uuid::Uuid;

pub fn parse(input: &str) -> Option<DeepLinkEvent> {
    if input.len() > 8192 || input.chars().any(char::is_control) || input.trim() != input {
        return None;
    }
    let authority = input.split_once("://")?.1.split(['?', '#']).next()?;
    if let Some((_, path)) = authority.split_once('/')
        && path.split('/').any(|segment| {
            segment.is_empty() || matches!(segment, "." | "..") || segment.contains('%')
        })
    {
        return None;
    }
    let url = reqwest::Url::parse(input).ok()?;
    if url.scheme() != "opennavo"
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
        || url.fragment().is_some()
    {
        return None;
    }
    let segments: Vec<_> = url
        .path()
        .trim_start_matches('/')
        .split('/')
        .filter(|s| !s.is_empty())
        .collect();
    if url.path().ends_with('/') && !url.path().is_empty() {
        return None;
    }
    let query: Vec<_> = url.query_pairs().collect();
    let mut action = None;
    for (name, value) in &query {
        if name == "action" {
            if action.is_some() || value != "install" {
                return None;
            }
            action = Some(DeepLinkAction::Install);
        }
    }
    let route = match url.host_str()? {
        "package" if segments.len() == 2 => {
            if !matches!(segments[0], "cask" | "formula" | "link")
                || validate_token(segments[1]).is_err()
                || query.iter().any(|(key, _)| key != "action")
            {
                return None;
            }
            if matches!(segments[0], "formula" | "link") {
                action = None;
            }
            format!("/package/{}/{}", segments[0], segments[1])
        }
        "collection" if segments.len() == 1 => {
            let slug = segments[0];
            let bytes = slug.as_bytes();
            if bytes.is_empty()
                || bytes.len() > 64
                || !bytes[0].is_ascii_lowercase() && !bytes[0].is_ascii_digit()
                || !bytes
                    .iter()
                    .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || *b == b'-')
                || query.iter().any(|(key, _)| key != "action")
            {
                return None;
            }
            format!("/collection/{slug}")
        }
        "search" if segments.is_empty() && action.is_none() => {
            if query.len() != 1 || query[0].0 != "q" {
                return None;
            }
            let value: String = query[0].1.chars().take(64).collect();
            let mut encoded = reqwest::Url::parse("http://localhost/").ok()?;
            encoded.query_pairs_mut().append_pair("q", &value);
            format!("/search?{}", encoded.query()?)
        }
        "startup" if segments.is_empty() && action.is_none() => {
            if query.len() != 1 || query[0].0 != "label" {
                return None;
            }
            let label = query[0].1.as_ref();
            if label.is_empty()
                || label.len() > 255
                || label.trim() != label
                || label.chars().any(char::is_control)
                || label.contains(['/', '\\', '?', '#'])
            {
                return None;
            }
            let mut route = reqwest::Url::parse("http://localhost/security/startup").ok()?;
            route
                .query_pairs_mut()
                .append_pair("label", label)
                .append_pair("preview", "1");
            format!("{}?{}", route.path(), route.query()?)
        }
        "uninstall" if segments.len() == 1 && action.is_none() => {
            if validate_token(segments[0]).is_err()
                || query.len() != 1
                || query[0].0 != "preview"
                || query[0].1 != "1"
            {
                return None;
            }
            // Only the preview page may ask the user to authorize the existing uninstall flow.
            format!("/uninstall/{}?preview=1", segments[0])
        }
        "scan" if segments.len() == 1 && action.is_none() => {
            if validate_token(segments[0]).is_err()
                || query.len() != 1
                || query[0].0 != "preview"
                || query[0].1 != "1"
            {
                return None;
            }
            // Navigation only: resolve the catalog token locally and ask the user to start a scan.
            let mut route = reqwest::Url::parse("http://localhost/security/scan").ok()?;
            route
                .query_pairs_mut()
                .append_pair("token", segments[0])
                .append_pair("preview", "1");
            format!("{}?{}", route.path(), route.query()?)
        }
        "updates" if segments.is_empty() && query.is_empty() => "/updates".into(),
        _ => return None,
    };
    Some(DeepLinkEvent {
        url: input.into(),
        route,
        action,
        pending_id: None,
    })
}

/// Keep only unique deep links that pass the same validation used by the router.
pub fn validated_links<I, S>(inputs: I) -> Vec<String>
where
    I: IntoIterator<Item = S>,
    S: AsRef<str>,
{
    let mut links = Vec::new();
    for input in inputs {
        let input = input.as_ref();
        if parse(input).is_some() && !links.iter().any(|link| link == input) {
            links.push(input.to_owned());
        }
    }
    links
}

#[cfg(target_os = "macos")]
pub(crate) fn startup_links(app: &tauri::AppHandle) -> Vec<String> {
    let current = app
        .try_state::<tauri_plugin_deep_link::DeepLink<tauri::Wry>>()
        .and_then(|plugin| plugin.get_current().ok().flatten())
        .unwrap_or_default();
    validated_links(
        current
            .into_iter()
            .map(|url| url.to_string())
            .chain(std::env::args()),
    )
}

struct State {
    ready: bool,
    pending: VecDeque<DeepLinkEvent>,
    recent: VecDeque<(String, Instant)>,
    installs: HashMap<String, PendingInstallState>,
    install_order: VecDeque<String>,
}

#[derive(Debug, Clone)]
enum PendingInstallKind {
    Single {
        target: TaskTarget,
    },
    Collection {
        slug: String,
        plan: Option<CollectionInstallPlan>,
        resolving: bool,
    },
}

#[derive(Debug, Clone)]
struct PendingInstallState {
    source: String,
    current: bool,
    kind: PendingInstallKind,
}

#[derive(Debug, Clone, PartialEq)]
pub struct CollectionInstallItem {
    pub name: String,
    pub disabled: bool,
    pub installable: bool,
}

#[derive(Debug, Clone, PartialEq)]
pub struct CollectionInstallPlan {
    pub items: Vec<CollectionInstallItem>,
    pub targets: Vec<TaskTarget>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct InstallTarget {
    pub source: String,
    pub target: TaskTarget,
    pub index: usize,
    pub total: usize,
}

#[derive(Debug, Clone, PartialEq)]
pub enum InstallStep {
    ResolveCollection {
        source: String,
        slug: String,
    },
    Collection {
        source: String,
        slug: String,
        plan: CollectionInstallPlan,
    },
    Target(InstallTarget),
}

pub struct Router {
    state: Mutex<State>,
    sink: EventSink,
    show: Arc<dyn Fn() + Send + Sync>,
}
impl Router {
    pub fn new(sink: EventSink, show: Arc<dyn Fn() + Send + Sync>) -> Arc<Self> {
        Arc::new(Self {
            state: Mutex::new(State {
                ready: false,
                pending: VecDeque::new(),
                recent: VecDeque::new(),
                installs: HashMap::new(),
                install_order: VecDeque::new(),
            }),
            sink,
            show,
        })
    }
    pub fn receive(&self, input: &str) {
        (self.show)();
        let Some(mut event) = parse(input) else {
            return;
        };
        let Ok(mut state) = self.state.lock() else {
            return;
        };
        let now = Instant::now();
        state
            .recent
            .retain(|(_, at)| now.duration_since(*at) < Duration::from_secs(2));
        if state.recent.iter().any(|(url, _)| url == &event.url) {
            return;
        }
        if state.recent.len() >= 32 {
            state.recent.pop_front();
        }
        state.recent.push_back((event.url.clone(), now));
        if event.action == Some(DeepLinkAction::Install) {
            let Some(install) = pending_install(&event) else {
                return;
            };
            while state.installs.len() >= 32 {
                let Some(oldest) = state.install_order.pop_front() else {
                    break;
                };
                state.installs.remove(&oldest);
            }
            let pending_id = Uuid::new_v4().to_string();
            state.installs.insert(pending_id.clone(), install);
            state.install_order.push_back(pending_id.clone());
            event.pending_id = Some(pending_id);
        }
        if state.ready {
            drop(state);
            (self.sink)(CoreEvent::Deeplink(event));
        } else {
            if state.pending.len() >= 32 {
                state.pending.pop_front();
            }
            state.pending.push_back(event);
        }
    }
    pub fn receive_args(&self, args: &[String]) {
        let links: Vec<_> = args
            .iter()
            .filter(|arg| arg.starts_with("opennavo:"))
            .collect();
        if links.is_empty() {
            (self.show)();
        }
        for input in links {
            self.receive(input);
        }
    }
    pub fn loading(&self) {
        if let Ok(mut state) = self.state.lock() {
            state.ready = false;
        }
    }
    pub fn ready(&self) {
        let Ok(mut state) = self.state.lock() else {
            return;
        };
        state.ready = true;
        let pending: Vec<_> = state.pending.drain(..).collect();
        drop(state);
        for event in pending {
            (self.sink)(CoreEvent::Deeplink(event));
        }
    }
    pub fn navigate(&self, route: &str) {
        (self.show)();
        let event = DeepLinkEvent {
            url: format!("opennavo://{}", route.trim_start_matches('/')),
            route: route.into(),
            action: None,
            pending_id: None,
        };
        let Ok(mut state) = self.state.lock() else {
            return;
        };
        if state.ready {
            drop(state);
            (self.sink)(CoreEvent::Deeplink(event));
        } else {
            if state.pending.len() >= 32 {
                state.pending.pop_front();
            }
            state.pending.push_back(event);
        }
    }

    /// Reserve the next target for an opaque deep-link id. The reservation prevents concurrent frontend calls from
    /// asking for the same item twice while a native dialog is open.
    pub fn begin_install(&self, pending_id: &str) -> Result<InstallStep, AppError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "deeplink_state"))?;
        let pending = state
            .installs
            .get_mut(pending_id)
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "deeplink_pending_missing"))?;
        if pending.current {
            return Err(AppError::new("E_BUSY", "confirmation_busy"));
        }
        match &mut pending.kind {
            PendingInstallKind::Single { target } => {
                pending.current = true;
                Ok(InstallStep::Target(InstallTarget {
                    source: pending.source.clone(),
                    target: target.clone(),
                    index: 0,
                    total: 1,
                }))
            }
            PendingInstallKind::Collection {
                slug,
                plan,
                resolving,
            } => {
                let Some(plan) = plan.as_ref() else {
                    if *resolving {
                        return Err(AppError::new("E_BUSY", "confirmation_busy"));
                    }
                    *resolving = true;
                    return Ok(InstallStep::ResolveCollection {
                        source: pending.source.clone(),
                        slug: slug.clone(),
                    });
                };
                pending.current = true;
                Ok(InstallStep::Collection {
                    source: pending.source.clone(),
                    slug: slug.clone(),
                    plan: plan.clone(),
                })
            }
        }
    }

    pub fn set_collection_plan(
        &self,
        pending_id: &str,
        plan: CollectionInstallPlan,
    ) -> Result<(), AppError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "deeplink_state"))?;
        let pending = state
            .installs
            .get_mut(pending_id)
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "deeplink_pending_missing"))?;
        match &mut pending.kind {
            PendingInstallKind::Collection {
                plan: stored,
                resolving,
                ..
            } => {
                *stored = Some(plan);
                *resolving = false;
                Ok(())
            }
            PendingInstallKind::Single { .. } => {
                Err(AppError::new("E_INVALID_ARG", "deeplink_not_collection"))
            }
        }
    }

    /// Release a reservation after a busy/error result so the same id can be retried. Denials cancel the id via
    /// cancel_install, which also drops the remaining collection items.
    pub fn release_install(&self, pending_id: &str) {
        if let Ok(mut state) = self.state.lock()
            && let Some(pending) = state.installs.get_mut(pending_id)
        {
            pending.current = false;
            if let PendingInstallKind::Collection { resolving, .. } = &mut pending.kind {
                *resolving = false;
            }
        }
    }

    pub fn finish_install(&self, pending_id: &str) -> Result<usize, AppError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| AppError::new("E_UNKNOWN", "deeplink_state"))?;
        let Some(pending) = state.installs.get_mut(pending_id) else {
            return Err(AppError::new("E_CONFIRMATION_REUSED", "deeplink_consumed"));
        };
        if !pending.current {
            return Err(AppError::new("E_INVALID_ARG", "deeplink_not_reserved"));
        }
        pending.current = false;
        Self::remove_install(&mut state, pending_id);
        Ok(0)
    }

    pub fn cancel_install(&self, pending_id: &str) {
        if let Ok(mut state) = self.state.lock() {
            Self::remove_install(&mut state, pending_id);
        }
    }

    fn remove_install(state: &mut State, pending_id: &str) {
        state.installs.remove(pending_id);
        if let Some(position) = state.install_order.iter().position(|id| id == pending_id) {
            state.install_order.remove(position);
        }
    }
}

fn pending_install(event: &DeepLinkEvent) -> Option<PendingInstallState> {
    let parts: Vec<_> = event.route.trim_start_matches('/').split('/').collect();
    let kind = match parts.as_slice() {
        ["package", "cask", _token] => TaskKind::Cask,
        ["package", "formula", _token] => TaskKind::Formula,
        ["collection", slug] => {
            return Some(PendingInstallState {
                source: event.url.clone(),
                current: false,
                kind: PendingInstallKind::Collection {
                    slug: (*slug).into(),
                    plan: None,
                    resolving: false,
                },
            });
        }
        _ => return None,
    };
    let token = parts.get(2)?.to_string();
    Some(PendingInstallState {
        source: event.url.clone(),
        current: false,
        kind: PendingInstallKind::Single {
            target: TaskTarget { kind, token },
        },
    })
}
struct WindowFit {
    minimum: tauri::LogicalSize<f64>,
    target: tauri::LogicalSize<f64>,
    position: tauri::LogicalPosition<f64>,
}

fn main_window_fit(
    inner: tauri::LogicalSize<f64>,
    outer: tauri::LogicalSize<f64>,
    area: tauri::LogicalSize<f64>,
    origin: tauri::LogicalPosition<f64>,
    position: tauri::LogicalPosition<f64>,
    welcome: bool,
) -> WindowFit {
    let frame_width = (outer.width - inner.width).max(0.0);
    let frame_height = (outer.height - inner.height).max(0.0);
    let max_width = (area.width - frame_width).floor().max(1.0);
    let max_height = (area.height - frame_height).floor().max(1.0);
    let (min_width, min_height): (f64, f64) = if welcome {
        (540.0, 600.0)
    } else {
        (1100.0, 700.0)
    };
    let minimum = tauri::LogicalSize::new(min_width.min(max_width), min_height.min(max_height));
    let requested = if welcome {
        tauri::LogicalSize::new(604.0, 820.0)
    } else {
        inner
    };
    let target = tauri::LogicalSize::new(
        requested.width.clamp(minimum.width, max_width),
        requested.height.clamp(minimum.height, max_height),
    );
    let remaining_width = (area.width - target.width - frame_width).max(0.0);
    let remaining_height = (area.height - target.height - frame_height).max(0.0);
    let position = if welcome {
        tauri::LogicalPosition::new(
            origin.x + remaining_width / 2.0,
            origin.y + remaining_height / 2.0,
        )
    } else {
        tauri::LogicalPosition::new(
            position.x.clamp(origin.x, origin.x + remaining_width),
            position.y.clamp(origin.y, origin.y + remaining_height),
        )
    };
    WindowFit {
        minimum,
        target,
        position,
    }
}

// Calculate final bounds before any setters: macOS may apply window changes asynchronously.
fn fit_main_to_work_area<R: tauri::Runtime>(
    window: &tauri::WebviewWindow<R>,
    welcome: bool,
) -> tauri::Result<()> {
    let Some(monitor) = window.current_monitor()?.or(window.primary_monitor()?) else {
        return Ok(());
    };
    let scale = window.scale_factor()?;
    let area = monitor.work_area();
    let inner = window.inner_size()?.to_logical::<f64>(scale);
    let position = window.outer_position()?.to_logical::<f64>(scale);
    let fit = main_window_fit(
        inner,
        window.outer_size()?.to_logical::<f64>(scale),
        area.size.to_logical::<f64>(monitor.scale_factor()),
        area.position.to_logical::<f64>(monitor.scale_factor()),
        position,
        welcome,
    );
    window.set_min_size(Some(fit.minimum))?;
    if welcome || fit.target != inner {
        window.set_size(fit.target)?;
    }
    if welcome || fit.position != position {
        window.set_position(fit.position)?;
    }
    Ok(())
}

pub fn show_main<R: tauri::Runtime>(app: &tauri::AppHandle<R>) {
    // Explicit navigation/Dock requests supersede transient menu-bar panels.
    // They no longer close through Focused(false) on macOS.
    crate::tray::hide(app);
    crate::monitor::popover::hide(app);
    if let Some(window) = app.get_webview_window("main") {
        // Resize to Welcome before first display to avoid flashing the full app frame.
        let welcome = app
            .try_state::<std::sync::Arc<crate::core::DesktopCore>>()
            .and_then(|core| {
                core.settings
                    .read()
                    .ok()
                    .map(|settings| !settings.onboarding_completed)
            })
            .unwrap_or(false);
        if welcome {
            let _ = window.unmaximize();
        }
        let _ = window.unminimize();
        if let Err(error) = fit_main_to_work_area(&window, welcome) {
            log::warn!("Could not fit main window to monitor work area: {error}");
        }
        let _ = window.show();
        let _ = window.set_focus();
    }
}
pub fn install(app: &tauri::AppHandle, sink: EventSink, relaunch_links: Vec<String>) {
    use tauri_plugin_deep_link::DeepLinkExt;
    let handle = app.clone();
    let router = Router::new(sink, Arc::new(move || show_main(&handle)));
    app.manage(router.clone());
    let incoming = router.clone();
    app.deep_link().on_open_url(move |event| {
        for url in event.urls() {
            incoming.receive(url.as_str());
        }
    });
    for link in relaunch_links {
        router.receive(&link);
    }
    if let Ok(Some(urls)) = app.deep_link().get_current() {
        for url in urls {
            router.receive(url.as_str());
        }
    }
    for arg in std::env::args().filter(|arg| arg.starts_with("opennavo:")) {
        router.receive(&arg);
    }
}

#[cfg(test)]
mod window_fit_tests {
    use super::main_window_fit;
    use tauri::{LogicalPosition, LogicalSize};

    #[test]
    fn welcome_redisplay_keeps_the_requested_size_inside_the_work_area() {
        let fit = main_window_fit(
            LogicalSize::new(604.0, 750.0),
            LogicalSize::new(604.0, 750.0),
            LogicalSize::new(1512.0, 750.0),
            LogicalPosition::new(0.0, 24.0),
            LogicalPosition::new(454.0, 24.0),
            true,
        );
        assert_eq!(fit.target, LogicalSize::new(604.0, 750.0));
        assert_eq!(fit.position, LogicalPosition::new(454.0, 24.0));
    }

    #[test]
    fn restoring_minimum_size_moves_the_expanded_window_inside_the_work_area() {
        let fit = main_window_fit(
            LogicalSize::new(1000.0, 650.0),
            LogicalSize::new(1000.0, 650.0),
            LogicalSize::new(1512.0, 900.0),
            LogicalPosition::new(0.0, 0.0),
            LogicalPosition::new(512.0, 250.0),
            false,
        );
        assert_eq!(fit.minimum, LogicalSize::new(1100.0, 700.0));
        assert_eq!(fit.target, LogicalSize::new(1100.0, 700.0));
        assert_eq!(fit.position, LogicalPosition::new(412.0, 200.0));
    }

    #[test]
    fn tiny_work_area_caps_minimum_and_reserves_native_frame_space() {
        let fit = main_window_fit(
            LogicalSize::new(1400.0, 820.0),
            LogicalSize::new(1410.0, 840.0),
            LogicalSize::new(1000.0, 650.0),
            LogicalPosition::new(-1000.0, 30.0),
            LogicalPosition::new(0.0, 0.0),
            false,
        );
        assert_eq!(fit.minimum, LogicalSize::new(990.0, 630.0));
        assert_eq!(fit.target, fit.minimum);
        assert_eq!(fit.position, LogicalPosition::new(-1000.0, 30.0));
    }
}

#[cfg(test)]
mod safety_preview_tests {
    #[test]
    fn startup_and_uninstall_links_never_request_execution() {
        let startup = super::parse("opennavo://startup?label=com.example.shared").unwrap();
        assert_eq!(
            startup.route,
            "/security/startup?label=com.example.shared&preview=1"
        );
        assert!(startup.action.is_none());
        let uninstall = super::parse("opennavo://uninstall/example?preview=1").unwrap();
        assert_eq!(uninstall.route, "/uninstall/example?preview=1");
        assert!(uninstall.action.is_none());
        for url in [
            "opennavo://startup?label=x&label=y",
            "opennavo://startup?label=x&execute=1",
            "opennavo://startup?label=%0Aroot",
            "opennavo://uninstall/example",
            "opennavo://uninstall/example?preview=1&delete=1",
            "opennavo://uninstall/example?preview=1&action=install",
        ] {
            assert!(super::parse(url).is_none(), "{url}");
        }
    }

    #[test]
    fn scan_deep_links_only_navigate_to_preview() {
        let event = super::parse("opennavo://scan/example+pro?preview=1").unwrap();
        assert_eq!(event.route, "/security/scan?token=example%2Bpro&preview=1");
        assert!(event.action.is_none());
        for url in [
            "opennavo://scan/example",
            "opennavo://scan/example?preview=0",
            "opennavo://scan/example?preview=1&preview=1",
            "opennavo://scan/example?preview=1&path=/tmp/file",
            "opennavo://scan/example?preview=1&action=install",
            "opennavo://scan/../example?preview=1",
            "opennavo://scan/example%2Fother?preview=1",
        ] {
            assert!(super::parse(url).is_none(), "{url}");
        }
    }
}

#[cfg(test)]
mod install_request_tests {
    use super::*;

    fn router(events: Arc<Mutex<Vec<CoreEvent>>>) -> Arc<Router> {
        Router::new(
            Arc::new(move |event| events.lock().unwrap().push(event)),
            Arc::new(|| {}),
        )
    }

    #[test]
    fn package_install_is_opaque_and_requires_the_rust_pending_id() {
        let events = Arc::new(Mutex::new(Vec::new()));
        let router = router(events.clone());
        router.ready();
        router.receive("opennavo://package/cask/ghostty?action=install");
        let event = match &events.lock().unwrap()[0] {
            CoreEvent::Deeplink(event) => event.clone(),
            _ => panic!("expected deep link"),
        };
        let pending_id = event.pending_id.expect("Rust creates an opaque pending id");
        let InstallStep::Target(step) = router.begin_install(&pending_id).unwrap() else {
            panic!("single package must be ready");
        };
        assert_eq!(
            step.target,
            TaskTarget {
                kind: TaskKind::Cask,
                token: "ghostty".into()
            }
        );
        assert_eq!(
            router.begin_install(&pending_id).unwrap_err().code,
            "E_BUSY"
        );
        assert_eq!(router.finish_install(&pending_id).unwrap(), 0);
        assert_eq!(
            router.begin_install(&pending_id).unwrap_err().code,
            "E_INVALID_ARG"
        );
    }

    #[test]
    fn collection_install_reserves_one_ordered_plan() {
        let events = Arc::new(Mutex::new(Vec::new()));
        let router = router(events.clone());
        router.ready();
        router.receive("opennavo://collection/dev-setup?action=install");
        let event = match &events.lock().unwrap()[0] {
            CoreEvent::Deeplink(event) => event.clone(),
            _ => panic!("expected deep link"),
        };
        let pending_id = event.pending_id.unwrap();
        assert!(matches!(
            router.begin_install(&pending_id).unwrap(),
            InstallStep::ResolveCollection { .. }
        ));
        router
            .set_collection_plan(
                &pending_id,
                CollectionInstallPlan {
                    items: vec![CollectionInstallItem {
                        name: "Zed".into(),
                        disabled: false,
                        installable: true,
                    }],
                    targets: vec![TaskTarget {
                        kind: TaskKind::Cask,
                        token: "zed".into(),
                    }],
                },
            )
            .unwrap();
        let InstallStep::Collection { plan, slug, .. } = router.begin_install(&pending_id).unwrap()
        else {
            panic!("collection plan must be ready");
        };
        assert_eq!(slug, "dev-setup");
        assert_eq!(plan.targets.len(), 1);
        assert_eq!(router.finish_install(&pending_id).unwrap(), 0);
        assert_eq!(
            router.begin_install(&pending_id).unwrap_err().code,
            "E_INVALID_ARG"
        );
    }
}
