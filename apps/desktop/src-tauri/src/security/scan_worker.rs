use super::*;
use crate::security::{av, scan_cache::CleanCache};
use std::{fs::File, os::unix::fs::MetadataExt, sync::Mutex};

pub(super) struct Job {
    pub path: PathBuf,
    pub file: File,
    pub snapshot: Snapshot,
}
pub(super) struct Verdict {
    pub signature: Option<String>,
    pub hash: String,
    pub cached: bool,
}
pub(super) struct Report {
    pub path: PathBuf,
    pub snapshot: Snapshot,
    pub verdict: Result<Verdict, AppError>,
    pub hash_us: u64,
    pub timing: av::ScanTiming,
}
pub(super) struct Isolated(pub Arc<av::Engine>);
impl Drop for Isolated {
    fn drop(&mut self) {
        self.0.shutdown();
        // shutdown waits for this child's exit before touching its extraction files.
        let _ = fs::remove_dir_all(&self.0.config.runtime);
    }
}
fn identity(m: &fs::Metadata) -> (u64, u64, u64, i64, i64, i64, i64) {
    (
        m.dev(),
        m.ino(),
        m.len(),
        m.mtime(),
        m.mtime_nsec(),
        m.ctime(),
        m.ctime_nsec(),
    )
}
pub(super) fn process(
    session: &av::ScanSession,
    mut job: Job,
    cache: &Mutex<CleanCache>,
    cancel: &AtomicBool,
    force: bool,
) -> Report {
    let mut hash_us = 0;
    let mut timing = av::ScanTiming::default();
    let verdict = (|| {
        files::canceled(cancel)?;
        files::same_file(&job.snapshot, &job.path)?;
        let before = identity(&job.file.metadata()?);
        let started = Instant::now();
        job.file.seek(SeekFrom::Start(0))?;
        let hash = files::hash(&mut job.file, cancel)?;
        hash_us = started.elapsed().as_micros() as u64;
        files::same_file(&job.snapshot, &job.path)?;
        if before != identity(&job.file.metadata()?) {
            return Err(AppError::new("E_PROTECTED", "security_object_changed"));
        }
        session.validate()?;
        let key = CleanCache::key(&session.fingerprint, &hash);
        if !force
            && cache
                .lock()
                .map_err(super::super::poison)?
                .contains(&key, super::super::now())
        {
            return Ok(Verdict {
                signature: None,
                hash,
                cached: true,
            });
        }
        cache.lock().map_err(super::super::poison)?.remove(&key);
        let verdict = session.scan(&mut job.file, cancel, &mut timing);
        let signature = verdict?;
        files::same_file(&job.snapshot, &job.path)?;
        if before != identity(&job.file.metadata()?) {
            return Err(AppError::new("E_PROTECTED", "security_object_changed"));
        }
        if signature.is_none() {
            cache
                .lock()
                .map_err(super::super::poison)?
                .insert(key, super::super::now());
        }
        Ok(Verdict {
            signature,
            hash,
            cached: false,
        })
    })();
    Report {
        path: job.path,
        snapshot: job.snapshot,
        verdict,
        hash_us,
        timing,
    }
}
