use crate::{
    AppError,
    brew::args::{catalog_target, write_args_in_tap},
    db::Database,
    model::{Kind, Task, TaskKind, TaskOp, TaskOptions, TaskTarget},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

/// Rust-owned snapshot of an install. No IPC command accepts this type, and its fields cannot be
/// changed after resolution. Keep the UI identity as well as the actual Homebrew identity.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ResolvedInstallPlan {
    target: TaskTarget,
    command_target: TaskTarget,
    tap: Option<String>,
    args: Vec<String>,
    options: TaskOptions,
}

impl ResolvedInstallPlan {
    pub fn resolve(
        database: &Database,
        target: TaskTarget,
        options: TaskOptions,
    ) -> Result<Self, AppError> {
        target.kind.brew_kind()?;
        crate::catalog::links::reject_write(database, &target)?;
        let item = if target.kind == TaskKind::Cask {
            crate::catalog::get(database, Kind::Cask, &target.token)?
        } else {
            None
        };
        let command_target = catalog_target(&target, item.as_ref())?;
        let tap = item.and_then(|item| item.tap);
        let args = write_args_in_tap(
            TaskOp::Install,
            Some(&command_target),
            &options,
            tap.as_deref(),
        )?;
        Ok(Self {
            target,
            command_target,
            tap,
            args,
            options,
        })
    }

    pub fn target(&self) -> &TaskTarget {
        &self.target
    }

    pub fn command_target(&self) -> &TaskTarget {
        &self.command_target
    }

    pub fn tap(&self) -> Option<&str> {
        self.tap.as_deref()
    }

    pub fn options(&self) -> &TaskOptions {
        &self.options
    }

    pub fn args(&self) -> &[String] {
        &self.args
    }

    pub fn digest(&self) -> String {
        digest(&(TaskOp::Install, self))
    }

    pub(super) fn validate_task(&self, task: &Task) -> Result<(), AppError> {
        if task.op != TaskOp::Install
            || task.target.as_ref() != Some(&self.target)
            || task.options != self.options
            || self.target.kind != self.command_target.kind
            || write_args_in_tap(
                TaskOp::Install,
                Some(&self.command_target),
                &self.options,
                self.tap.as_deref(),
            )? != self.args
        {
            return Err(AppError::new(
                "E_CONFIRMATION_MISMATCH",
                "install_plan_mismatch",
            ));
        }
        Ok(())
    }
}

/// Order, resolved identities, taps, final arguments and all options are part of the approval.
pub fn ordered_install_plans_digest(plans: &[ResolvedInstallPlan]) -> String {
    digest(
        &plans
            .iter()
            .map(ResolvedInstallPlan::digest)
            .collect::<Vec<_>>(),
    )
}

pub(super) fn request_digest(
    op: TaskOp,
    target: Option<&TaskTarget>,
    options: &TaskOptions,
) -> String {
    digest(&(op, target, options))
}

fn digest(value: &impl Serialize) -> String {
    let canonical = serde_json::to_vec(value).expect("execution plans are serializable");
    format!("{:x}", Sha256::digest(canonical))
}
