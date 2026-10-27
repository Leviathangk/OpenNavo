//! Read-only posture facts. Failed or unavailable probes remain unknown.
use super::{Commands, PostureCheck, PostureFact, PostureId, PostureState, files};
use std::{collections::BTreeMap, path::Path};
const PRIVACY: &str = "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension";
const USERS: &str = "x-apple.systempreferences:com.apple.Users-Groups-Settings.extension";
fn text(c: &dyn Commands, p: &str, a: &[&str]) -> Option<String> {
    c.run(p, a).ok().map(|s| s.trim().to_owned())
}
fn plist(path: &Path) -> Option<plist::Value> {
    plist::from_bytes(&files::read(path, 4 * 1024 * 1024).ok()?).ok()
}
fn value(path: &Path, key: &str) -> Option<plist::Value> {
    plist(path)?.as_dictionary()?.get(key).cloned()
}
fn boolean(v: Option<plist::Value>) -> Option<bool> {
    v.and_then(|v| {
        v.as_boolean()
            .or_else(|| v.as_signed_integer().map(|v| v != 0))
    })
}
fn check_bool(
    id: PostureId,
    enabled: Option<bool>,
    good: bool,
    panel: Option<&str>,
    bad: PostureState,
) -> PostureCheck {
    let mut facts = BTreeMap::new();
    if let Some(v) = enabled {
        facts.insert("enabled".into(), PostureFact::Boolean(v));
    }
    PostureCheck {
        id,
        state: enabled
            .map(|v| if v == good { PostureState::Ok } else { bad })
            .unwrap_or(PostureState::Unknown),
        facts,
        settings_url: panel.map(str::to_owned),
    }
}
fn enabled(output: Option<String>) -> Option<bool> {
    output.and_then(|s| {
        let s = s.to_lowercase();
        if s.contains("disabled") {
            Some(false)
        } else if s.contains("enabled") {
            Some(true)
        } else {
            None
        }
    })
}
pub fn check(c: &dyn Commands, home: &Path) -> Vec<PostureCheck> {
    let mut checks = vec![
        check_bool(
            PostureId::Sip,
            enabled(text(c, "/usr/bin/csrutil", &["status"])),
            true,
            None,
            PostureState::Bad,
        ),
        check_bool(
            PostureId::Ssv,
            enabled(text(
                c,
                "/usr/bin/csrutil",
                &["authenticated-root", "status"],
            )),
            true,
            None,
            PostureState::Bad,
        ),
        check_bool(
            PostureId::Gatekeeper,
            gatekeeper(c),
            true,
            Some(PRIVACY),
            PostureState::Bad,
        ),
        check_bool(
            PostureId::Filevault,
            text(c, "/usr/bin/fdesetup", &["status"]).and_then(|v| {
                if v.contains("FileVault is On") {
                    Some(true)
                } else if v.contains("FileVault is Off") {
                    Some(false)
                } else {
                    None
                }
            }),
            true,
            Some(PRIVACY),
            PostureState::Warn,
        ),
        check_bool(
            PostureId::Firewall,
            enabled(text(
                c,
                "/usr/libexec/ApplicationFirewall/socketfilterfw",
                &["--getglobalstate"],
            )),
            true,
            Some("x-apple.systempreferences:com.apple.Network-Settings.extension?Firewall"),
            PostureState::Warn,
        ),
    ];
    let mut x = PostureCheck {
        id: PostureId::Xprotect,
        state: PostureState::Unknown,
        facts: BTreeMap::new(),
        settings_url: None,
    };
    for path in [
        "/Library/Apple/System/Library/CoreServices/XProtect.bundle/Contents/Info.plist",
        "/System/Library/CoreServices/XProtect.bundle/Contents/Info.plist",
    ] {
        if let Some(version) = value(Path::new(path), "CFBundleShortVersionString")
            .and_then(|v| v.as_string().map(str::to_owned))
        {
            x.state = PostureState::Ok;
            x.facts.insert("version".into(), PostureFact::Text(version));
            if let Ok(m) = files::safe_metadata(Path::new(path))
                && let Ok(t) = m.modified()
                && let Ok(d) = t.duration_since(std::time::UNIX_EPOCH)
            {
                x.facts.insert(
                    "updatedAt".into(),
                    PostureFact::Number(d.as_millis() as i64),
                );
            }
            break;
        }
    }
    checks.push(x);
    let updates = Path::new("/Library/Preferences/com.apple.SoftwareUpdate.plist");
    let config = boolean(value(updates, "ConfigDataInstall"));
    let critical = boolean(value(updates, "CriticalUpdateInstall"));
    checks.push(check_bool(
        PostureId::Updates,
        config.zip(critical).map(|(a, b)| a && b),
        true,
        Some("x-apple.systempreferences:com.apple.Software-Update-Settings.extension"),
        PostureState::Warn,
    ));
    // Modern macOS no longer persists these values in the legacy ByHost domain.
    // This status query reads effective policy and never asks for a password.
    let screen = c
        .output("/usr/sbin/sysadminctl", &["-screenLock", "status"])
        .ok()
        .and_then(|o| o.success().ok())
        .map(|o| o.text())
        .and_then(|v| screen_lock(&v))
        .or_else(|| {
            for host in [true, false] {
                let read = |key| {
                    let mut args = if host { vec!["-currentHost"] } else { vec![] };
                    args.extend(["read", "com.apple.screensaver", key]);
                    text(c, "/usr/bin/defaults", &args).and_then(|v| v.parse::<f64>().ok())
                };
                if let Some(asks) = read("askForPassword") {
                    if asks == 0.0 {
                        return Some(false);
                    }
                    if asks == 1.0
                        && let Some(delay) = read("askForPasswordDelay")
                    {
                        return Some((0.0..=5.0).contains(&delay));
                    }
                }
            }
            None
        });
    checks.push(check_bool(
        PostureId::Screenlock,
        screen,
        true,
        Some("x-apple.systempreferences:com.apple.Lock-Screen-Settings.extension"),
        PostureState::Warn,
    ));
    let login = Path::new("/Library/Preferences/com.apple.loginwindow.plist");
    let auto = plist(login).and_then(|p| {
        p.as_dictionary().map(|d| {
            d.get("autoLoginUser")
                .and_then(|v| v.as_string())
                .is_some_and(|v| !v.is_empty())
        })
    });
    checks.push(check_bool(
        PostureId::Autologin,
        auto,
        false,
        Some(USERS),
        PostureState::Bad,
    ));
    checks.push(check_bool(
        PostureId::Guest,
        boolean(value(login, "GuestEnabled")),
        false,
        Some(USERS),
        PostureState::Warn,
    ));
    checks.push(sharing(c, Path::new("/")));
    let _ = home;

    checks
}
fn gatekeeper(c: &dyn Commands) -> Option<bool> {
    let output = c.output("/usr/sbin/spctl", &["--status"]).ok()?;
    // spctl uses exit 1 for disabled. Only exact status lines are authoritative.
    output.text().lines().find_map(|line| match line.trim() {
        "assessments enabled" => Some(true),
        "assessments disabled" => Some(false),
        _ => None,
    })
}
fn screen_lock(text: &str) -> Option<bool> {
    let status = text.lines().find_map(|line| {
        line.split_once("screenLock delay is ")
            .map(|(_, s)| s.trim())
    })?;
    if status == "immediate" {
        return Some(true);
    }
    if matches!(status, "off" | "disabled") {
        return Some(false);
    }
    let seconds = status
        .strip_suffix(" seconds")
        .or_else(|| status.strip_suffix(" second"))?
        .parse::<f64>()
        .ok()?;
    Some((0.0..=5.0).contains(&seconds))
}
fn sharing(c: &dyn Commands, root: &Path) -> PostureCheck {
    let mut result = PostureCheck {
        id: PostureId::Sharing,
        state: PostureState::Unknown,
        facts: BTreeMap::new(),
        settings_url: Some("x-apple.systempreferences:com.apple.Sharing-Settings.extension".into()),
    };
    let domain = crate::uninstall::identity::user_domain();
    let system_overrides = text(c, "/bin/launchctl", &["print-disabled", "system"]);
    let user_overrides = text(c, "/bin/launchctl", &["print-disabled", &domain]);
    let mut active = 0;
    let mut known = 0;
    for (label, plist_name, domain, overrides) in [
        (
            "com.openssh.sshd",
            "LaunchDaemons/ssh.plist",
            "system",
            system_overrides.as_deref(),
        ),
        (
            "com.apple.smbd",
            "LaunchDaemons/com.apple.smbd.plist",
            "system",
            system_overrides.as_deref(),
        ),
        (
            "com.apple.screensharing",
            "LaunchDaemons/com.apple.screensharing.plist",
            "system",
            system_overrides.as_deref(),
        ),
        (
            "com.apple.RemoteDesktop.agent",
            "LaunchAgents/com.apple.RemoteDesktop.plist",
            domain.as_str(),
            user_overrides.as_deref(),
        ),
    ] {
        let output = c.output("/bin/launchctl", &["print", &format!("{domain}/{label}")]);
        let on = match output {
            Ok(o) if o.code == Some(0) && label == "com.apple.RemoteDesktop.agent" => {
                // ARDAgent is registered by default, even when remote management is off.
                // Apple's launch plist gates it on this marker; registration alone
                // is not evidence that sharing is enabled.
                if o.text().lines().any(|l| l.trim() == "state = running") {
                    Some(true)
                } else {
                    remote_management_marker(root)
                }
            }
            Ok(o) if o.code == Some(0) => Some(true),
            Ok(o)
                if o.code == Some(113)
                    && o.text()
                        .contains(&format!("Could not find service \"{label}\"")) =>
            {
                // An absent service is not loaded, even when its plist is enabled by default.
                Some(false)
            }
            _ => None,
        }
        .or_else(|| {
            // If a status query is unavailable, an explicit disable or a disabled
            // system default is evidence of off. Missing override alone proves nothing.
            let overrides = overrides?;
            match super::startup::override_disabled(Some(overrides), label) {
                Some(disabled) => Some(!disabled),
                None => boolean(value(
                    &root.join("System/Library").join(plist_name),
                    "Disabled",
                ))
                .filter(|v| *v)
                .map(|_| false),
            }
        });
        if let Some(on) = on {
            known += 1;
            active += u32::from(on);
            result.facts.insert(label.into(), PostureFact::Boolean(on));
        }
    }
    if active > 0 {
        result.state = PostureState::Warn;
    } else if known == 4 {
        result.state = PostureState::Info;
    }
    result
}
fn remote_management_marker(root: &Path) -> Option<bool> {
    match std::fs::metadata(
        root.join("Library/Application Support/Apple/Remote Desktop/RemoteManagement.launchd"),
    ) {
        Ok(_) => Some(true),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Some(false),
        Err(_) => None,
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    struct Missing;
    impl Commands for Missing {
        fn run(&self, _: &str, _: &[&str]) -> Result<String, crate::AppError> {
            Err(crate::AppError::new("E_UNKNOWN", "unavailable"))
        }
    }
    struct StatusFixture;
    impl Commands for StatusFixture {
        fn run(&self, program: &str, args: &[&str]) -> Result<String, crate::AppError> {
            if args.first() == Some(&"print-disabled") {
                Ok("disabled services = {}".into())
            } else {
                Missing.run(program, args)
            }
        }
        fn output(
            &self,
            program: &str,
            args: &[&str],
        ) -> Result<super::super::command::Output, crate::AppError> {
            let (code, stdout, stderr) = if program.ends_with("spctl") {
                (1, "assessments disabled\n".to_owned(), String::new())
            } else if program.ends_with("sysadminctl") && args == ["-screenLock", "status"] {
                // sysadminctl writes its successful status to stderr on macOS.
                (
                    0,
                    String::new(),
                    "sysadminctl[123] screenLock delay is immediate".into(),
                )
            } else if args.first() == Some(&"print") {
                let label = args[1].rsplit('/').next().unwrap();
                (
                    113,
                    String::new(),
                    format!("Could not find service \"{label}\" in domain"),
                )
            } else {
                return Err(crate::AppError::new("E_UNKNOWN", "unavailable"));
            };
            Ok(super::super::command::Output {
                code: Some(code),
                stdout,
                stderr,
            })
        }
    }
    #[test]
    fn disabled_gatekeeper_and_modern_screenlock_and_absent_sharing_are_readable() {
        let rows = check(&StatusFixture, Path::new("/nonexistent"));
        for (id, state) in [
            (PostureId::Gatekeeper, PostureState::Bad),
            (PostureId::Screenlock, PostureState::Ok),
            (PostureId::Sharing, PostureState::Info),
        ] {
            assert_eq!(rows.iter().find(|r| r.id == id).unwrap().state, state);
        }
        assert_eq!(screen_lock("screenLock delay is 300 seconds"), Some(false));
        assert_eq!(screen_lock("screenLock delay is off"), Some(false));
        assert_eq!(screen_lock("permission denied"), None);
        assert_eq!(
            sharing(&Missing, Path::new("/nonexistent")).state,
            PostureState::Unknown
        );
    }
    #[test]
    fn disabled_is_not_misread_as_enabled() {
        assert_eq!(
            enabled(Some("System Integrity Protection status: disabled.".into())),
            Some(false)
        );
        assert_eq!(enabled(Some("unknown".into())), None);
    }
    #[test]
    fn failed_sip_probe_is_unknown() {
        let rows = check(&Missing, Path::new("/nonexistent"));
        assert_eq!(rows.len(), 11);
        assert_eq!(rows[0].state, PostureState::Unknown);
        assert!(rows[0].facts.is_empty());
    }
    #[test]
    fn registered_remote_desktop_agent_is_not_enabled_without_its_activation_marker() {
        struct IdleAgent;
        impl Commands for IdleAgent {
            fn run(&self, p: &str, a: &[&str]) -> Result<String, crate::AppError> {
                StatusFixture.run(p, a)
            }
            fn output(
                &self,
                p: &str,
                a: &[&str],
            ) -> Result<super::super::command::Output, crate::AppError> {
                if a.first() == Some(&"print") && a[1].ends_with("/com.apple.RemoteDesktop.agent") {
                    Ok(super::super::command::Output {
                        code: Some(0),
                        stdout: "state = not running".into(),
                        stderr: String::new(),
                    })
                } else {
                    StatusFixture.output(p, a)
                }
            }
        }
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        assert_eq!(sharing(&IdleAgent, &root).state, PostureState::Info);
        let marker =
            root.join("Library/Application Support/Apple/Remote Desktop/RemoteManagement.launchd");
        std::fs::create_dir_all(marker.parent().unwrap()).unwrap();
        std::fs::write(marker, "").unwrap();
        assert_eq!(sharing(&IdleAgent, &root).state, PostureState::Warn);
    }
}
