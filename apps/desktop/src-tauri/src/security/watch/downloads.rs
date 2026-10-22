//! Resident download state. Keep only paths in a complete directory listing;
//! unavailable/disabled monitoring must not turn existing files into new downloads.
use crate::security::{download, files};
use std::{
    collections::BTreeMap,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    time::{Duration, Instant, SystemTime},
};

struct Download {
    identity: (u64, u64),
    size: u64,
    modified: SystemTime,
    stable_since: Instant,
    processed: bool,
    observed: u64,
}

#[derive(Default)]
pub(super) struct DownloadWatch {
    files: BTreeMap<PathBuf, Download>,
    generation: u64,
    complete: bool,
}

impl DownloadWatch {
    pub(super) fn reconcile(&mut self, listing: Option<download::DownloadListing>) -> Vec<PathBuf> {
        let Some(listing) = listing else {
            self.complete = false;
            return vec![];
        };
        self.generation = self.generation.wrapping_add(1);
        self.complete = listing.complete;
        listing.paths
    }

    pub(super) fn finish_poll(&mut self) {
        if self.complete {
            // Observation already visited every candidate. No extra set of
            // paths or second tree lookup is needed to retire absent entries.
            self.files
                .retain(|_, file| file.observed == self.generation);
        }
        self.complete = false;
    }

    pub(super) fn ready(
        &mut self,
        path: &Path,
        baseline: Option<SystemTime>,
        now: Instant,
    ) -> bool {
        // This also checks every ancestor for symlinks/cloud placeholders.
        // Reuse the result for type filtering and stability, avoiding a
        // second metadata syscall for every file on every polling pass.
        let metadata = files::safe_metadata(path)
            .ok()
            .and_then(|meta| meta.modified().ok().map(|modified| (meta, modified)));
        let Some((meta, modified)) = metadata else {
            // A listed but temporarily unreadable path is not a deletion.
            if let Some(entry) = self.files.get_mut(path) {
                entry.observed = self.generation;
            }
            return false;
        };
        if !download::watched_download(path, &meta) {
            self.files.remove(path);
            return false;
        }
        let identity = (meta.dev(), meta.ino());
        let entry = self.files.entry(path.to_owned()).or_insert(Download {
            identity,
            size: meta.len(),
            modified,
            stable_since: now,
            processed: baseline.is_some_and(|started| modified < started),
            observed: self.generation,
        });
        entry.observed = self.generation;
        // A replacement with the same name, length and preserved mtime is
        // a new download too; a cached processed flag must not hide it.
        if entry.identity != identity || entry.size != meta.len() || entry.modified != modified {
            *entry = Download {
                identity,
                size: meta.len(),
                modified,
                stable_since: now,
                processed: false,
                observed: self.generation,
            };
        }
        !entry.processed && now.duration_since(entry.stable_since) >= Duration::from_secs(3)
    }

    pub(super) fn finish(&mut self, path: &Path, verified: bool, now: Instant) {
        if let Some(entry) = self.files.get_mut(path) {
            entry.processed = verified;
            if !verified {
                entry.stable_since = now;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    fn poll(
        watch: &mut DownloadWatch,
        paths: &[PathBuf],
        complete: bool,
        baseline: Option<SystemTime>,
        now: Instant,
    ) -> Vec<PathBuf> {
        let ready = watch
            .reconcile(Some(download::DownloadListing {
                paths: paths.to_vec(),
                complete,
            }))
            .into_iter()
            .filter(|path| watch.ready(path, baseline, now))
            .collect();
        watch.finish_poll();
        ready
    }

    #[test]
    fn startup_baseline_preserves_old_files_but_checks_downloads_written_during_startup() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let started = SystemTime::UNIX_EPOCH + Duration::from_secs(1_700_000_000);
        let paths: Vec<_> = ["old.zip", "new.pdf"].map(|name| root.join(name)).into();
        for (path, modified) in paths
            .iter()
            .zip([started - Duration::from_secs(1), started])
        {
            let file = fs::File::create(path).unwrap();
            file.set_modified(modified).unwrap();
        }
        let now = Instant::now();
        let mut watch = DownloadWatch::default();
        assert!(poll(&mut watch, &paths, true, Some(started), now).is_empty());
        assert!(poll(&mut watch, &paths, true, None, now + Duration::from_secs(2)).is_empty());
        assert_eq!(
            poll(&mut watch, &paths, true, None, now + Duration::from_secs(3)),
            vec![paths[1].clone()]
        );
    }

    #[test]
    fn writes_restart_stability_and_failed_verification_retries_without_duplicate_success() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().canonicalize().unwrap().join("download.zip");
        fs::write(&path, b"first").unwrap();
        let mut watch = DownloadWatch::default();
        let now = Instant::now();
        assert!(!watch.ready(&path, None, now));
        fs::write(&path, b"still downloading").unwrap();
        assert!(!watch.ready(&path, None, now + Duration::from_secs(2)));
        assert!(!watch.ready(&path, None, now + Duration::from_secs(4)));
        assert!(watch.ready(&path, None, now + Duration::from_secs(5)));
        watch.finish(&path, false, now + Duration::from_secs(5));
        assert!(!watch.ready(&path, None, now + Duration::from_secs(7)));
        assert!(watch.ready(&path, None, now + Duration::from_secs(8)));
        watch.finish(&path, true, now + Duration::from_secs(8));
        assert!(!watch.ready(&path, None, now + Duration::from_secs(20)));
    }

    #[test]
    fn only_complete_listings_retire_paths_and_reappearing_files_are_checked_again() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().canonicalize().unwrap().join("download.zip");
        fs::write(&path, b"fixture").unwrap();
        let mut watch = DownloadWatch::default();
        let now = Instant::now();
        assert!(!watch.ready(&path, None, now));
        watch.finish(&path, true, now);
        // Disabled monitoring, denied directory access and incomplete reads must
        // not lose processed state and cause duplicate reminders on recovery.
        assert!(watch.reconcile(None).is_empty());
        watch.finish_poll();
        assert!(poll(&mut watch, &[], false, None, now).is_empty());
        assert_eq!(watch.files.len(), 1);
        assert!(poll(&mut watch, std::slice::from_ref(&path), true, None, now).is_empty());
        // It can disappear after enumeration but before its metadata is read.
        let held = path.with_file_name("held.zip");
        fs::rename(&path, &held).unwrap();
        assert!(poll(&mut watch, std::slice::from_ref(&path), true, None, now).is_empty());
        assert_eq!(watch.files.len(), 1);
        fs::rename(&held, &path).unwrap();
        assert!(poll(&mut watch, std::slice::from_ref(&path), true, None, now).is_empty());
        // A completed empty listing bounds retained history even after years of downloads.
        assert!(poll(&mut watch, &[], true, None, now).is_empty());
        assert!(watch.files.is_empty());
        assert!(!watch.ready(&path, None, now));
        assert!(watch.ready(&path, None, now + Duration::from_secs(3)));
        // An incomplete listing still processes the readable new files it contains.
        assert_eq!(
            poll(
                &mut watch,
                std::slice::from_ref(&path),
                false,
                None,
                now + Duration::from_secs(3)
            ),
            vec![path]
        );
    }

    #[test]
    fn atomic_replacement_with_preserved_size_and_mtime_is_a_new_download() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let path = root.join("download.zip");
        let replacement = root.join("replacement.zip");
        fs::write(&path, b"old").unwrap();
        let modified = fs::metadata(&path).unwrap().modified().unwrap();
        let mut watch = DownloadWatch::default();
        let now = Instant::now();
        assert!(!watch.ready(&path, None, now));
        watch.finish(&path, true, now);
        fs::write(&replacement, b"new").unwrap();
        fs::File::options()
            .write(true)
            .open(&replacement)
            .unwrap()
            .set_modified(modified)
            .unwrap();
        fs::rename(replacement, &path).unwrap();
        assert!(!watch.ready(&path, None, now + Duration::from_secs(10)));
        assert!(watch.ready(&path, None, now + Duration::from_secs(13)));
    }

    #[test]
    fn filtering_still_rejects_links_partial_files_hidden_files_and_ordinary_directories() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let file = root.join("complete.zip");
        fs::write(&file, b"fixture").unwrap();
        std::os::unix::fs::symlink(&file, root.join("linked.zip")).unwrap();
        std::os::unix::fs::symlink(&root, root.join("linked-parent")).unwrap();
        for name in ["video.crdownload", ".hidden"] {
            fs::write(root.join(name), b"fixture").unwrap();
        }
        fs::create_dir(root.join("Folder")).unwrap();
        fs::create_dir(root.join("Tool.app")).unwrap();
        let mut watch = DownloadWatch::default();
        let now = Instant::now();
        for name in [
            "linked.zip",
            "linked-parent/complete.zip",
            "video.crdownload",
            ".hidden",
            "Folder",
        ] {
            assert!(!watch.ready(&root.join(name), None, now));
            assert!(!watch.ready(&root.join(name), None, now + Duration::from_secs(3)));
        }
        assert!(watch.files.is_empty());
        for path in [file, root.join("Tool.app")] {
            assert!(!watch.ready(&path, None, now));
            assert!(watch.ready(&path, None, now + Duration::from_secs(3)));
        }
    }

    #[test]
    #[ignore = "manual resident download polling benchmark"]
    fn download_poll_perf() {
        let (_temp, service, _) = crate::security::tests::fixture();
        service.settings.write().unwrap().av_consent = true;
        let root = service.home.join("Downloads");
        fs::create_dir(&root).unwrap();
        for index in 0..2000 {
            fs::write(root.join(format!("download-{index}.zip")), b"fixture").unwrap();
        }
        for index in 0..200 {
            fs::create_dir(root.join(format!("Folder-{index}"))).unwrap();
        }
        let mut previous = BTreeMap::new();
        let mut watch = DownloadWatch::default();
        let baseline = SystemTime::now() + Duration::from_secs(1);
        let mut old_poll = || {
            assert!(service.download_monitor_enabled());
            files::safe_metadata(&root).unwrap();
            let paths: Vec<_> = fs::read_dir(&root)
                .unwrap()
                .flatten()
                .map(|entry| entry.path())
                .filter(|path| {
                    fs::symlink_metadata(path)
                        .is_ok_and(|meta| download::watched_download(path, &meta))
                })
                .collect();
            for path in paths {
                let meta = files::safe_metadata(&path).unwrap();
                let modified = meta.modified().unwrap();
                let entry = previous
                    .entry(path.clone())
                    .or_insert((meta.len(), modified));
                assert_eq!(*entry, (meta.len(), modified));
            }
        };
        let mut new_poll = || {
            for path in watch.reconcile(service.download_candidates()) {
                assert!(!watch.ready(&path, Some(baseline), Instant::now()));
            }
            watch.finish_poll();
        };
        old_poll();
        new_poll();
        for round in 0..3 {
            let started = Instant::now();
            for _ in 0..10 {
                old_poll();
            }
            let before = started.elapsed().as_secs_f64() * 1000.0 / 10.0;
            let started = Instant::now();
            for _ in 0..10 {
                new_poll();
            }
            let after = started.elapsed().as_secs_f64() * 1000.0 / 10.0;
            eprintln!(
                "download-poll round={round} files=2000 directories=200 before_ms={before:.6} after_ms={after:.6}"
            );
        }
    }
}
