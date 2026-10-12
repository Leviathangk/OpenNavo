use crate::{
    AppError,
    model::{Kind, TaskOp, TaskOptions, TaskTarget},
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

pub fn write_args(
    op: TaskOp,
    target: Option<&TaskTarget>,
    options: &TaskOptions,
) -> Result<Vec<String>, AppError> {
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
    args.push(target.token.clone());
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
        validate_token(token)?;
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
