//! Local verification only. No execution, mounting, upload, or external reputation service.
use super::*;
use crate::uninstall::rules::Snapshot;
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    fs,
    io::{Seek, SeekFrom},
};
const MAX_DOWNLOAD: u64 = 8 * 1024 * 1024 * 1024;

#[derive(Clone)]
pub(crate) struct Session {
    detected: Snapshot,
    detected_at: std::time::Instant,
    verdict: Option<DownloadVerdict>,
}
impl Service {
    // Only the watcher can grant a path this run-scoped capability. A direct
    // download_verify call never grants permission to open an arbitrary file.
    pub(crate) fn download_detected(&self, path: &Path, token: String) -> Result<(), AppError> {
        self.own_data()?.check(path)?;
        files::safe_metadata(path)?;
        let snapshot = Snapshot::capture(path)?;
        let mut sessions = self.downloads_session.lock().map_err(poison)?;
        if sessions.len() >= 256
            && !sessions.contains_key(path)
            && let Some(oldest) = sessions
                .iter()
                .min_by_key(|(_, session)| session.detected_at)
                .map(|(path, _)| path.clone())
        {
            sessions.remove(&oldest);
        }
        sessions.insert(
            path.to_owned(),
            Session {
                detected: snapshot,
                detected_at: std::time::Instant::now(),
                verdict: None,
            },
        );
        drop(sessions);
        (self.sink)(CoreEvent::DownloadDetected(DownloadDetected {
            path: path.to_string_lossy().into_owned(),
            token,
        }));
        Ok(())
    }
    pub fn download_act(&self, path: PathBuf, action: DownloadAction) -> Result<(), AppError> {
        let _guard = self.mutation.lock().map_err(poison)?;
        let session = self
            .downloads_session
            .lock()
            .map_err(poison)?
            .get(&path)
            .cloned()
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "download_not_detected"))?;
        let verdict = session
            .verdict
            .ok_or_else(|| AppError::new("E_INVALID_ARG", "download_not_verified"))?;
        let quarantine = self
            .quarantine_list()?
            .into_iter()
            .find(|q| q.original_path == verdict.path);
        let unsafe_file = verdict.quarantined
            || quarantine.is_some()
            || verdict.av.as_ref().is_some_and(|v| !v.clean)
            || verdict.team_matches == Some(false)
            || matches!(
                verdict.signature.status,
                SignatureStatus::Invalid | SignatureStatus::Modified
            );
        if action == DownloadAction::Open && unsafe_file {
            return Err(AppError::new("E_DOWNLOAD_UNSAFE", "download_unsafe"));
        }
        // A quarantined file can still be revealed, but only through its recorded vault entry.
        let reveal_path = if let Some(q) = quarantine {
            let (_, meta) = self.quarantine_record(&q.id)?;
            let stored = meta
                .stored
                .ok_or_else(|| AppError::new("E_INVALID_ARG", "download_quarantine_pending"))?;
            let stored_path = self.vault().join(q.id);
            files::same_file(&stored, &stored_path)?;
            stored_path
        } else {
            files::same_file(&session.detected, &path)
                .map_err(|_| AppError::new("E_INVALID_ARG", "download_changed"))?;
            path.clone()
        };
        if action == DownloadAction::Open {
            let own_data = self.own_data()?;
            if !matches!(
                verdict.format,
                DownloadFormat::Dmg | DownloadFormat::Pkg | DownloadFormat::Zip
            ) {
                return Err(AppError::new(
                    "E_INVALID_ARG",
                    "download_installer_required",
                ));
            }
            let hash = files::hash_up_to(
                &mut own_data.open_read(&path, MAX_DOWNLOAD)?,
                &AtomicBool::new(false),
                MAX_DOWNLOAD,
            )?;
            if hash != verdict.sha256 {
                return Err(AppError::new("E_INVALID_ARG", "download_changed"));
            }
            files::same_file(&session.detected, &path)?;
            self.commands
                .run("/usr/bin/open", &["--", &path.to_string_lossy()])?;
        } else {
            self.commands.run(
                "/usr/bin/open",
                &["-R", "--", &reveal_path.to_string_lossy()],
            )?;
        }
        Ok(())
    }

    pub fn download_verify(
        self: &Arc<Self>,
        path: PathBuf,
        token: Option<String>,
    ) -> Result<DownloadVerdict, AppError> {
        let own_data = self.own_data()?;
        // Explicit verification must not bypass the scan policy through signature
        // inspection or SHA-256 reads, even when AV is disabled or unavailable.
        own_data.check(&path)?;
        let metadata = files::safe_metadata(&path)?;
        let root_snapshot = Snapshot::capture(&path)?;
        let cancel = AtomicBool::new(false);
        let format = match path
            .extension()
            .and_then(|s| s.to_str())
            .map(str::to_ascii_lowercase)
            .as_deref()
        {
            Some("app") => DownloadFormat::App,
            Some("dmg") => DownloadFormat::Dmg,
            Some("pkg") => DownloadFormat::Pkg,
            Some("zip") => DownloadFormat::Zip,
            _ => DownloadFormat::Other,
        };
        let signature = signature::inspect_guarded(&path, Some(&own_data))?;
        let team = if let Some(token) = token {
            let item = crate::catalog::get(&self.database, crate::model::Kind::Link, &token)?
                .ok_or_else(|| AppError::new("E_NOT_FOUND", "download_catalog_item"))?;
            item.link.and_then(|l| l.team_id)
        } else {
            None
        };
        let team_matches = team
            .as_deref()
            .zip(signature.team_id.as_deref())
            .map(|(expected, actual)| expected == actual);
        let settings = self.settings.read().map_err(poison)?.clone();
        let consent = settings.av_consent;
        let av_excluded = scan::exclusions(&self.home, &settings.av_exclusions, &self.data);
        let mut av_skipped = if !consent {
            Some(AvSkipReason::NoConsent)
        } else if self.av.ready().is_err() {
            Some(AvSkipReason::NoDatabase)
        } else if av_excluded.iter().any(|p| path.starts_with(p)) {
            Some(AvSkipReason::Excluded)
        } else if metadata.is_file() && metadata.len() > own_data.scan_limit(&path)? {
            Some(AvSkipReason::TooLarge)
        } else {
            None
        };
        let lease = if av_skipped.is_none() {
            Some(self.av.lease(&cancel)?)
        } else {
            None
        };
        let mut size = 0;
        let mut hashes = BTreeMap::new();
        let mut threat = None;
        let mut av_complete = true;
        if metadata.is_dir() && format != DownloadFormat::App {
            return Err(AppError::new("E_INVALID_ARG", "download_bundle_required"));
        }
        let mut skipped = AvSkipped::default();
        let mut visit = |file_path: &Path| {
            let before = Snapshot::capture(file_path)?;
            let mut file = own_data.open_read(file_path, MAX_DOWNLOAD)?;
            let hash = files::hash_up_to(&mut file, &cancel, MAX_DOWNLOAD)?;
            hashes.insert(
                file_path
                    .strip_prefix(&path)
                    .unwrap_or(file_path)
                    .to_owned(),
                hash,
            );
            size += before.length;
            if lease.is_some() && !av_excluded.iter().any(|p| file_path.starts_with(p)) {
                file.seek(SeekFrom::Start(0))?;
                match self.av.scan(&mut file, &cancel) {
                    Ok(Some(found)) => threat = Some(found),
                    Ok(None) => {}
                    Err(e) => {
                        av_complete = false;
                        av_skipped = Some(if e.message == "av_scan_too_large" {
                            AvSkipReason::TooLarge
                        } else {
                            AvSkipReason::Unreadable
                        });
                    }
                }
            } else {
                av_complete = false;
                if lease.is_some() {
                    av_skipped = Some(AvSkipReason::Excluded);
                }
            }
            files::same_file(&before, file_path)?;
            Ok(())
        };
        if metadata.is_file() {
            visit(&path)?;
        } else {
            scan::walk_guarded(
                vec![path.clone()],
                &[],
                &cancel,
                &mut skipped,
                &|_, m| files::dataless(m),
                &mut visit,
                Some(&own_data),
            )?;
        }
        if skipped.dataless > 0 || skipped.unreadable > 0 || skipped.too_large > 0 {
            return Err(AppError::new("E_PROTECTED", "download_incomplete_read"));
        }
        files::same_file(&root_snapshot, &path)?;
        let sha256 = if metadata.is_file() {
            hashes
                .into_values()
                .next()
                .ok_or_else(|| AppError::new("E_PROTECTED", "download_not_read"))?
        } else {
            let mut digest = Sha256::new();
            for (relative, hash) in hashes {
                digest.update(relative.as_os_str().as_encoded_bytes());
                digest.update([0]);
                digest.update(hash.as_bytes());
            }
            format!("{:x}", digest.finalize())
        };
        let av = lease
            .as_ref()
            .filter(|_| av_complete || threat.is_some())
            .map(|_| AvVerdict {
                clean: threat.is_none(),
                signature: threat.clone(),
                scanned_at: now(),
            });
        if threat.is_some() {
            self.record_event(
                ProtectionEventKind::DownloadThreat,
                &path.to_string_lossy(),
                None,
                false,
            )?;
        } else if signature.status == SignatureStatus::Unsigned {
            self.record_event(
                ProtectionEventKind::DownloadUnsigned,
                &path.to_string_lossy(),
                None,
                false,
            )?;
        }
        let result = DownloadVerdict {
            path: path.to_string_lossy().into_owned(),
            sha256,
            size,
            format,
            signature,
            team_matches,
            av_skipped: if av.is_some() { None } else { av_skipped },
            av,
            quarantined: false,
        };
        if let Some(session) = self
            .downloads_session
            .lock()
            .map_err(poison)?
            .get_mut(&path)
        {
            if files::same_file(&session.detected, &path).is_ok() {
                session.verdict = Some(result.clone());
            } else {
                session.verdict = None;
            }
        }
        (self.sink)(CoreEvent::DownloadVerified(result.clone()));
        Ok(result)
    }
    pub(crate) fn recent_download_token(&self, path: &Path) -> Result<Option<String>, AppError> {
        let name = path
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or_default();
        let own_data = self.own_data()?;
        own_data.check(path)?;
        let origins = download_origins(path, &own_data);
        self.database.with(|c| {
            let mut statement = c.prepare("SELECT token,url FROM link_activity WHERE opened_at>?1 ORDER BY opened_at DESC LIMIT 30")?;
            let rows = statement.query_map([now()-900_000], |r| Ok((r.get::<_,String>(0)?,r.get::<_,String>(1)?)))?
                .collect::<rusqlite::Result<Vec<_>>>()?;
            for (token, url) in rows {
                if origins.contains(&url) || reqwest::Url::parse(&url).ok()
                    .and_then(|u| u.path_segments().and_then(|mut s| s.next_back()).map(str::to_owned))
                    .is_some_and(|s| s == name) {
                    return Ok(Some(token));
                }
            }
            Ok(None)
        })
    }
    pub(crate) fn downloads(&self) -> Vec<PathBuf> {
        if !self.download_monitor_enabled() {
            return vec![];
        }
        let root = self.home.join("Downloads");
        if files::safe_metadata(&root).is_err() {
            return vec![];
        }
        fs::read_dir(root)
            .into_iter()
            .flatten()
            .filter_map(|e| e.ok().map(|e| e.path()))
            .filter(|p| watched_download(p))
            .collect()
    }
    pub(crate) fn download_monitor_enabled(&self) -> bool {
        self.settings
            .read()
            .is_ok_and(|s| s.av_consent && s.watch_downloads)
            || self
                .database
                .with(|c| {
                    Ok(c.query_row(
                        "SELECT EXISTS(SELECT 1 FROM link_activity WHERE opened_at>?1)",
                        [now() - 900_000],
                        |r| r.get::<_, bool>(0),
                    )?)
                })
                .unwrap_or(false)
    }
}

// Browser-recorded download/referrer metadata. Read a bounded attribute through
// the already checked file descriptor; no Spotlight crawl or browser access.
fn download_origins(path: &Path, guard: &own_data::OwnData) -> Vec<String> {
    #[cfg(target_os = "macos")]
    {
        use std::os::fd::AsRawFd;
        let Ok(file) = guard.open_read(path, MAX_DOWNLOAD) else {
            return vec![];
        };
        let mut bytes = vec![0u8; 64 * 1024];
        let n = unsafe {
            nix::libc::fgetxattr(
                file.as_raw_fd(),
                c"com.apple.metadata:kMDItemWhereFroms".as_ptr(),
                bytes.as_mut_ptr().cast(),
                bytes.len(),
                0,
                0,
            )
        };
        if n <= 0 {
            return vec![];
        }
        bytes.truncate(n as usize);
        plist::from_bytes::<plist::Value>(&bytes)
            .ok()
            .and_then(|v| v.into_array())
            .unwrap_or_default()
            .iter()
            .filter_map(|v| v.as_string().map(str::to_owned))
            .collect()
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (path, guard);
        vec![]
    }
}

/// Files the Downloads watch checks (plan §6, S1): every finished download, not
/// only installers; installers also get their signature checked. Partial files
/// are skipped until the browser renames them, and hidden files are never downloads.
pub(crate) fn watched_download(path: &Path) -> bool {
    let Some(name) = path.file_name().and_then(|n| n.to_str()) else {
        return false;
    };
    let extension = path
        .extension()
        .and_then(|e| e.to_str())
        .map(str::to_ascii_lowercase);
    !name.starts_with('.')
        && !matches!(
            extension.as_deref(),
            Some("download" | "crdownload" | "part" | "partial" | "opdownload")
        )
        && (extension.as_deref() == Some("app")
            || std::fs::symlink_metadata(path).is_ok_and(|m| m.is_file()))
}

#[cfg(test)]
mod smoke_tests {
    #[test]
    fn the_watch_takes_every_finished_download_but_not_partial_or_hidden_files() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path().canonicalize().unwrap();
        let files = [
            "report.pdf",
            "tool.dmg",
            "opennavo-eicar-watch.com",
            "Archive.ZIP",
            "movie.mp4.crdownload",
            "video.part",
            ".DS_Store",
        ];
        for name in files {
            std::fs::write(root.join(name), b"x").unwrap();
        }
        // Safari keeps an unfinished download in a .download folder.
        std::fs::create_dir_all(root.join("setup.dmg.download")).unwrap();
        std::fs::create_dir_all(root.join("Tool.app/Contents")).unwrap();
        std::fs::create_dir(root.join("Folder")).unwrap();
        let mut watched: Vec<_> = std::fs::read_dir(&root)
            .unwrap()
            .map(|e| e.unwrap().path())
            .filter(|p| super::watched_download(p))
            .map(|p| p.file_name().unwrap().to_string_lossy().into_owned())
            .collect();
        watched.sort();
        assert_eq!(
            watched,
            [
                "Archive.ZIP",
                "Tool.app",
                "opennavo-eicar-watch.com",
                "report.pdf",
                "tool.dmg"
            ]
        );
    }

    use super::*;
    #[test]
    fn download_monitor_respects_consent_settings_and_recent_link_activity() {
        let (_temp, s, _) = super::super::tests::fixture();
        fs::create_dir(s.home.join("Downloads")).unwrap();
        let path = s.home.join("Downloads/app.dmg");
        fs::write(&path, "fixture").unwrap();
        assert!(s.downloads().is_empty());
        s.settings.write().unwrap().av_consent = true;
        assert_eq!(s.downloads(), vec![path.clone()]);
        s.settings.write().unwrap().watch_downloads = false;
        assert!(s.downloads().is_empty());
        s.database.with(|c| {
            c.execute("INSERT INTO link_activity(token,target,url,opened_at) VALUES('fixture','download','https://example.invalid/app.dmg',?1)", [now()])?;
            Ok(())
        }).unwrap();
        assert_eq!(s.downloads(), vec![path]);
        s.database
            .with(|c| {
                c.execute("UPDATE link_activity SET opened_at=0", [])?;
                Ok(())
            })
            .unwrap();
        assert!(s.downloads().is_empty());
    }
    #[test]
    fn old_sessions_are_evicted_without_blocking_new_verification() {
        let (_temp, s, _) = super::super::tests::fixture();
        for id in 0..257 {
            let path = s.home.join(format!("download-{id}.zip"));
            fs::write(&path, b"fixture").unwrap();
            s.download_detected(&path, "example.app".into()).unwrap();
        }
        let old = s.home.join("download-0.zip");
        let newest = s.home.join("download-256.zip");
        assert_eq!(s.downloads_session.lock().unwrap().len(), 256);
        assert_eq!(
            s.download_act(old, DownloadAction::Reveal)
                .unwrap_err()
                .message,
            "download_not_detected"
        );
        s.download_verify(newest.clone(), None).unwrap();
        s.download_act(newest, DownloadAction::Reveal).unwrap();
    }

    #[test]
    fn modified_downloads_are_unsafe_but_not_code_is_neutral() {
        let (_temp, s, _) = super::super::tests::fixture();
        let path = s.home.join("download.zip");
        fs::write(&path, b"PK\x03\x04fixture").unwrap();
        s.download_detected(&path, "example.app".into()).unwrap();
        let verdict = s.download_verify(path.clone(), None).unwrap();
        assert_eq!(verdict.signature.status, SignatureStatus::NotCode);
        assert!(
            !s.events(20)
                .unwrap()
                .iter()
                .any(|e| e.kind == ProtectionEventKind::DownloadUnsigned)
        );
        s.download_act(path.clone(), DownloadAction::Open).unwrap();
        for status in [SignatureStatus::Modified, SignatureStatus::Invalid] {
            s.downloads_session
                .lock()
                .unwrap()
                .get_mut(&path)
                .unwrap()
                .verdict
                .as_mut()
                .unwrap()
                .signature
                .status = status;
            assert_eq!(
                s.download_act(path.clone(), DownloadAction::Open)
                    .unwrap_err()
                    .code,
                "E_DOWNLOAD_UNSAFE"
            );
            s.download_act(path.clone(), DownloadAction::Reveal)
                .unwrap();
        }
    }
}
