//! Resolve a `MEDIA:` path to something the webview can display.
//!
//! Local files go through Tauri's asset protocol, which supports byte ranges for video and
//! audio. Remote panel images are read as data URLs. Remote audio and video play through a
//! loopback proxy: the panel ticket stays in this process and the webview only loads
//! `http://127.0.0.1`.

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager};

use super::{AppError, panel};

/// Images are decoded by the webview, so they get the same order of headroom as the attachment
/// preview. Audio and video are streamed through the asset protocol; the cap only keeps a
/// mislabelled multi-gigabyte file out of the renderer.
const MAX_IMAGE_BYTES: u64 = 16 * 1024 * 1024;
const MAX_STREAM_BYTES: u64 = 1024 * 1024 * 1024;

const HERMES_CACHE_SUBDIRS: &[&str] = &[
    "images",
    "audio",
    "videos",
    "documents",
    "screenshots",
    "generated",
];
const HERMES_LEGACY_CACHE_DIRS: &[&str] = &[
    "image_cache",
    "audio_cache",
    "video_cache",
    "document_cache",
    "browser_screenshots",
];
const DENIED_SYSTEM_PREFIXES: &[&str] = &[
    "/etc", "/proc", "/sys", "/dev", "/boot", "/var/log", "/var/lib", "/var/run",
];
const DENIED_HOME_SUBPATHS: &[&str] = &[
    ".ssh",
    ".aws",
    ".gnupg",
    ".kube",
    ".docker",
    ".config",
    ".azure",
    ".gcloud",
    "Library/Keychains",
];
const HERMES_CREDENTIAL_PATHS: &[&str] = &[
    ".env",
    "auth.json",
    "auth.lock",
    "credentials",
    "config.yaml",
    ".anthropic_oauth.json",
    "google_token.json",
    "google_oauth_pending.json",
    "auth/google_oauth.json",
    "webhook_subscriptions.json",
    "bws_cache.json",
    "bws_cache.enc.json",
    "op_cache.json",
    "photon-sidecar.json",
    "cache/bws_cache.json",
    "cache/bws_cache.enc.json",
    "cache/op_cache.json",
    "runtime/photon-sidecar.json",
    "pairing",
    "mcp-tokens",
    "webapp",
    "sessions",
    "browser-profile",
    "state.db",
    "state.db-wal",
    "state.db-shm",
    "state.db-journal",
    "kanban.db",
    "kanban.db-wal",
    "kanban.db-shm",
    "kanban.db-journal",
];
const KANBAN_DB_FILES: &[&str] = &[
    "kanban.db",
    "kanban.db-wal",
    "kanban.db-shm",
    "kanban.db-journal",
];

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesMediaInput {
    pub path: String,
    pub kind: String,
    pub profile_id: Option<String>,
    pub profile: Option<String>,
    pub session_id: Option<String>,
    pub cwd: Option<String>,
}

#[derive(Debug, Clone, Serialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct HermesMediaSource {
    pub remote: bool,
    pub path: String,
    pub data_url: Option<String>,
    pub stream_url: Option<String>,
    pub size: Option<u64>,
}

fn media_error(detail: impl Into<String>) -> AppError {
    AppError::new("E_HERMES_MEDIA", "Could not read this Hermes media file.")
        .with_detail(detail.into())
}

fn canonical_or_self(path: &Path) -> PathBuf {
    std::fs::canonicalize(path).unwrap_or_else(|_| path.to_path_buf())
}

fn hermes_homes(home: &Path, hermes_home: &Path) -> Vec<PathBuf> {
    let mut roots = vec![
        canonical_or_self(hermes_home),
        canonical_or_self(&home.join(".hermes")),
    ];
    let mut index = 0;
    while index < roots.len() {
        let root = roots[index].clone();
        if let Ok(entries) = std::fs::read_dir(root.join("profiles")) {
            for entry in entries.flatten() {
                let path = entry.path();
                if path.is_dir() {
                    let path = canonical_or_self(&path);
                    if !roots.contains(&path) {
                        roots.push(path);
                    }
                }
            }
        }
        index += 1;
    }
    roots
}

fn is_hermes_cache_path(path: &Path, homes: &[PathBuf]) -> bool {
    homes.iter().any(|root| {
        HERMES_CACHE_SUBDIRS
            .iter()
            .any(|subdir| path.starts_with(root.join("cache").join(subdir)))
            || HERMES_LEGACY_CACHE_DIRS
                .iter()
                .any(|subdir| path.starts_with(root.join(subdir)))
    })
}

/// Named Kanban boards keep their own `kanban.db` beside the attachments directory that delivery
/// is allowed to use. Those databases hold task, comment and run transcripts.
fn is_kanban_board_db_path(path: &Path, homes: &[PathBuf], home: &Path) -> bool {
    let mut roots = homes.to_vec();
    if let Some(root) = std::env::var_os("HERMES_KANBAN_HOME")
        .map(PathBuf::from)
        .filter(|root| !root.as_os_str().is_empty())
    {
        let root = if root == Path::new("~") {
            home.to_path_buf()
        } else if let Ok(rest) = root.strip_prefix("~") {
            home.join(rest)
        } else {
            root
        };
        let root = canonical_or_self(&root);
        if !roots.contains(&root) {
            roots.push(root);
        }
    }
    roots.iter().any(|root| {
        let boards = canonical_or_self(&root.join("kanban").join("boards"));
        let Ok(relative) = path.strip_prefix(&boards) else {
            return false;
        };
        let mut components = relative.components();
        let (Some(_board), Some(file)) = (components.next(), components.next()) else {
            return false;
        };
        if components.next().is_some() {
            return false;
        }
        let file = file.as_os_str().to_string_lossy();
        KANBAN_DB_FILES.iter().any(|name| file.as_ref() == *name)
    })
}

/// Local `MEDIA:` paths are model-authored. Match the official delivery policy closely enough that
/// prompt injection cannot turn a credential or conversation store into a rendered/opened file.
fn is_protected_local_media_path(path: &Path, home: &Path, hermes_home: &Path) -> bool {
    let path = canonical_or_self(path);
    let home = canonical_or_self(home);
    let hermes_home = canonical_or_self(hermes_home);
    let homes = hermes_homes(&home, &hermes_home);
    // Hermes' own generated media is safe even when the runtime home sits under `/root` or another
    // broadly denied prefix.
    if is_hermes_cache_path(&path, &homes) {
        return false;
    }
    for prefix in DENIED_SYSTEM_PREFIXES {
        let denied = canonical_or_self(Path::new(prefix));
        if denied != home && path.starts_with(&denied) {
            return true;
        }
    }
    for subpath in DENIED_HOME_SUBPATHS {
        if path.starts_with(canonical_or_self(&home.join(subpath))) {
            return true;
        }
    }
    for root in &homes {
        for relative in HERMES_CREDENTIAL_PATHS {
            if path.starts_with(canonical_or_self(&root.join(relative))) {
                return true;
            }
        }
    }
    if is_kanban_board_db_path(&path, &homes, &home) {
        return true;
    }
    if let Some(data_root) = hermes_home.parent() {
        for relative in [".opennavo", "audit.jsonl"] {
            if path.starts_with(canonical_or_self(&data_root.join(relative))) {
                return true;
            }
        }
    }
    false
}

fn panel_id(input: &HermesMediaInput) -> Option<&str> {
    input
        .profile_id
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
}

/// Expand `file:` URLs and `~/`, then anchor relative paths on the session's working directory.
/// The async resolver canonicalizes the result with `tokio::fs` before touching the asset scope.
pub fn resolve_local_path(raw: &str, cwd: Option<&str>, home: &Path) -> Result<PathBuf, AppError> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err(media_error("The media path is empty."));
    }

    let expanded = if trimmed == "~" {
        home.to_path_buf()
    } else if let Some(rest) = trimmed.strip_prefix("~/") {
        home.join(rest)
    } else if trimmed.to_ascii_lowercase().starts_with("file:") {
        reqwest::Url::parse(trimmed)
            .ok()
            .and_then(|url| url.to_file_path().ok())
            .ok_or_else(|| media_error("The file URL is not a local path."))?
    } else {
        PathBuf::from(trimmed)
    };

    let absolute = if expanded.is_absolute() {
        expanded
    } else {
        let base = cwd
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .map(PathBuf::from)
            .unwrap_or_else(|| home.to_path_buf());
        let base = if base.is_absolute() {
            base
        } else {
            home.join(base)
        };
        base.join(expanded)
    };

    Ok(absolute)
}

/// Path to send a panel file route.
///
/// `file:` URLs are decoded the way Hermes desktop's `filePathFromMediaPath` decodes them.
/// The URL's own shape decides Windows versus POSIX, so a Mac client can name a file on a
/// Windows or Linux panel. `%2f` stays encoded: decoding it would name a different file.
/// A value that is not a `file:` URL is only trimmed.
pub(crate) fn panel_media_path(raw: &str) -> String {
    let trimmed = raw.trim();
    if !trimmed.to_ascii_lowercase().starts_with("file:") {
        return trimmed.to_owned();
    }
    file_url_to_native_path(trimmed).unwrap_or_else(|| strip_file_scheme(trimmed))
}

fn strip_file_scheme(value: &str) -> String {
    let bytes = value.as_bytes();
    if bytes.len() >= 7 && bytes[..7].eq_ignore_ascii_case(b"file://") {
        value[7..].to_owned()
    } else {
        value.to_owned()
    }
}

fn file_url_to_native_path(value: &str) -> Option<String> {
    let url = reqwest::Url::parse(value).ok()?;
    if !url.scheme().eq_ignore_ascii_case("file") {
        return None;
    }
    let pathname = url.path();
    if pathname.to_ascii_lowercase().contains("%2f") {
        return None;
    }
    let decoded = percent_decode(pathname)?;
    let hostname = url.host_str().unwrap_or("");
    let windows = !hostname.is_empty() || drive_path(&decoded);
    if windows && pathname.to_ascii_lowercase().contains("%5c") {
        return None;
    }
    if encodes_another_path(&decoded, windows) {
        return None;
    }
    if !windows {
        return Some(decoded);
    }
    let native = if hostname.is_empty() {
        decoded.get(1..).unwrap_or(&decoded).to_owned()
    } else {
        format!("//{hostname}{decoded}")
    };
    Some(native.replace('/', "\\"))
}

fn drive_path(path: &str) -> bool {
    let mut chars = path.chars();
    chars.next() == Some('/')
        && chars
            .next()
            .is_some_and(|letter| letter.is_ascii_alphabetic())
        && chars.next() == Some(':')
}

fn encodes_another_path(path: &str, windows: bool) -> bool {
    let lower = path.to_ascii_lowercase();
    if lower.contains("%2f") || (windows && lower.contains("%5c")) {
        return true;
    }
    if windows {
        path.split(['/', '\\']).any(encoded_dot_segment)
    } else {
        path.split('/').any(encoded_dot_segment)
    }
}

fn encoded_dot_segment(segment: &str) -> bool {
    if segment.is_empty() || segment.len() > 6 {
        return false;
    }
    let bytes = segment.as_bytes();
    let mut index = 0;
    let mut units = 0;
    while index < bytes.len() {
        if bytes[index] == b'.' {
            units += 1;
            index += 1;
        } else if index + 3 <= bytes.len()
            && bytes[index] == b'%'
            && bytes[index + 1].eq_ignore_ascii_case(&b'2')
            && bytes[index + 2].eq_ignore_ascii_case(&b'e')
        {
            units += 1;
            index += 3;
        } else {
            return false;
        }
        if units > 2 {
            return false;
        }
    }
    (1..=2).contains(&units)
}

fn percent_decode(input: &str) -> Option<String> {
    let bytes = input.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            if index + 2 >= bytes.len() {
                return None;
            }
            let hex = std::str::from_utf8(&bytes[index + 1..index + 3]).ok()?;
            out.push(u8::from_str_radix(hex, 16).ok()?);
            index += 3;
        } else {
            out.push(bytes[index]);
            index += 1;
        }
    }
    String::from_utf8(out).ok()
}

/// The kind is inferred from the extension rather than trusting the renderer: only a real media
/// extension may widen the asset protocol scope.
fn media_kind_for_path(path: &Path) -> &'static str {
    let extension = path
        .extension()
        .and_then(|value| value.to_str())
        .map(|value| value.to_ascii_lowercase())
        .unwrap_or_default();
    match extension.as_str() {
        "png" | "jpg" | "jpeg" | "gif" | "webp" | "bmp" | "tiff" | "svg" | "heic" | "heif"
        | "avif" => "image",
        "mp4" | "m4v" | "mov" | "avi" | "mkv" | "webm" | "3gp" => "video",
        "mp3" | "m2a" | "wav" | "ogg" | "opus" | "m4a" | "flac" | "aac" => "audio",
        _ => "file",
    }
}

pub async fn resolve(
    app: &AppHandle,
    input: HermesMediaInput,
) -> Result<HermesMediaSource, AppError> {
    if let Some(profile_id) = panel_id(&input) {
        let mut source = HermesMediaSource {
            remote: true,
            path: input.path.clone(),
            data_url: None,
            stream_url: None,
            size: None,
        };
        let path = panel_media_path(&input.path);
        match input.kind.as_str() {
            "image" => {
                source.data_url = Some(
                    panel::read_data_url(
                        profile_id,
                        &path,
                        input.profile.as_deref(),
                        input.session_id.as_deref(),
                    )
                    .await?,
                );
            }
            "video" | "audio" => {
                let upstream = panel::file_stream_request(
                    profile_id,
                    &path,
                    input.profile.as_deref(),
                    input.session_id.as_deref(),
                )
                .await?;
                source.stream_url = Some(super::media_proxy::publish_with_cookie(
                    upstream.url,
                    upstream.cookie,
                )?);
            }
            _ => {}
        }
        return Ok(source);
    }

    let home = app
        .path()
        .home_dir()
        .map_err(|error| media_error(error.to_string()))?;
    let hermes_home = super::hermes_home(app)?;
    let path = resolve_local_path(&input.path, input.cwd.as_deref(), &home)?;
    let path = tokio::fs::canonicalize(&path)
        .await
        .map_err(|error| media_error(format!("Could not resolve {}: {error}", path.display())))?;
    if is_protected_local_media_path(&path, &home, &hermes_home) {
        return Err(media_error(format!(
            "{} is not allowed as local Hermes media.",
            path.display()
        )));
    }
    let metadata = tokio::fs::metadata(&path)
        .await
        .map_err(|error| media_error(format!("{}: {error}", path.display())))?;
    if !metadata.is_file() {
        return Err(media_error(format!(
            "{} is not a regular file.",
            path.display()
        )));
    }
    let limit = match input.kind.as_str() {
        "image" if media_kind_for_path(&path) == "image" => Some(MAX_IMAGE_BYTES),
        "video" if media_kind_for_path(&path) == "video" => Some(MAX_STREAM_BYTES),
        "audio" if media_kind_for_path(&path) == "audio" => Some(MAX_STREAM_BYTES),
        "image" | "video" | "audio" => {
            return Err(media_error(format!(
                "{} does not match the declared media kind.",
                path.display()
            )));
        }
        _ => None,
    };
    if let Some(limit) = limit {
        if metadata.len() > limit {
            return Err(media_error(format!(
                "{} is too large to preview.",
                path.display()
            )));
        }
        app.asset_protocol_scope()
            .allow_file(&path)
            .map_err(|error| media_error(error.to_string()))?;
    }
    Ok(HermesMediaSource {
        remote: false,
        path: path.to_string_lossy().into_owned(),
        data_url: None,
        stream_url: None,
        size: Some(metadata.len()),
    })
}

pub async fn open_url(input: HermesMediaInput) -> Result<String, AppError> {
    let Some(profile_id) = panel_id(&input) else {
        return Ok(String::new());
    };
    panel::file_download_url(
        profile_id,
        &panel_media_path(&input.path),
        input.profile.as_deref(),
        input.session_id.as_deref(),
    )
    .await
}

#[cfg(test)]
mod tests {
    use super::*;

    fn home() -> PathBuf {
        PathBuf::from("/Users/test")
    }

    #[test]
    fn expands_a_home_relative_media_path() {
        assert_eq!(
            resolve_local_path("~/shots/a b.png", None, &home()).unwrap(),
            PathBuf::from("/Users/test/shots/a b.png")
        );
    }

    #[test]
    fn turns_a_file_url_back_into_a_native_path() {
        assert_eq!(
            resolve_local_path("file:///tmp/a%20b.png", None, &home()).unwrap(),
            PathBuf::from("/tmp/a b.png")
        );
    }

    #[test]
    fn anchors_a_relative_path_on_the_session_directory() {
        assert_eq!(
            resolve_local_path("out/clip.mp4", Some("/tmp/project"), &home()).unwrap(),
            PathBuf::from("/tmp/project/out/clip.mp4")
        );
    }

    #[test]
    fn rejects_an_empty_path() {
        assert!(resolve_local_path("   ", None, &home()).is_err());
    }

    #[test]
    fn panel_paths_decode_file_urls_without_opening_encoded_separators() {
        assert_eq!(
            panel_media_path("/home/u/.hermes/images/a.png"),
            "/home/u/.hermes/images/a.png"
        );
        assert_eq!(panel_media_path("file:///tmp/a%20b.png"), "/tmp/a b.png");
        assert_eq!(
            panel_media_path("file:///C:/Users/me/a%20b.png"),
            r"C:\Users\me\a b.png"
        );
        assert_eq!(
            panel_media_path("file://nas/share/a%20b.png"),
            r"\\nas\share\a b.png"
        );
        assert_eq!(
            panel_media_path("file:///tmp/..%2f..%2fetc/a.png"),
            "/tmp/..%2f..%2fetc/a.png"
        );
        assert_eq!(panel_media_path("file:///tmp/100%.png"), "/tmp/100%.png");
    }

    #[test]
    fn detects_the_kind_from_the_extension_case_insensitively() {
        assert_eq!(media_kind_for_path(Path::new("/tmp/a.PNG")), "image");
        assert_eq!(media_kind_for_path(Path::new("/tmp/a.mov")), "video");
        assert_eq!(media_kind_for_path(Path::new("/tmp/a.flac")), "audio");
        assert_eq!(media_kind_for_path(Path::new("/tmp/a.key")), "file");
        assert_eq!(media_kind_for_path(Path::new("/tmp/no-extension")), "file");
    }

    #[test]
    fn blocks_credentials_but_allows_generated_hermes_media() {
        let dir = tempfile::tempdir().unwrap();
        let home = dir.path().join("home");
        let data_root = dir.path().join("data");
        let hermes_home = data_root.join("hermes");
        let profile = hermes_home.join("profiles").join("work");
        for path in [
            home.join(".ssh").join("id_rsa"),
            home.join(".config").join("secret.json"),
            hermes_home.join(".env"),
            hermes_home.join("auth.json"),
            hermes_home.join("sessions").join("chat.json"),
            hermes_home.join("bws_cache.json"),
            hermes_home.join("cache").join("op_cache.json"),
            hermes_home.join("runtime").join("photon-sidecar.json"),
            hermes_home
                .join("kanban")
                .join("boards")
                .join("alpha")
                .join("kanban.db"),
            hermes_home
                .join("kanban")
                .join("boards")
                .join("alpha")
                .join("kanban.db-wal"),
            profile.join(".env"),
            data_root.join(".opennavo").join("session.json"),
            data_root.join("audit.jsonl"),
        ] {
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(path, b"secret").unwrap();
        }
        let cache = hermes_home
            .join("profiles")
            .join("work")
            .join("cache")
            .join("images")
            .join("shot.png");
        std::fs::create_dir_all(cache.parent().unwrap()).unwrap();
        std::fs::write(&cache, b"png").unwrap();
        let attachment = hermes_home
            .join("kanban")
            .join("attachments")
            .join("brief.pdf");
        std::fs::create_dir_all(attachment.parent().unwrap()).unwrap();
        std::fs::write(&attachment, b"pdf").unwrap();
        let board_attachment = hermes_home
            .join("kanban")
            .join("boards")
            .join("alpha")
            .join("attachments")
            .join("brief.pdf");
        std::fs::create_dir_all(board_attachment.parent().unwrap()).unwrap();
        std::fs::write(&board_attachment, b"pdf").unwrap();
        let ordinary = dir.path().join("shot.png");
        std::fs::write(&ordinary, b"png").unwrap();

        for denied in [
            home.join(".ssh").join("id_rsa"),
            home.join(".config").join("secret.json"),
            hermes_home.join(".env"),
            hermes_home.join("auth.json"),
            hermes_home.join("sessions").join("chat.json"),
            hermes_home.join("bws_cache.json"),
            hermes_home.join("cache").join("op_cache.json"),
            hermes_home.join("runtime").join("photon-sidecar.json"),
            hermes_home
                .join("kanban")
                .join("boards")
                .join("alpha")
                .join("kanban.db"),
            hermes_home
                .join("kanban")
                .join("boards")
                .join("alpha")
                .join("kanban.db-wal"),
            profile.join(".env"),
            data_root.join(".opennavo").join("session.json"),
            data_root.join("audit.jsonl"),
        ] {
            assert!(
                is_protected_local_media_path(&denied, &home, &hermes_home),
                "{} must be denied",
                denied.display()
            );
        }
        assert!(!is_protected_local_media_path(&cache, &home, &hermes_home));
        assert!(!is_protected_local_media_path(
            &attachment,
            &home,
            &hermes_home
        ));
        assert!(!is_protected_local_media_path(
            &board_attachment,
            &home,
            &hermes_home
        ));
        assert!(!is_protected_local_media_path(
            &ordinary,
            &home,
            &hermes_home
        ));
    }
}
