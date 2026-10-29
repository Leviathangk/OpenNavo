use opennavo_desktop_lib::{deeplink, events::CoreEvent, model::*};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};
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
            true,
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
