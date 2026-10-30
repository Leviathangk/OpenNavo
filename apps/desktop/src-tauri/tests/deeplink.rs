use opennavo_desktop_lib::{deeplink, events::CoreEvent, model::*};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};

#[test]
fn deeplink_confirmation_acl_is_main_local_only() {
    let mut context: tauri::Context<tauri::Wry> = tauri::generate_context!();
    let authority = context.runtime_authority_mut();
    use tauri::ipc::Origin;
    assert!(
        authority
            .resolve_access("deeplink_install_confirm", "main", "main", &Origin::Local)
            .is_some()
    );
    for label in [
        "tray",
        "permission-guide",
        "tray-monitor",
        "firewall-alert",
        "foreign",
    ] {
        assert!(
            authority
                .resolve_access("deeplink_install_confirm", label, label, &Origin::Local)
                .is_none()
        );
    }
    assert!(
        authority
            .resolve_access(
                "deeplink_install_confirm",
                "main",
                "main",
                &Origin::Remote {
                    url: "https://example.test".parse().unwrap()
                }
            )
            .is_none()
    );
}

#[test]
fn destroying_webview_clears_active_router_reservations() {
    let events = Arc::new(Mutex::new(Vec::new()));
    let saved = events.clone();
    let router = deeplink::Router::new(
        Arc::new(move |event| saved.lock().unwrap().push(event)),
        Arc::new(|| {}),
    );
    router.ready();
    router.receive("opennavo://package/cask/zed?action=install");
    router.receive("opennavo://collection/dev-setup?action=install");
    let ids: Vec<_> = events
        .lock()
        .unwrap()
        .iter()
        .map(|event| match event {
            CoreEvent::Deeplink(event) => event.pending_id.clone().unwrap(),
            _ => panic!("expected deep link"),
        })
        .collect();
    for id in &ids {
        router.begin_install(id).unwrap();
    }
    router.cancel_active_installs();
    for id in &ids {
        assert_eq!(router.begin_install(id).unwrap_err().code, "E_INVALID_ARG");
    }
}
#[test]
fn routes_actions_encoding_and_bounded_query() {
    for (url, route, install) in [
        (
            "opennavo://package/cask/visual-studio-code",
            "/package/cask/visual-studio-code",
            false,
        ),
        (
            "opennavo://package/formula/ripgrep?action=install",
            "/package/formula/ripgrep",
            false,
        ),
        (
            "opennavo://collection/new-mac",
            "/collection/new-mac",
            false,
        ),
        (
            "opennavo://collection/new-mac?action=install",
            "/collection/new-mac",
            true,
        ),
        ("opennavo://updates", "/updates", false),
        (
            "opennavo://search?q=hello%20world",
            "/search?q=hello+world",
            false,
        ),
    ] {
        let event = deeplink::parse(url).unwrap();
        assert_eq!(event.route, route);
        assert_eq!(
            event.action,
            if install {
                Some(DeepLinkAction::Install)
            } else {
                None
            }
        );
    }
    let url = format!("opennavo://search?q={}", "微信".repeat(80));
    let event = deeplink::parse(&url).unwrap();
    let route = reqwest::Url::parse(&format!("http://localhost{}", event.route)).unwrap();
    assert_eq!(route.query_pairs().next().unwrap().1.chars().count(), 64);
    for invalid in [
        "https://evil.test/updates",
        "opennavo://user@updates",
        "opennavo://updates:99",
        "opennavo://updates#x",
        "opennavo://package/ruby/node",
        "opennavo://package/cask/--all",
        "opennavo://package/cask/a%2fb",
        "opennavo://package/cask/node?action=remove",
        "opennavo://package/cask/node?action=install&action=install",
        "opennavo://collection/../x",
        "opennavo://updates?action=install",
        "opennavo://search?q=x&q=y",
        "opennavo://collection/new-mac/",
        "opennavo://updates\n",
    ] {
        assert!(deeplink::parse(invalid).is_none(), "{invalid}");
    }
    assert!(deeplink::parse(&format!("opennavo://package/cask/{}", "a".repeat(129))).is_none());
    assert!(deeplink::parse(&format!("opennavo://collection/{}", "a".repeat(65))).is_none());
}

#[test]
fn relaunch_carries_unique_valid_links_and_drops_invalid_or_missing_links() {
    let valid = "opennavo://package/cask/ghostty?action=install";
    let links =
        deeplink::validated_links(["OpenNavo", valid, "opennavo://updates?action=remove", valid]);
    assert_eq!(links, vec![valid]);
    assert!(deeplink::validated_links(["OpenNavo", "--hidden"]).is_empty());
}

#[test]
fn formula_install_links_are_read_only_and_install_intents_preserve_the_active_confirmation() {
    let formula = deeplink::parse("opennavo://package/formula/ripgrep?action=install").unwrap();
    assert!(formula.action.is_none());

    let events = Arc::new(Mutex::new(Vec::new()));
    let saved = events.clone();
    let router = deeplink::Router::new(
        Arc::new(move |event| saved.lock().unwrap().push(event)),
        Arc::new(|| {}),
    );
    router.ready();
    for index in 0..32 {
        router.receive(&format!(
            "opennavo://package/cask/app-{index}?action=install"
        ));
    }
    let events = events.lock().unwrap();
    let first = match &events[0] {
        CoreEvent::Deeplink(event) => event.pending_id.clone().unwrap(),
        _ => panic!("expected deep link"),
    };
    let second = match &events[1] {
        CoreEvent::Deeplink(event) => event.pending_id.clone().unwrap(),
        _ => panic!("expected deep link"),
    };
    drop(events);
    let _active = router
        .begin_install(&first)
        .expect("first request is active");
    router.receive("opennavo://package/cask/app-32?action=install");
    assert_eq!(
        router.begin_install(&second).unwrap_err().code,
        "E_INVALID_ARG"
    );
    router.release_install(&first);
    assert!(router.begin_install(&first).is_ok());
}

#[test]
fn cold_launch_forwarding_buffer_duplicate_and_invalid_only_show_window() {
    let events = Arc::new(Mutex::new(Vec::new()));
    let saved = events.clone();
    let opened = Arc::new(AtomicUsize::new(0));
    let count = opened.clone();
    let router = deeplink::Router::new(
        Arc::new(move |event| saved.lock().unwrap().push(event)),
        Arc::new(move || {
            count.fetch_add(1, Ordering::Relaxed);
        }),
    );
    let input = "opennavo://package/cask/ghostty?action=install";
    router.receive(input);
    router.receive_args(&["OpenNavo".into(), input.into()]);
    router.receive("opennavo://updates?action=remove");
    assert_eq!(opened.load(Ordering::Relaxed), 3);
    assert!(events.lock().unwrap().is_empty());
    router.ready();
    assert_eq!(events.lock().unwrap().len(), 1);
    assert!(
        matches!(&events.lock().unwrap()[0],CoreEvent::Deeplink(event) if event.action==Some(DeepLinkAction::Install))
    );
    router.receive("opennavo://updates");
    assert_eq!(events.lock().unwrap().len(), 2);
    router.loading();
    router.navigate("/settings");
    assert_eq!(events.lock().unwrap().len(), 2);
    router.ready();
    assert_eq!(events.lock().unwrap().len(), 3);
    // The router has no queue handle; install intent is only an event and cannot cause brew writes.
    assert!(
        events
            .lock()
            .unwrap()
            .iter()
            .all(|e| matches!(e, CoreEvent::Deeplink(_)))
    );
}
