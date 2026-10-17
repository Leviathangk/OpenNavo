//! User-session sidecar protocol. No subprocess here ever runs as root.
use crate::{AppError, model::TaskOp, uninstall::rules::Snapshot};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
use tokio::{
    io::{AsyncBufReadExt, AsyncReadExt, BufReader},
    process::Command,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrivilegedInstall {
    pub adam_id: u64,
    pub bundle_id: Option<String>,
    pub staging: PathBuf,
    pub staging_snapshot: Snapshot,
    pub pkg: PathBuf,
    pub pkg_snapshot: Snapshot,
    pub receipt: PathBuf,
    pub receipt_snapshot: Snapshot,
    pub app_path: PathBuf,
    pub app_snapshot: Option<Snapshot>,
    pub parent_snapshot: Snapshot,
}
#[derive(Debug, Deserialize)]
#[serde(tag = "event", rename_all = "camelCase")]
pub enum Event {
    Phase {
        #[serde(rename = "adamId")]
        adam_id: u64,
        phase: String,
        #[serde(default, deserialize_with = "deserialize_percent")]
        percent: Option<f32>,
    },
    NeedsPrivilegedInstall {
        #[serde(rename = "adamId")]
        adam_id: u64,
        pkg: PathBuf,
        receipt: PathBuf,
        #[serde(rename = "appPath")]
        app_path: PathBuf,
    },
    Error {
        #[serde(rename = "adamId")]
        adam_id: Option<u64>,
        code: String,
        domain: Option<String>,
        #[serde(rename = "errorCode")]
        error_code: Option<i64>,
        message: Option<String>,
    },
}
fn deserialize_percent<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<Option<f32>, D::Error> {
    // Tagged enums buffer arbitrary-precision decimals/exponents as Number maps. Decode the
    // number explicitly, then validate before narrowing so rounding cannot admit values above 100.
    Option::<serde_json::Number>::deserialize(deserializer)?
        .map(|number| {
            number
                .as_f64()
                .filter(|value| value.is_finite() && (0.0..=100.0).contains(value))
                .map(|value| value as f32)
                .ok_or_else(|| {
                    serde::de::Error::custom(
                        "store percent must be a finite number between 0 and 100",
                    )
                })
        })
        .transpose()
}
#[derive(Debug)]
pub struct DownloadResult {
    pub adam_id: u64,
    pub privileged: Option<PrivilegedInstall>,
    pub error: Option<AppError>,
}
pub type ProgressSink = Arc<dyn Fn(u64, &str, Option<f32>) -> Result<(), AppError> + Send + Sync>;
#[derive(Clone)]
pub struct Runner {
    pub executable: PathBuf,
    pub environment: BTreeMap<String, String>,
}
impl Runner {
    pub fn native() -> Result<Self, AppError> {
        let executable = std::env::current_exe()?
            .parent()
            .and_then(|p| p.parent())
            .map(|p| p.join("Helpers/opennavo-store"))
            .filter(|p| p.is_file())
            .ok_or_else(|| engine("store_helper_missing"))?;
        Ok(Self {
            executable,
            environment: BTreeMap::new(),
        })
    }
    pub async fn download(
        &self,
        op: TaskOp,
        ids: &[u64],
        staging: &Path,
        cancel: Arc<AtomicBool>,
        progress: ProgressSink,
    ) -> Result<Vec<DownloadResult>, AppError> {
        if !matches!(op, TaskOp::Install | TaskOp::Upgrade)
            || ids.is_empty()
            || ids.len() > 100
            || ids.iter().any(|id| !super::valid_adam_id(*id))
            || ids.iter().collect::<BTreeSet<_>>().len() != ids.len()
        {
            return Err(AppError::new("E_INVALID_ARG", "mas_install_targets"));
        }
        validate_staging(staging, unsafe { nix::libc::getuid() })?;
        let expected: BTreeSet<_> = ids.iter().copied().collect();
        let mut terminal = BTreeMap::new();
        let mut child = Command::new(&self.executable)
            .arg(if op == TaskOp::Upgrade {
                "update"
            } else {
                "install"
            })
            .arg("--adam-ids")
            .arg(ids.iter().map(u64::to_string).collect::<Vec<_>>().join(","))
            .arg("--staging")
            .arg(staging)
            .envs(&self.environment)
            .stdin(std::process::Stdio::piped())
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .kill_on_drop(true)
            .spawn()
            .map_err(|_| engine("store_helper_start"))?;
        let stdin = child.stdin.take();
        let stdout = child.stdout.take().ok_or_else(|| engine("store_stdout"))?;
        let mut stderr = child.stderr.take().ok_or_else(|| engine("store_stderr"))?;
        // Drain continuously without retaining account diagnostics or allowing a
        // full stderr pipe to stall the purchase process.
        let stderr_reader = tokio::spawn(async move {
            let mut bytes = [0u8; 8192];
            while let Ok(count) = stderr.read(&mut bytes).await {
                if count == 0 {
                    break;
                }
            }
        });
        let mut reader = BufReader::new(stdout);
        let mut line_buffer = Vec::new();
        let result=async {
   loop {
    let next=tokio::select! {
     line=bounded_line(&mut reader,&mut line_buffer)=>line?,
     _=tokio::time::sleep(Duration::from_millis(50))=>{if cancel.load(Ordering::Acquire){return Err(AppError::new("E_INTERRUPTED","mas_canceled"));}continue;},
    };
    let Some(line)=next else{break};
    if line.len()>65536{return Err(engine("store_protocol_line_size"));}
    let event:Event=serde_json::from_slice(&line).map_err(|_|engine("store_protocol_json"))?;
    let id=match &event{Event::Phase{adam_id,..}|Event::NeedsPrivilegedInstall{adam_id,..}=>Some(*adam_id),Event::Error{adam_id,..}=>adam_id.filter(|id|*id!=0)};
    if let Some(id)=id && (!expected.contains(&id)||terminal.contains_key(&id)){return Err(engine("store_protocol_target"));}
    match event {
     Event::Phase{adam_id,phase,percent}=>{
      super::progress::stage(&phase,percent)?;progress(adam_id,&phase,percent)?;
      if phase=="done"{terminal.insert(adam_id,DownloadResult{adam_id,privileged:None,error:None});}
     }
     Event::NeedsPrivilegedInstall{adam_id,pkg,receipt,app_path}=>{
      let request=PrivilegedInstall::capture(adam_id,staging,&pkg,&receipt,&app_path)?;
      progress(adam_id,"installing",None)?;
      terminal.insert(adam_id,DownloadResult{adam_id,privileged:Some(request),error:None});
     }
     Event::Error{adam_id,code,domain,error_code,message}=>{
      let allowed=["E_INTERRUPTED","E_MAS_NOT_SIGNED_IN","E_MAS_NOT_OWNED","E_MAS_PAID","E_MAS_OTHER_ACCOUNT","E_MAS_REGION","E_MAS_RATE_LIMITED","E_MAS_ENGINE"];
      let code=if allowed.contains(&code.as_str()){code.as_str()}else{"E_MAS_ENGINE"};
      let error=AppError::new(code,message.unwrap_or_else(||"store_engine_error".into())).with_detail(serde_json::json!({"domain":domain,"code":error_code}).to_string());
      if let Some(adam_id)=adam_id.filter(|id|*id!=0){terminal.insert(adam_id,DownloadResult{adam_id,privileged:None,error:Some(error)});}else{return Err(error);}
     }
    }
   }
   Ok::<_,AppError>(())
  }.await;
        // Closing stdin cancels CommerceKit before terminating a stuck child. Never
        // leave an orphan purchase running when the protocol/parent fails.
        drop(stdin);
        let exit_error = match tokio::time::timeout(Duration::from_secs(3), child.wait()).await {
            Ok(Ok(status)) if status.success() => None,
            // main.swift exits 1 after reporting all items when any item failed.
            // This is not a process failure and must not revoke another item's
            // validated privileged-install request. Signals, missing results and
            // protocol errors still invalidate pending privileged work.
            Ok(Ok(status))
                if status.code() == Some(1)
                    && result.is_ok()
                    && terminal.len() == expected.len()
                    && terminal.values().any(|item| item.error.is_some()) =>
            {
                None
            }
            Ok(Ok(_)) => Some(engine("store_exit_failed")),
            Ok(Err(_)) => Some(engine("store_wait")),
            Err(_) => {
                let _ = child.kill().await;
                let _ = child.wait().await;
                Some(engine("store_exit_timeout"))
            }
        };
        stderr_reader.abort();
        let failure = result.err().or(exit_error);
        // A protocol/process failure cannot undo an installation already completed.
        // Preserve the outer error contract when no per-item result was received.
        if terminal.is_empty()
            && let Some(error) = &failure
            && error.code != "E_INTERRUPTED"
        {
            return Err(error.clone());
        }
        Ok(ids
            .iter()
            .map(|id| {
                let mut item = terminal.remove(id).unwrap_or_else(|| DownloadResult {
                    adam_id: *id,
                    privileged: None,
                    error: Some(
                        failure
                            .clone()
                            .unwrap_or_else(|| engine("store_incomplete")),
                    ),
                });
                if item.privileged.is_some() && failure.is_some() {
                    item.privileged = None;
                    item.error = failure.clone();
                }
                item
            })
            .collect())
    }
}
fn engine(message: &str) -> AppError {
    AppError::new("E_MAS_ENGINE", message)
}
pub fn validate_staging(path: &Path, uid: u32) -> Result<(), AppError> {
    use std::os::unix::fs::MetadataExt;
    if !crate::uninstall::rules::parents_safe(path) {
        return Err(engine("staging_symlink_parent"));
    }
    let meta = std::fs::symlink_metadata(path)?;
    if !meta.is_dir() || meta.is_symlink() || meta.uid() != uid || meta.mode() & 0o777 != 0o700 {
        return Err(engine("staging_owner_mode"));
    }
    Ok(())
}
impl PrivilegedInstall {
    pub fn capture(
        adam_id: u64,
        staging: &Path,
        pkg: &Path,
        receipt: &Path,
        app_path: &Path,
    ) -> Result<Self, AppError> {
        if pkg.parent() != Some(staging)
            || receipt.parent() != Some(staging)
            || pkg == receipt
            || app_path.extension().is_none_or(|e| e != "app")
            || !app_path.is_absolute()
        {
            return Err(engine("store_artifact_path"));
        }
        let pkg_snapshot = Snapshot::capture(pkg)?;
        let receipt_snapshot = Snapshot::capture(receipt)?;
        if pkg_snapshot.kind != 0
            || receipt_snapshot.kind != 0
            || pkg_snapshot.length == 0
            || receipt_snapshot.length == 0
            || receipt_snapshot.length > 4 * 1024 * 1024
        {
            return Err(engine("store_artifact_type"));
        }
        Ok(Self {
            adam_id,
            bundle_id: None,
            staging: staging.into(),
            staging_snapshot: Snapshot::capture(staging)?,
            pkg: pkg.into(),
            pkg_snapshot,
            receipt: receipt.into(),
            receipt_snapshot,
            app_path: app_path.into(),
            app_snapshot: match std::fs::symlink_metadata(app_path) {
                Ok(_) => Some(Snapshot::capture(app_path)?),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => None,
                Err(e) => return Err(e.into()),
            },
            parent_snapshot: Snapshot::capture(
                app_path.parent().ok_or_else(|| engine("app_parent"))?,
            )?,
        })
    }
    pub fn validate_destination(&self) -> Result<(), AppError> {
        let parent = self.app_path.parent().ok_or_else(|| engine("app_parent"))?;
        let same = match &self.app_snapshot {
            Some(snapshot) => snapshot.kind == 1 && snapshot.still(&self.app_path),
            None => std::fs::symlink_metadata(&self.app_path)
                .is_err_and(|e| e.kind() == std::io::ErrorKind::NotFound),
        };
        if !same
            || !self.parent_snapshot.still(parent)
            || !crate::uninstall::rules::parents_safe(&self.app_path)
        {
            return Err(engine("store_destination_changed"));
        }
        Ok(())
    }
    pub fn validate(&self, uid: u32) -> Result<(), AppError> {
        validate_staging(&self.staging, uid)?;
        if !super::valid_adam_id(self.adam_id)
            || !self.staging_snapshot.still(&self.staging)
            || !self.pkg_snapshot.matches(&self.pkg)
            || !self.receipt_snapshot.matches(&self.receipt)
        {
            return Err(engine("store_artifact_changed"));
        }
        self.validate_destination()?;
        Self::capture(
            self.adam_id,
            &self.staging,
            &self.pkg,
            &self.receipt,
            &self.app_path,
        )
        .map(|_| ())
    }
}

async fn bounded_line<R: tokio::io::AsyncBufRead + Unpin>(
    reader: &mut R,
    line: &mut Vec<u8>,
) -> Result<Option<Vec<u8>>, AppError> {
    loop {
        let available = reader
            .fill_buf()
            .await
            .map_err(|_| engine("store_protocol_read"))?;
        if available.is_empty() {
            return if line.is_empty() {
                Ok(None)
            } else {
                Err(engine("store_truncated_line"))
            };
        }
        let newline = available.iter().position(|b| *b == b'\n');
        let amount = newline.map_or(available.len(), |i| i + 1);
        if line.len() + amount > 65536 {
            return Err(engine("store_protocol_line_size"));
        }
        line.extend_from_slice(&available[..amount]);
        reader.consume(amount);
        if newline.is_some() {
            line.pop();
            return Ok(Some(std::mem::take(line)));
        }
    }
}
