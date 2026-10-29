use crate::{
    AppError,
    model::{CatalogItem, Kind, TaskOp, TaskOptions, TaskTarget},
};

pub fn validate_token(token: &str) -> Result<(), AppError> {
    let bytes = token.as_bytes();
    if bytes.is_empty()
        || bytes.len() > 128
        || !bytes[0].is_ascii_lowercase() && !bytes[0].is_ascii_digit()
        || !bytes
            .iter()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b"@._+-".contains(b))
    {
        return Err(AppError::new("E_INVALID_ARG", "token"));
    }
    Ok(())
}

/// Third-party tap names as brew spells them (`owner/name`); Homebrew's own taps never come from the catalog.
pub fn validate_tap(tap: &str) -> Result<(), AppError> {
    let invalid = || AppError::new("E_INVALID_ARG", "tap");
    let (owner, name) = tap.split_once('/').ok_or_else(invalid)?;
    let segment = |value: &str, max: usize, extra: &[u8]| {
        let bytes = value.as_bytes();
        !bytes.is_empty()
            && bytes.len() <= max
            && (bytes[0].is_ascii_lowercase() || bytes[0].is_ascii_digit())
            && bytes
                .iter()
                .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || extra.contains(b))
    };
    if !segment(owner, 39, b"-") || !segment(name, 100, b"-_.") || owner == "homebrew" {
        return Err(invalid());
    }
    Ok(())
}

/// The name brew installs: `<tap>/<token>` for third-party taps, otherwise the token itself.
pub fn qualified_token(tap: Option<&str>, token: &str) -> Result<String, AppError> {
    validate_token(token)?;
    match tap {
        Some(tap) => {
            validate_tap(tap)?;
            Ok(format!("{tap}/{token}"))
        }
        None => Ok(token.to_owned()),
    }
}

/// Split a local Homebrew installation identity, including Homebrew's own tap.
/// Catalog tokens and catalog-supplied taps keep their stricter validators.
pub fn split_cask_token(identity: &str) -> Result<(Option<&str>, &str), AppError> {
    match identity.rsplit_once('/') {
        Some((tap, token)) => {
            validate_token(token)?;
            if tap != "homebrew/cask" {
                validate_tap(tap)?;
            }
            Ok((Some(tap), token))
        }
        None => {
            validate_token(identity)?;
            Ok((None, identity))
        }
    }
}

/// Resolve a catalog site alias for brew while keeping the task/navigation identity intact.
pub fn catalog_target(
    target: &TaskTarget,
    item: Option<&CatalogItem>,
) -> Result<TaskTarget, AppError> {
    let mut resolved = target.clone();
    if let Some(item) = item
        && let Some(token) = &item.brew_token
    {
        if target.kind != Kind::Cask || item.kind != Kind::Cask || item.token != target.token {
            return Err(AppError::new("E_INVALID_ARG", "catalog_brew_token"));
        }
        let tap = item
            .tap
            .as_deref()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "catalog_brew_token"))?;
        qualified_token(Some(tap), token)?;
        resolved.token = token.clone();
    }
    Ok(resolved)
}

pub fn write_args(
    op: TaskOp,
    target: Option<&TaskTarget>,
    options: &TaskOptions,
) -> Result<Vec<String>, AppError> {
    write_args_in_tap(op, target, options, None)
}

/// write_args for a cask the catalog lists from a third-party tap. Install, reinstall and upgrade name it
/// `<tap>/<token>`: brew then taps the repository if needed and trusts that one cask (Homebrew 7 refuses
/// to load casks from untrusted taps). Uninstall and pinning act on the installed cask by token.
pub fn write_args_in_tap(
    op: TaskOp,
    target: Option<&TaskTarget>,
    options: &TaskOptions,
    tap: Option<&str>,
) -> Result<Vec<String>, AppError> {
    if target.is_some_and(|target| target.kind == Kind::Link) {
        return Err(AppError::new("E_NOT_INSTALLABLE", "link_target"));
    }
    if options.plan_id.is_some() || options.item_ids.is_some() || options.delete_mode.is_some() {
        return Err(AppError::new(
            "E_INVALID_ARG",
            "plan_options_require_engine",
        ));
    }
    if matches!(op, TaskOp::Update | TaskOp::Cleanup) {
        if target.is_some() || *options != TaskOptions::default() {
            return Err(AppError::new("E_INVALID_ARG", "global_options"));
        }
        return Ok(if op == TaskOp::Update {
            vec!["update".into()]
        } else {
            vec!["cleanup".into(), "--prune=all".into()]
        });
    }
    if op == TaskOp::InstallHomebrew {
        if target.is_some() || *options != TaskOptions::default() {
            return Err(AppError::new("E_INVALID_ARG", "installer_options"));
        }
        // The installer downloads/executes a file in a separate branch; never pass these empty arguments to brew itself.
        return Ok(vec![]);
    }
    let target = target.ok_or_else(|| AppError::new("E_INVALID_ARG", "target"))?;
    target.kind.brew_kind()?;
    validate_token(&target.token)?;
    if (options.zap || options.force) && (op != TaskOp::Uninstall || target.kind != Kind::Cask)
        || options.adopt && (op != TaskOp::Install || target.kind != Kind::Cask)
        || options.greedy && (op != TaskOp::Upgrade || target.kind != Kind::Cask)
    {
        return Err(AppError::new("E_INVALID_ARG", "operation_options"));
    }
    if (options.quit_running || options.reopen)
        && (op != TaskOp::Upgrade || target.kind != Kind::Cask || !options.quit_running)
    {
        return Err(AppError::new("E_INVALID_ARG", "running_app_options"));
    }
    let name = match op {
        TaskOp::Install => "install",
        TaskOp::Upgrade => "upgrade",
        TaskOp::Uninstall => "uninstall",
        TaskOp::Reinstall => "reinstall",
        TaskOp::Pin => "pin",
        TaskOp::Unpin => "unpin",
        _ => return Err(AppError::new("E_INVALID_ARG", "operation")),
    };
    let mut args = vec![name.into(), target.kind.flag().into()];
    if op == TaskOp::Upgrade && target.kind == Kind::Cask {
        args.push("--greedy".into());
    }
    if options.zap {
        args.push("--zap".into());
    }
    if options.force {
        args.push("--force".into());
    }
    if options.adopt {
        args.push("--adopt".into());
    }
    let tap = tap.filter(|_| {
        matches!(op, TaskOp::Install | TaskOp::Reinstall | TaskOp::Upgrade)
            && target.kind == Kind::Cask
    });
    args.push(qualified_token(tap, &target.token)?);
    Ok(args)
}

#[derive(Debug, Clone, Copy)]
pub enum ReadOp {
    Version,
    Prefix,
    Cache,
    Installed,
    Outdated { greedy: bool },
    Doctor,
    CleanupEstimate,
    Info(Kind),
}
pub fn read_args(op: ReadOp, token: Option<&str>) -> Result<Vec<String>, AppError> {
    if let ReadOp::Info(kind) = op {
        let token = token.ok_or_else(|| AppError::new("E_INVALID_ARG", "token"))?;
        if kind == Kind::Cask {
            split_cask_token(token)?;
        } else {
            validate_token(token)?;
        }
        return Ok(vec![
            "info".into(),
            "--json=v2".into(),
            kind.flag().into(),
            token.into(),
        ]);
    }
    if token.is_some() {
        return Err(AppError::new("E_INVALID_ARG", "unexpected_token"));
    }
    Ok(match op {
        ReadOp::Version => vec!["--version".into()],
        ReadOp::Prefix => vec!["--prefix".into()],
        ReadOp::Cache => vec!["--cache".into()],
        ReadOp::Installed => vec!["info".into(), "--json=v2".into(), "--installed".into()],
        ReadOp::Outdated { greedy } => {
            let mut args = vec!["outdated".into(), "--json=v2".into()];
            if greedy {
                args.push("--greedy".into());
            }
            args
        }
        ReadOp::Doctor => vec!["doctor".into()],
        ReadOp::CleanupEstimate => vec!["cleanup".into(), "--prune=all".into(), "--dry-run".into()],
        ReadOp::Info(_) => return Err(AppError::new("E_INVALID_ARG", "info_requires_target")),
    })
}

pub fn export_args(path: &str) -> Result<Vec<String>, AppError> {
    if !std::path::Path::new(path).is_absolute() || path.contains(['\0', '\n', '\r']) {
        return Err(AppError::new("E_INVALID_ARG", "export_path"));
    }
    Ok(vec![
        "bundle".into(),
        "dump".into(),
        format!("--file={path}"),
        "--force".into(),
    ])
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn catalog_alias_preserves_site_target_and_validates_upstream() {
        let mut item = crate::catalog::links::tests::item();
        item.kind = Kind::Cask;
        item.token = "stablyai-orca".into();
        item.tap = Some("stablyai/orca".into());
        item.brew_token = Some("orca".into());
        let site = TaskTarget {
            kind: Kind::Cask.into(),
            token: item.token.clone(),
        };
        let resolved = catalog_target(&site, Some(&item)).unwrap();
        assert_eq!(site.token, "stablyai-orca");
        assert_eq!(resolved.token, "orca");
        for token in ["../orca", "--help", "a/b", "UPPER"] {
            item.brew_token = Some(token.into());
            assert!(catalog_target(&site, Some(&item)).is_err());
        }
        item.brew_token = Some("orca".into());
        item.tap = None;
        assert!(catalog_target(&site, Some(&item)).is_err());
        item.tap = Some("homebrew/cask".into());
        assert!(catalog_target(&site, Some(&item)).is_err());
    }

    #[test]
    fn reject_shell_paths_flags_and_unicode() {
        for bad in [
            "", "--help", "../node", "a/b", "node;id", "a\nb", "$(id)", "NODE", "微信",
        ] {
            assert!(validate_token(bad).is_err(), "{bad}");
        }
        assert!(validate_token(&"a".repeat(129)).is_err());
        for good in [
            "node@22",
            "openssl@3",
            "ripgrep",
            "python@3.14",
            "a+b",
            "font_1",
        ] {
            assert!(validate_token(good).is_ok());
        }
    }
    #[test]
    fn flags_and_options_are_operation_specific() {
        let target = TaskTarget {
            kind: Kind::Cask.into(),
            token: "firefox".into(),
        };
        assert_eq!(
            write_args(TaskOp::Upgrade, Some(&target), &TaskOptions::default()).expect("args"),
            ["upgrade", "--cask", "--greedy", "firefox"]
        );
        let cli = TaskTarget {
            kind: Kind::Formula.into(),
            token: "ripgrep".into(),
        };
        assert_eq!(
            write_args(TaskOp::Install, Some(&cli), &TaskOptions::default()).expect("args"),
            ["install", "--formula", "ripgrep"]
        );
        assert!(
            write_args(
                TaskOp::Install,
                Some(&cli),
                &TaskOptions {
                    adopt: true,
                    ..Default::default()
                }
            )
            .is_err()
        );
        assert!(write_args(TaskOp::Cleanup, Some(&cli), &Default::default()).is_err());
        // --force is only for finishing Cask uninstalls; reject other operations and all Formula uses.
        let finish = TaskOptions {
            zap: true,
            force: true,
            ..Default::default()
        };
        assert_eq!(
            write_args(TaskOp::Uninstall, Some(&target), &finish).expect("args"),
            ["uninstall", "--cask", "--zap", "--force", "firefox"]
        );
        let force = TaskOptions {
            force: true,
            ..Default::default()
        };
        assert!(write_args(TaskOp::Upgrade, Some(&target), &force).is_err());
        assert!(write_args(TaskOp::Uninstall, Some(&cli), &force).is_err());
        assert_eq!(
            read_args(ReadOp::Installed, None).expect("args"),
            ["info", "--json=v2", "--installed"]
        );
    }
    #[test]
    fn third_party_taps_qualify_install_upgrade_and_reinstall_only() {
        for good in ["swiftvm-app/tap", "user/homebrew-tap.v2", "a1/b_c"] {
            assert!(validate_tap(good).is_ok(), "{good}");
        }
        for bad in [
            "",
            "tap",
            "homebrew/cask",
            "a/b/c",
            "A/b",
            "-a/b",
            "a/../b",
            "a/.b",
            "a /b",
            "a/b\n",
            "a/b;id",
        ] {
            assert!(validate_tap(bad).is_err(), "{bad}");
        }
        let target = TaskTarget {
            kind: Kind::Cask.into(),
            token: "swiftvm".into(),
        };
        let tap = Some("swiftvm-app/tap");
        let none = TaskOptions::default();
        assert_eq!(
            write_args_in_tap(TaskOp::Install, Some(&target), &none, tap).expect("args"),
            ["install", "--cask", "swiftvm-app/tap/swiftvm"]
        );
        assert_eq!(
            write_args_in_tap(TaskOp::Upgrade, Some(&target), &none, tap).expect("args"),
            ["upgrade", "--cask", "--greedy", "swiftvm-app/tap/swiftvm"]
        );
        assert_eq!(
            write_args_in_tap(TaskOp::Reinstall, Some(&target), &none, tap).expect("args"),
            ["reinstall", "--cask", "swiftvm-app/tap/swiftvm"]
        );
        // Generic arguments qualify installs and upgrades; planned removals use their verified installed identity.
        assert_eq!(
            write_args_in_tap(TaskOp::Uninstall, Some(&target), &none, tap).expect("args"),
            ["uninstall", "--cask", "swiftvm"]
        );
        let formula = TaskTarget {
            kind: Kind::Formula.into(),
            token: "swiftvm".into(),
        };
        assert_eq!(
            write_args_in_tap(TaskOp::Install, Some(&formula), &none, tap).expect("args"),
            ["install", "--formula", "swiftvm"]
        );
        assert!(
            write_args_in_tap(TaskOp::Install, Some(&target), &none, Some("homebrew/cask"))
                .is_err()
        );
        assert_eq!(
            read_args(ReadOp::Info(Kind::Cask), Some("swiftvm-app/tap/swiftvm")).expect("args"),
            ["info", "--json=v2", "--cask", "swiftvm-app/tap/swiftvm"]
        );
        assert_eq!(
            read_args(ReadOp::Info(Kind::Cask), Some("homebrew/cask/firefox")).expect("args"),
            ["info", "--json=v2", "--cask", "homebrew/cask/firefox"]
        );
        for bad in ["a/b", "x/y/--help", "homebrew/core/firefox", "a/b/c/d"] {
            assert!(
                read_args(ReadOp::Info(Kind::Cask), Some(bad)).is_err(),
                "{bad}"
            );
        }
        assert!(read_args(ReadOp::Info(Kind::Formula), Some("swiftvm-app/tap/swiftvm")).is_err());
    }
    #[test]
    fn every_whitelisted_operation_has_fixed_arguments() {
        for kind in [Kind::Cask, Kind::Formula] {
            let target = TaskTarget {
                kind: kind.into(),
                token: "valid@1".into(),
            };
            for op in [
                TaskOp::Install,
                TaskOp::Upgrade,
                TaskOp::Uninstall,
                TaskOp::Reinstall,
                TaskOp::Pin,
                TaskOp::Unpin,
            ] {
                let args =
                    write_args(op, Some(&target), &TaskOptions::default()).expect("allowlist");
                assert!(args.contains(&kind.flag().to_owned()));
                assert_eq!(args.last().map(String::as_str), Some("valid@1"));
            }
            assert_eq!(
                read_args(ReadOp::Info(kind), Some("valid@1")).expect("single package")[2],
                kind.flag()
            );
        }
        for (op, args) in [
            (ReadOp::Version, vec!["--version"]),
            (ReadOp::Prefix, vec!["--prefix"]),
            (ReadOp::Cache, vec!["--cache"]),
            (ReadOp::Doctor, vec!["doctor"]),
            (
                ReadOp::Outdated { greedy: true },
                vec!["outdated", "--json=v2", "--greedy"],
            ),
            (
                ReadOp::CleanupEstimate,
                vec!["cleanup", "--prune=all", "--dry-run"],
            ),
        ] {
            assert_eq!(read_args(op, None).expect("global"), args);
            assert!(read_args(op, Some("node")).is_err());
        }
        assert_eq!(
            export_args("/tmp/with spaces/Brewfile").expect("export"),
            [
                "bundle",
                "dump",
                "--file=/tmp/with spaces/Brewfile",
                "--force"
            ]
        );
        assert!(export_args("--eval=code").is_err());
        assert!(export_args("/tmp/a\nb").is_err());
    }
}
