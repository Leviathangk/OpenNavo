use super::{
    admin::{Action, Request},
    files, *,
};
use crate::{AppError, uninstall::rules::Snapshot};
use rusqlite::{OptionalExtension, params};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::{Seek, SeekFrom},
    os::unix::fs::{MetadataExt, PermissionsExt},
    path::{Path, PathBuf},
    sync::atomic::AtomicBool,
};
#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct Metadata {
    pub original: Snapshot,
    pub stored: Option<Snapshot>,
    pub mode: u32,
    pub system: bool,
    pub startup: Option<StartupItem>,
    pub finding: Option<String>,
    #[serde(default)]
    pub findings: Vec<String>,
    pub state: String,
}
impl Metadata {
    fn finding_ids(&self) -> Vec<String> {
        let mut ids = self.findings.clone();
        if let Some(id) = &self.finding
            && !ids.contains(id)
        {
            ids.push(id.clone());
        }
        ids
    }
}

// Call inside the same transaction as the action's other persisted state.
fn findings_status(
    c: &rusqlite::Connection,
    ids: &[String],
    status: FindingStatus,
    snapshot: Option<&Snapshot>,
) -> Result<(), AppError> {
    for id in ids {
        c.execute(
            "UPDATE security_findings SET value=json_set(value,'$.status',?2),snapshot=COALESCE(?3,snapshot) WHERE id=?1",
            params![id, serde_json::to_value(status)?.as_str(), snapshot.map(serde_json::to_string).transpose()?],
        )?;
        c.execute(
            "UPDATE protection_events SET value=json_set(value,'$.handled',json(?2)) WHERE json_extract(value,'$.itemId')=?1",
            params![id, if status == FindingStatus::Open { "false" } else { "true" }],
        )?;
    }
    Ok(())
}
pub(crate) fn valid_ids(ids: &[String]) -> Result<(), AppError> {
    if ids.is_empty()
        || ids.len() > 256
        || ids.iter().collect::<std::collections::BTreeSet<_>>().len() != ids.len()
    {
        return Err(AppError::new("E_INVALID_ARG", "security_action_ids"));
    }
    Ok(())
}
impl Service {
    pub(crate) fn vault(&self) -> PathBuf {
        self.data.join("quarantine")
    }
    pub fn quarantine_list(&self) -> Result<Vec<QuarantineEntry>, AppError> {
        self.database.with(|c| {
            let mut s =
                c.prepare("SELECT value,metadata FROM security_quarantine ORDER BY id DESC")?;
            let rows = s
                .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            let mut result = vec![];
            for (value, meta) in rows {
                let entry: QuarantineEntry = serde_json::from_str(&value)?;
                let meta: Metadata = serde_json::from_str(&meta)?;
                if meta.state == "stored"
                    || (meta.state == "pending" && self.vault().join(&entry.id).exists())
                {
                    result.push(entry);
                }
            }
            Ok(result)
        })
    }
    pub(crate) fn quarantine_record(
        &self,
        id: &str,
    ) -> Result<(QuarantineEntry, Metadata), AppError> {
        self.database.with(|c| {
            let row = c
                .query_row(
                    "SELECT value,metadata FROM security_quarantine WHERE id=?1",
                    [id],
                    |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)),
                )
                .optional()?
                .ok_or_else(|| AppError::new("E_NOT_FOUND", "quarantine_entry"))?;
            Ok((serde_json::from_str(&row.0)?, serde_json::from_str(&row.1)?))
        })
    }
    // A path or digest alone is not a threat instance: require the exact scanned
    // object, its content metadata and its verified parent chain as well.
    fn related_findings(
        &self,
        path: &str,
        hash: &str,
        snapshot: &Snapshot,
    ) -> Result<Vec<String>, AppError> {
        self.database.with(|c| {
            let mut statement = c.prepare("SELECT id,snapshot FROM security_findings WHERE json_extract(value,'$.path')=?1 AND json_extract(value,'$.sha256')=?2 AND json_extract(value,'$.status')='open'")?;
            let rows = statement.query_map(params![path, hash], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?.collect::<rusqlite::Result<Vec<_>>>()?;
            let mut ids = Vec::new();
            for (id, saved) in rows {
                if serde_json::from_str::<Snapshot>(&saved)? == *snapshot {
                    ids.push(id);
                }
            }
            Ok(ids)
        })
    }
    pub(crate) fn admin_run(
        &self,
        requests: &[Request],
    ) -> Result<Vec<admin::ResultItem>, AppError> {
        if requests.is_empty() {
            return Ok(vec![]);
        }
        self.admin
            .read()
            .map_err(poison)?
            .as_ref()
            .ok_or_else(|| AppError::new("E_ADMIN_CANCELED", "security_admin_unavailable"))?
            .run(requests)
    }
    pub(crate) fn prepare_quarantine(
        &self,
        path: &Path,
        snapshot: &Snapshot,
        reason: QuarantineReason,
        startup: Option<StartupItem>,
        finding: Option<String>,
    ) -> Result<(QuarantineEntry, Metadata, Request), AppError> {
        let own_data = self.own_data()?;
        own_data.check(path)?;
        files::same_file(snapshot, path)?;
        let meta = files::safe_metadata(path)?;
        if meta.nlink() != 1
            || !meta.is_file()
            || path.starts_with("/System")
            || path.starts_with("/usr")
            || path.starts_with("/bin")
            || path.starts_with("/sbin")
            || path.starts_with(&self.data)
        {
            return Err(AppError::new("E_PROTECTED", "quarantine_protected_path"));
        }
        let mut file = own_data.open_read(path, own_data.scan_limit(path)?)?;
        let hash = files::hash(&mut file, &AtomicBool::new(false))?;
        files::same_file(snapshot, path)?;
        files::private_dir(&self.vault())?;
        let entry = QuarantineEntry {
            id: uuid::Uuid::now_v7().to_string(),
            name: path
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into_owned(),
            original_path: path.to_string_lossy().into_owned(),
            size_bytes: meta.len(),
            sha256: hash,
            reason,
            quarantined_at: now(),
        };
        let metadata = Metadata {
            original: snapshot.clone(),
            stored: None,
            mode: meta.mode() & 0o777,
            system: requires_admin(path),
            startup,
            findings: self.related_findings(&entry.original_path, &entry.sha256, snapshot)?,
            finding,
            state: "pending".into(),
        };
        self.database.with(|c| {
            c.execute(
                "INSERT INTO security_quarantine(id,value,metadata) VALUES(?1,?2,?3)",
                params![
                    entry.id,
                    serde_json::to_string(&entry)?,
                    serde_json::to_string(&metadata)?
                ],
            )?;
            Ok(())
        })?;
        let request = Request {
            id: entry.id.clone(),
            action: Action::QuarantineMove {
                source: path.into(),
                destination: self.vault().join(&entry.id),
                snapshot: snapshot.clone(),
            },
        };
        Ok((entry, metadata, request))
    }
    pub(crate) fn finish_quarantine(
        &self,
        entry: &QuarantineEntry,
        mut meta: Metadata,
    ) -> Result<(), AppError> {
        let stored = Snapshot::capture(&self.vault().join(&entry.id))?;
        if stored.device != meta.original.device || stored.inode != meta.original.inode {
            return Err(AppError::new("E_PROTECTED", "quarantine_identity"));
        }
        meta.stored = Some(stored);
        meta.state = "stored".into();
        self.database.with(|c| {
            let tx = c.transaction()?;
            tx.execute(
                "UPDATE security_quarantine SET metadata=?2 WHERE id=?1",
                params![entry.id, serde_json::to_string(&meta)?],
            )?;
            findings_status(&tx, &meta.finding_ids(), FindingStatus::Quarantined, None)?;
            tx.commit()?;
            Ok(())
        })?;
        self.changed(SecurityChangeReason::Events);
        self.changed(SecurityChangeReason::Quarantine);
        Ok(())
    }
    pub(crate) fn move_user(&self, request: &Request) -> Result<(), AppError> {
        let Action::QuarantineMove {
            source,
            destination,
            snapshot,
        } = &request.action
        else {
            return Err(AppError::new("E_INVALID_ARG", "quarantine_action"));
        };
        admin::quarantine_move(source, destination, snapshot)
    }
    pub fn quarantine_restore(&self, ids: Vec<String>) -> Result<ActOutcome, AppError> {
        valid_ids(&ids)?;
        let _guard = self.mutation.lock().map_err(poison)?;
        self.startup_revision.fetch_add(1, Ordering::Relaxed);
        let mut outcome = ActOutcome::default();
        let mut pending = vec![];
        for id in ids {
            let result = (|| {
                let (entry, mut meta) = self.quarantine_record(&id)?;
                let source = self.vault().join(&id);
                let destination = PathBuf::from(&entry.original_path);
                if fs::symlink_metadata(&destination).is_ok() {
                    return Err(AppError::new(
                        "E_RESTORE_CONFLICT",
                        "restore_destination_exists",
                    ));
                }
                let snapshot = meta
                    .stored
                    .clone()
                    .map(Ok)
                    .unwrap_or_else(|| meta.original.moved_to(&source))?;
                files::same_file(&snapshot, &source)?;
                // No executable permissions in the vault. The recorded mode is restored at the final move.
                // Also recover older pending records that incorrectly recorded system=false.
                if meta.system || requires_admin_to(&source, &destination) {
                    pending.push((
                        entry,
                        meta,
                        Request {
                            id: id.clone(),
                            action: Action::QuarantineRestore {
                                source,
                                destination,
                                snapshot,
                                mode: self.quarantine_record(&id)?.1.mode,
                            },
                        },
                    ));
                } else {
                    fs::set_permissions(&source, fs::Permissions::from_mode(meta.mode))?;
                    let verified = (|| {
                        let mut f = files::open_read(&source, files::MAX_INSTALLER_FILE)?;
                        let hash = files::hash(&mut f, &AtomicBool::new(false))?;
                        if hash != entry.sha256 {
                            return Err(AppError::new("E_PROTECTED", "quarantine_hash_changed"));
                        }
                        files::same_file(&snapshot, &source)?;
                        admin::move_exclusive(&source, &destination)
                    })();
                    if let Err(e) = verified {
                        let _ = fs::set_permissions(&source, fs::Permissions::from_mode(0o0));
                        return Err(e);
                    }
                    meta.state = "restored".into();
                    self.restored(&entry, &meta)?;
                    outcome.done.push(id.clone());
                }
                Ok(())
            })();
            if let Err(e) = result {
                outcome.failed.push(ActFailure { id, code: e.code });
            }
        }
        let requests: Vec<_> = pending.iter().map(|(_, _, r)| r.clone()).collect();
        let results = self.admin_run(&requests);
        for (entry, mut meta, _) in pending {
            let error = match &results {
                Ok(v) => v
                    .iter()
                    .find(|r| r.id == entry.id)
                    .map(|r| r.error.clone())
                    .unwrap_or_else(|| {
                        Some(AppError::new("E_ADMIN_CANCELED", "admin_result_missing"))
                    }),
                Err(e) => Some(e.clone()),
            };
            if let Some(e) = error {
                outcome.failed.push(ActFailure {
                    id: entry.id,
                    code: e.code,
                });
            } else {
                meta.state = "restored".into();
                match self.restored(&entry, &meta) {
                    Ok(()) => outcome.done.push(entry.id),
                    Err(e) => outcome.failed.push(ActFailure {
                        id: entry.id,
                        code: e.code,
                    }),
                }
            }
        }
        self.changed(SecurityChangeReason::Quarantine);
        Ok(outcome)
    }
    fn restored(&self, entry: &QuarantineEntry, meta: &Metadata) -> Result<(), AppError> {
        let path = Path::new(&entry.original_path);
        let expected = meta
            .stored
            .as_ref()
            .unwrap_or(&meta.original)
            .moved_to(path)?;
        files::same_file(&expected, path)?;
        let mut file = files::open_read(path, files::MAX_INSTALLER_FILE)?;
        if files::hash(&mut file, &AtomicBool::new(false))? != entry.sha256 {
            return Err(AppError::new("E_PROTECTED", "quarantine_hash_changed"));
        }
        files::same_file(&expected, path)?;
        let snapshot = Snapshot::capture(path)?;
        self.database.with(|c| {
            let tx = c.transaction()?;
            tx.execute("UPDATE security_quarantine SET metadata=?2 WHERE id=?1", params![entry.id, serde_json::to_string(meta)?])?;
            findings_status(&tx, &meta.finding_ids(), FindingStatus::Open, Some(&snapshot))?;
            if let Some(item) = &meta.startup {
                let mut item = item.clone();
                item.state = StartupState::Disabled;
                item.actions = vec![StartupAction::Enable, StartupAction::Remove, StartupAction::Reveal, StartupAction::Scan];
                tx.execute("INSERT INTO security_startup(id,value,snapshot) VALUES(?1,?2,?3) ON CONFLICT(id) DO UPDATE SET value=excluded.value,snapshot=excluded.snapshot", params![item.id, serde_json::to_string(&item)?, serde_json::to_string(&snapshot)?])?;
            }
            tx.commit()?;
            Ok(())
        })?;
        self.changed(SecurityChangeReason::Events);
        Ok(())
    }
    fn delete_quarantined(&self, id: &str) -> Result<(), AppError> {
        let (_, meta) = self.quarantine_record(id)?;
        let path = self.vault().join(id);
        let expected = meta
            .stored
            .clone()
            .map(Ok)
            .unwrap_or_else(|| meta.original.moved_to(&path))?;
        files::same_file(&expected, &path)?;
        fs::remove_file(path)?;
        self.database.with(|c| {
            let tx = c.transaction()?;
            tx.execute("DELETE FROM security_quarantine WHERE id=?1", [id])?;
            findings_status(&tx, &meta.finding_ids(), FindingStatus::Deleted, None)?;
            tx.commit()?;
            Ok(())
        })?;
        self.changed(SecurityChangeReason::Events);
        Ok(())
    }
    pub fn quarantine_delete(&self, ids: Vec<String>) -> Result<ActOutcome, AppError> {
        valid_ids(&ids)?;
        let _guard = self.mutation.lock().map_err(poison)?;
        let mut outcome = ActOutcome::default();
        for id in ids {
            let result = self.delete_quarantined(&id);
            match result {
                Ok(()) => outcome.done.push(id),
                Err(e) => outcome.failed.push(ActFailure { id, code: e.code }),
            }
        }
        self.changed(SecurityChangeReason::Quarantine);
        Ok(outcome)
    }
    pub fn findings_act(
        &self,
        scan_id: &str,
        ids: Vec<String>,
        action: FindingAction,
    ) -> Result<ActOutcome, AppError> {
        valid_ids(&ids)?;
        let _guard = self.mutation.lock().map_err(poison)?;
        if matches!(action, FindingAction::Quarantine | FindingAction::Delete) {
            self.startup_revision.fetch_add(1, Ordering::Relaxed);
        }
        let mut results = std::collections::BTreeMap::<String, Result<(), AppError>>::new();
        let mut pending = vec![];
        let mut pending_ids = std::collections::BTreeSet::new();
        let mut invalid = std::collections::BTreeMap::new();
        for id in &ids {
            let mut related = vec![id.clone()];
            let result = (|| {
                let (finding, snapshot): (AvFinding, Snapshot) = self.database.with(|c| {
                    let row = c.query_row("SELECT value,snapshot FROM security_findings WHERE id=?1 AND scan_id=?2", params![id, scan_id], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?))).optional()?.ok_or_else(|| AppError::new("E_NOT_FOUND", "security_finding"))?;
                    Ok((serde_json::from_str(&row.0)?, serde_json::from_str(&row.1)?))
                }).inspect_err(|error| { invalid.insert(id.clone(), error.clone()); })?;
                // Validate the requested scan before reusing an earlier operation's result.
                if let Some(result) = results.get(id) {
                    return result.clone();
                }
                if pending_ids.contains(id) {
                    return Ok(());
                }
                if finding.status != FindingStatus::Open {
                    return Err(AppError::new("E_PROTECTED", "finding_already_handled"));
                }
                let path = Path::new(&finding.path);
                files::same_file(&snapshot, path)?;
                if action == FindingAction::Reveal {
                    self.commands.run("/usr/bin/open", &["-R", &finding.path])?;
                    return Ok(());
                }
                let mut file = files::open_read(path, files::scan_limit(path)?)?;
                file.seek(SeekFrom::Start(0))?;
                if files::hash(&mut file, &AtomicBool::new(false))? != finding.sha256 {
                    return Err(AppError::new("E_PROTECTED", "finding_hash_changed"));
                }
                files::same_file(&snapshot, path)?;
                related = self.related_findings(&finding.path, &finding.sha256, &snapshot)?;
                if action == FindingAction::Trust {
                    self.database.with(|c| {
                        let tx = c.transaction()?;
                        tx.execute("INSERT INTO security_trusted(sha256,at) VALUES(?1,?2) ON CONFLICT(sha256) DO UPDATE SET at=excluded.at", params![finding.sha256, now()])?;
                        findings_status(&tx, &related, FindingStatus::Trusted, None)?;
                        tx.commit()?;
                        Ok(())
                    })?;
                    self.changed(SecurityChangeReason::Events);
                    return Ok(());
                }
                let (entry, meta, request) = self.prepare_quarantine(
                    path,
                    &snapshot,
                    QuarantineReason::Threat {
                        signature: finding.signature,
                    },
                    None,
                    Some(id.clone()),
                )?;
                if meta.system {
                    pending_ids.extend(related.iter().cloned());
                    pending.push((related.clone(), entry, meta, request));
                } else {
                    self.move_user(&request)?;
                    self.finish_quarantine(&entry, meta)?;
                    if action == FindingAction::Delete {
                        // Quarantined is already committed. A deletion failure must keep it recoverable.
                        self.delete_quarantined(&entry.id)?;
                    }
                }
                Ok(())
            })();
            if !pending_ids.contains(id) {
                for related_id in related {
                    results.insert(related_id, result.clone());
                }
            }
        }
        let requests: Vec<_> = pending.iter().map(|(_, _, _, r)| r.clone()).collect();
        let admin_results = self.admin_run(&requests);
        for (related, entry, meta, _) in pending {
            let result = (|| {
                let responses = admin_results.as_ref().map_err(Clone::clone)?;
                let response = responses
                    .iter()
                    .find(|r| r.id == entry.id)
                    .ok_or_else(|| AppError::new("E_ADMIN_CANCELED", "admin_result_missing"))?;
                if let Some(error) = &response.error {
                    return Err(error.clone());
                }
                self.finish_quarantine(&entry, meta)?;
                if action == FindingAction::Delete {
                    self.delete_quarantined(&entry.id)?;
                }
                Ok(())
            })();
            for related_id in related {
                results.insert(related_id, result.clone());
            }
        }
        let mut outcome = ActOutcome::default();
        for id in ids {
            match invalid
                .remove(&id)
                .map(Err)
                .or_else(|| results.remove(&id))
                .unwrap_or_else(|| Err(AppError::new("E_UNKNOWN", "finding_result_missing")))
            {
                Ok(()) => outcome.done.push(id),
                Err(e) => outcome.failed.push(ActFailure { id, code: e.code }),
            }
        }
        outcome.restorable = action == FindingAction::Quarantine && !outcome.done.is_empty();
        self.changed(SecurityChangeReason::Quarantine);
        Ok(outcome)
    }
}
