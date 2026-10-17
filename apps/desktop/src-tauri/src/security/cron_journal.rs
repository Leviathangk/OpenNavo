//! Durable, current-user crontab edits. The CLI has no atomic compare-and-swap:
//! persist intent, check again, write, then reconcile against exact before/after.
use super::{
    legacy_startup::{Undo, backup_key, digest, fingerprint},
    *,
};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};

const KEY: &str = "security_cron_journal_v2";
#[derive(Clone, Serialize, Deserialize)]
struct Line {
    text: String,
    id: String,
}
#[derive(Clone, Copy, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
enum WritePhase {
    Prepared,
    // Round-one journals did not track this boundary. Treat them conservatively.
    #[default]
    WriteUnknown,
}
#[derive(Clone, Serialize, Deserialize)]
struct Pending {
    #[serde(default)]
    phase: WritePhase,
    before: Option<String>,
    after: Option<String>,
    removed: BTreeSet<usize>,
}
#[derive(Clone, Serialize, Deserialize)]
pub(super) struct Journal {
    version: u8,
    epoch: String,
    original: Option<String>,
    lines: Vec<Line>,
    removed: BTreeSet<usize>,
    expected: Option<String>,
    pending: Option<Pending>,
}
fn conflict() -> AppError {
    AppError::new("E_STARTUP_CHANGED", "cron_changed")
}
impl Journal {
    fn new(text: Option<String>) -> Self {
        let epoch = uuid::Uuid::now_v7().to_string();
        let lines = text
            .as_deref()
            .unwrap_or("")
            .split_inclusive('\n')
            .enumerate()
            .map(|(index, text)| Line {
                text: text.into(),
                id: format!("cron:{}", digest(&format!("{epoch}:{index}"))),
            })
            .collect();
        Self {
            version: 2,
            epoch,
            lines,
            original: text.clone(),
            expected: text,
            removed: BTreeSet::new(),
            pending: None,
        }
    }
    fn render(&self, removed: &BTreeSet<usize>) -> String {
        self.lines
            .iter()
            .enumerate()
            .filter(|(i, _)| !removed.contains(i))
            .map(|(_, l)| l.text.as_str())
            .collect()
    }
    fn label(&self, index: usize) -> String {
        format!("v2:{}:{index}", self.epoch)
    }
    fn validate(&self) -> Result<(), AppError> {
        if self.version != 2
            || self.original.as_deref().unwrap_or("")
                != self
                    .lines
                    .iter()
                    .map(|l| l.text.as_str())
                    .collect::<String>()
            || self.removed.iter().any(|i| *i >= self.lines.len())
            || self.expected.as_deref().unwrap_or("") != self.render(&self.removed)
        {
            return Err(conflict());
        }
        Ok(())
    }
    // Bind visible positions only when the scanner observed the exact expected
    // document. Duplicate commands retain distinct original occurrence IDs.
    fn bind(&mut self, items: &[StartupItem]) {
        let text = self.expected.as_deref().unwrap_or("");
        for (visible, original) in (0..self.lines.len())
            .filter(|i| !self.removed.contains(i))
            .enumerate()
        {
            if let Some(item) = items.iter().find(|item| {
                item.category == StartupCategory::Cron
                    && item.state != StartupState::Removed
                    && item.label.as_deref() == Some(&fingerprint(text, visible))
            }) {
                self.lines[original].id = item.id.clone();
            }
        }
    }
}
impl Service {
    fn load_cron(&self) -> Result<Option<Journal>, AppError> {
        let journal: Option<Journal> = self.s2_get(KEY)?;
        if let Some(j) = &journal {
            j.validate()?;
        }
        Ok(journal)
    }
    /// Commit journal, undo references and card state together, never around OS I/O.
    fn save_cron(&self, journal: &Journal) -> Result<(), AppError> {
        self.database.with(|c| {
            let tx = c.unchecked_transaction()?;
            tx.execute("INSERT INTO meta(key,value) VALUES(?1,?2) ON CONFLICT(key) DO UPDATE SET value=excluded.value", params![KEY, serde_json::to_string(journal)?])?;
            let items = {
                let mut stmt = tx.prepare("SELECT value FROM security_startup")?;
                stmt.query_map([], |r| r.get::<_, String>(0))?.collect::<rusqlite::Result<Vec<_>>>()?
            };
            for json in items {
                let mut item: StartupItem = serde_json::from_str(&json)?;
                if item.category != StartupCategory::Cron { continue; }
                let Some(index) = journal.lines.iter().position(|l| l.id == item.id) else { continue; };
                let removed = journal.removed.contains(&index);
                item.label = Some(journal.label(index));
                item.state = if removed { StartupState::Removed } else { StartupState::Enabled };
                item.actions = if removed { vec![StartupAction::Restore] } else { vec![StartupAction::Remove, StartupAction::Reveal] };
                item.is_new = false;
                tx.execute("UPDATE security_startup SET value=?2 WHERE id=?1", params![item.id, serde_json::to_string(&item)?])?;
                let key = backup_key(&item.id);
                if removed {
                    let undo = Undo::CronJournal { epoch: journal.epoch.clone(), occurrence: index };
                    tx.execute("INSERT INTO meta(key,value) VALUES(?1,?2) ON CONFLICT(key) DO UPDATE SET value=excluded.value", params![key, serde_json::to_string(&undo)?])?;
                } else {
                    tx.execute("DELETE FROM meta WHERE key=?1", [key])?;
                }
            }
            tx.commit()?;
            Ok(())
        })
    }
    // Only external read failures and content conflicts are category-local.
    // Storage/serialization failures still propagate instead of disappearing.
    fn reconcile_cron_status(&self) -> Result<Option<AppError>, AppError> {
        let Some(mut journal) = self.load_cron()? else {
            return Ok(None);
        };
        let Some(pending) = journal.pending.clone() else {
            return Ok(None);
        };
        if pending.phase == WritePhase::Prepared {
            // No OS write can have started in this phase, even after a crash or
            // failed cancellation commit. Preserve every existing undo/card.
            journal.pending = None;
            self.s2_set(KEY, &journal)?;
            return Ok(None);
        }
        let current = match self.commands.crontab_read() {
            Ok(current) => current,
            Err(error) => return Ok(Some(error)),
        };
        if current == pending.after {
            journal.expected = pending.after;
            journal.removed = pending.removed;
        } else if current != pending.before {
            return Ok(Some(conflict())); // retain uncertain intent and every undo
        }
        journal.pending = None;
        self.save_cron(&journal)?;
        Ok(None)
    }
    pub(super) fn reconcile_cron(&self) -> Result<(), AppError> {
        match self.reconcile_cron_status()? {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }
    pub(super) fn cron_scan_issue(&self) -> Result<Option<AppError>, AppError> {
        if let Some(error) = self.reconcile_cron_status()? {
            return Ok(Some(error));
        }
        // Earlier removals still depend on the exact expected document even
        // when this action was canceled before writing and has no pending now.
        if let Some(journal) = self.load_cron()?
            && !journal.removed.is_empty()
        {
            return Ok(match self.commands.crontab_read() {
                Ok(current) if current == journal.expected => None,
                Ok(_) => Some(conflict()),
                Err(error) => Some(error),
            });
        }
        Ok(None)
    }
    pub(super) fn map_cron_scan(&self, snapshot: &mut StartupSnapshot) -> Result<(), AppError> {
        let Some(journal) = self.load_cron()? else {
            return Ok(());
        };
        let text = journal.expected.as_deref().unwrap_or("");
        let mut mapping = BTreeMap::new();
        for (visible, original) in (0..journal.lines.len())
            .filter(|i| !journal.removed.contains(i))
            .enumerate()
        {
            mapping.insert(fingerprint(text, visible), original);
        }
        for item in &mut snapshot.items {
            if item.category != StartupCategory::Cron {
                continue;
            }
            if let Some(index) = item.label.as_ref().and_then(|label| mapping.get(label)) {
                item.id = journal.lines[*index].id.clone();
                item.label = Some(journal.label(*index));
            }
        }
        Ok(())
    }
    fn migrate_cron(
        &self,
        current: &Option<String>,
        items: &[StartupItem],
    ) -> Result<Option<Journal>, AppError> {
        let rows = self.database.with(|c| {
            let mut stmt = c.prepare(
                "SELECT key,value FROM meta WHERE key LIKE 'security_s2_startup_undo:%'",
            )?;
            Ok(stmt
                .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?
                .collect::<rusqlite::Result<Vec<_>>>()?)
        })?;
        let mut edges = vec![];
        for (key, json) in rows {
            if let Undo::Cron { before, after } = serde_json::from_str(&json)? {
                let id = key
                    .strip_prefix("security_s2_startup_undo:")
                    .ok_or_else(conflict)?;
                let item = items
                    .iter()
                    .find(|i| {
                        i.id == id
                            && i.category == StartupCategory::Cron
                            && i.state == StartupState::Removed
                    })
                    .ok_or_else(conflict)?;
                let index = checked_index(item, before.as_deref().unwrap_or(""))?;
                let mut lines: Vec<_> = before
                    .as_deref()
                    .unwrap_or("")
                    .split_inclusive('\n')
                    .collect();
                lines.remove(index);
                if lines.concat() != after {
                    return Err(conflict());
                }
                edges.push((item.id.clone(), before, after, index));
            }
        }
        if edges.is_empty() {
            return Ok(None);
        }
        let mut tail = current.clone();
        let mut chain = vec![];
        while !edges.is_empty() {
            let matches: Vec<_> = edges
                .iter()
                .enumerate()
                .filter(|(_, e)| Some(&e.2) == tail.as_ref())
                .map(|(i, _)| i)
                .collect();
            if matches.len() != 1 {
                return Err(conflict());
            }
            let edge = edges.remove(matches[0]);
            tail = edge.1.clone();
            chain.push(edge);
        }
        let mut journal = Journal::new(tail);
        for (id, before, after, visible) in chain.into_iter().rev() {
            if journal.expected != before {
                return Err(conflict());
            }
            let index = (0..journal.lines.len())
                .filter(|i| !journal.removed.contains(i))
                .nth(visible)
                .ok_or_else(conflict)?;
            journal.lines[index].id = id;
            journal.removed.insert(index);
            journal.expected = Some(after);
        }
        journal.bind(items);
        self.save_cron(&journal)?;
        Ok(Some(journal))
    }
    pub(super) fn cron_act(
        &self,
        item: &mut StartupItem,
        action: StartupItemAction,
    ) -> Result<(), AppError> {
        self.reconcile_cron()?;
        let current = self.commands.crontab_read()?;
        let items = self.saved_startup()?;
        let existing = match self.load_cron()? {
            Some(journal) => Some(journal),
            None => self.migrate_cron(&current, &items)?,
        };
        let mut journal = match existing {
            Some(j) if j.expected == current => j,
            Some(j) if !j.removed.is_empty() || j.pending.is_some() => return Err(conflict()),
            _ => {
                if action == StartupItemAction::Restore {
                    return Err(conflict());
                }
                checked_index(item, current.as_deref().unwrap_or(""))?;
                let mut j = Journal::new(current.clone());
                j.bind(&items);
                j
            }
        };
        let index = journal
            .lines
            .iter()
            .position(|l| l.id == item.id)
            .ok_or_else(conflict)?;
        let remove = action == StartupItemAction::Remove;
        if journal.removed.contains(&index) == remove {
            return Err(conflict());
        }
        if !remove {
            let undo: Undo = self.s2_get(&backup_key(&item.id))?.ok_or_else(conflict)?;
            if !matches!(undo, Undo::CronJournal { epoch, occurrence } if epoch == journal.epoch && occurrence == index)
            {
                return Err(conflict());
            }
        }
        let mut removed = journal.removed.clone();
        if remove {
            removed.insert(index);
        } else {
            removed.remove(&index);
        }
        let after = Some(journal.render(&removed));
        journal.pending = Some(Pending {
            phase: WritePhase::Prepared,
            before: current.clone(),
            after: after.clone(),
            removed,
        });
        // A failed DB write means the OS is never touched. A failed final commit
        // leaves this intent durable; the next action/scan completes it exactly.
        self.s2_set(KEY, &journal)?;
        let prewrite = self.commands.crontab_read().and_then(|observed| {
            if observed == current {
                Ok(())
            } else {
                Err(conflict())
            }
        });
        if let Err(error) = prewrite {
            journal.pending = None;
            self.s2_set(KEY, &journal)?;
            return Err(error);
        }
        // Commit the uncertainty boundary before invoking the OS. A crash from
        // here on must reconcile actual text, never assume the write did not run.
        journal.pending.as_mut().expect("prepared intent").phase = WritePhase::WriteUnknown;
        self.s2_set(KEY, &journal)?;
        let result = self.commands.crontab_write(after.as_deref().unwrap_or(""));
        self.reconcile_cron()?;
        let committed = self.load_cron()?.ok_or_else(conflict)?;
        if committed.removed.contains(&index) != remove {
            return Err(result.err().unwrap_or_else(conflict));
        }
        *item = self
            .saved_startup()?
            .into_iter()
            .find(|i| i.id == item.id)
            .ok_or_else(conflict)?;
        if remove {
            self.record_event(
                ProtectionEventKind::StartupRemoved,
                &item.path,
                Some(item.id.clone()),
                true,
            )?;
        }
        Ok(())
    }
}
fn checked_index(item: &StartupItem, text: &str) -> Result<usize, AppError> {
    let (hash, index) = item
        .label
        .as_deref()
        .and_then(|l| l.rsplit_once(':'))
        .and_then(|(hash, index)| index.parse::<usize>().ok().map(|i| (hash, i)))
        .ok_or_else(conflict)?;
    if hash != digest(text)
        || text
            .split_inclusive('\n')
            .nth(index)
            .is_none_or(|l| l.trim() != item.name)
    {
        return Err(conflict());
    }
    Ok(index)
}
