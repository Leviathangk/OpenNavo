//! All webviews stay on app-owned pages. External links use the opener plugin.
use tauri::{Manager, Runtime, Url, plugin::TauriPlugin};

fn allowed(url: &Url, dev_server: Option<&Url>) -> bool {
    if !url.username().is_empty() || url.password().is_some() {
        return false;
    }
    if url.port().is_none()
        && ((url.scheme() == "tauri" && url.host_str() == Some("localhost"))
            || (matches!(url.scheme(), "http" | "https")
                && url.host_str() == Some("tauri.localhost")))
    {
        return true;
    }
    #[cfg(debug_assertions)]
    if let Some(dev) = dev_server {
        return matches!(dev.scheme(), "http" | "https")
            && matches!(dev.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"))
            && url.origin() == dev.origin();
    }
    #[cfg(not(debug_assertions))]
    let _ = dev_server;
    false
}

pub(crate) fn guard<R: Runtime>() -> TauriPlugin<R> {
    tauri::plugin::Builder::new("local-navigation")
        .on_navigation(|webview, url| {
            allowed(url, webview.app_handle().config().build.dev_url.as_ref())
        })
        .build()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn navigation_allows_only_exact_packaged_origins() {
        for value in [
            "tauri://localhost/index.html#/discover",
            "http://tauri.localhost/",
            "https://tauri.localhost/index.html",
        ] {
            assert!(allowed(&value.parse().unwrap(), None), "{value}");
        }
        for value in [
            "https://example.test/",
            "https://api.opennavo.com/",
            "https://localhost/",
            "https://tauri.localhost.evil.test/",
            "tauri://evil.test/",
            "tauri://localhost:1234/",
            "http://tauri.localhost:1234/",
            "https://user@tauri.localhost/",
            "https://tauri.localhost@evil.test/",
            "file:///tmp/index.html",
            "data:text/html,hello",
            "javascript:alert(1)",
            "about:blank",
        ] {
            assert!(!allowed(&value.parse().unwrap(), None), "{value}");
        }
    }

    #[test]
    fn dev_navigation_is_debug_only_and_matches_the_configured_origin() {
        let dev = Url::parse("http://localhost:18741").unwrap();
        assert_eq!(
            allowed(&dev.join("index.html#/discover").unwrap(), Some(&dev)),
            cfg!(debug_assertions)
        );
        for value in [
            "http://localhost:18742/",
            "https://localhost:18741/",
            "http://127.0.0.1:18741/",
            "http://localhost.evil.test:18741/",
            "http://user@localhost:18741/",
        ] {
            assert!(!allowed(&value.parse().unwrap(), Some(&dev)), "{value}");
        }
        let remote = Url::parse("https://example.test").unwrap();
        assert!(!allowed(&remote, Some(&remote)));
    }

    #[test]
    fn navigation_guard_is_registered_globally_before_windows_are_built() {
        let source = include_str!("lib.rs");
        let guard = source
            .find(".plugin(navigation::guard())")
            .expect("global navigation plugin");
        assert!(guard < source.find(".setup(move |app|").unwrap());
    }
}
