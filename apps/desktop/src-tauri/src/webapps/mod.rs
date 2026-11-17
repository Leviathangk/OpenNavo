//! Local web apps: one signed WebView helper, one thin bundle per website.
//!
//! The helper ships inside OpenNavo and is copied into every generated app. A
//! generated app is only an Info.plist, a config.json and that helper; nothing
//! is compiled on the user's Mac. Signing is ad-hoc because these apps run
//! locally. Sharing one with another Mac needs a Developer ID signature and a
//! notarization ticket, which is intentionally not part of this local flow.

use std::ffi::OsStr;
use std::io::Cursor;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::LazyLock;
use std::time::Duration;

use base64::{Engine as _, engine::general_purpose::STANDARD};
use chrono::{DateTime, SecondsFormat, Utc};
use futures_util::StreamExt;
use image::{ImageFormat, RgbaImage, imageops::FilterType};
use regex::Regex;
use serde::{Deserialize, Serialize};

use crate::error::AppError;

const HELPER_NAME: &str = "opennavo-webapp-helper";
const CONFIG_NAME: &str = "config.json";
const DEFAULT_WIDTH: u32 = 1200;
const DEFAULT_HEIGHT: u32 = 800;
const MARKER: &str = "com.opennavo.webapp";
const PAGE_LIMIT: usize = 1024 * 1024;
const IMAGE_LIMIT: usize = 2_000_000;
const MANIFEST_LIMIT: usize = 256 * 1024;

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct WebApp {
    pub id: String,
    pub name: String,
    pub url: String,
    pub path: String,
    pub bundle_id: String,
    pub created_at: String,
    pub icon: Option<String>,
}

#[derive(Debug, Clone, Deserialize, specta::Type)]
#[serde(rename_all = "camelCase")]
pub struct CreateWebApp {
    pub name: String,
    pub url: String,
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeConfig {
    marker: String,
    id: String,
    name: String,
    url: String,
    bundle_id: String,
    created_at: String,
    width: u32,
    height: u32,
}

pub fn list() -> Result<Vec<WebApp>, AppError> {
    list_at(&applications_dir()?)
}

pub async fn create(input: CreateWebApp) -> Result<WebApp, AppError> {
    let name = sanitize_name(&input.name)?;
    let url = validate_url(&input.url)?;
    let helper = helper_path()?;
    let fetched = tokio::time::timeout(Duration::from_secs(8), site_icon(&url))
        .await
        .ok()
        .flatten();
    let icon = match fetched {
        Some(image) if image.width() >= 64 && image.height() >= 64 => {
            render_site_icon(&helper, &image)
                .or_else(|_| letter_icon(&helper, &name, url.as_str()))?
        }
        _ => letter_icon(&helper, &name, url.as_str())?,
    };
    let app = build_bundle(
        &applications_dir()?,
        &helper,
        &name,
        url.as_str(),
        Some(&icon),
        Utc::now(),
    )?;
    if let Err(error) = sign_bundle(Path::new(&app.path)) {
        let _ = std::fs::remove_dir_all(&app.path);
        return Err(error);
    }
    Ok(app)
}

pub fn delete(id: &str) -> Result<(), AppError> {
    let app = find(id)?;
    let path = PathBuf::from(&app.path);
    let trash = home_dir()?.join(".Trash");
    std::fs::create_dir_all(&trash).map_err(io_error)?;
    let filename = path
        .file_name()
        .ok_or_else(|| AppError::new("E_WEBAPP_DELETE_FAILED", "webapp_path_invalid"))?;
    let destination = unique_destination(&trash, filename);
    if std::fs::rename(&path, &destination).is_err() {
        copy_dir_all(&path, &destination).map_err(io_error)?;
        std::fs::remove_dir_all(&path).map_err(io_error)?;
    }
    Ok(())
}

pub fn open(id: &str) -> Result<(), AppError> {
    let app = find(id)?;
    run_open(&[Path::new(&app.path).as_os_str()], "E_WEBAPP_OPEN_FAILED")
}

pub fn reveal(id: &str) -> Result<(), AppError> {
    let app = find(id)?;
    run_open(
        &[OsStr::new("-R"), Path::new(&app.path).as_os_str()],
        "E_WEBAPP_OPEN_FAILED",
    )
}

fn find(id: &str) -> Result<WebApp, AppError> {
    list()?
        .into_iter()
        .find(|app| app.id == id)
        .ok_or_else(|| AppError::new("E_WEBAPP_NOT_FOUND", "webapp_not_found"))
}

fn home_dir() -> Result<PathBuf, AppError> {
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .ok_or_else(|| AppError::new("E_WEBAPP_HOME", "home_missing"))
}

fn applications_dir() -> Result<PathBuf, AppError> {
    let directory = home_dir()?.join("Applications");
    std::fs::create_dir_all(&directory).map_err(io_error)?;
    Ok(directory)
}

fn helper_path() -> Result<PathBuf, AppError> {
    let executable = std::env::current_exe().map_err(io_error)?;
    if let Some(contents) = executable.parent().and_then(Path::parent) {
        let bundled = contents.join("Helpers").join(HELPER_NAME);
        if bundled.is_file() {
            return Ok(bundled);
        }
    }
    if let Some(directory) = executable.parent() {
        let sibling = directory.join(HELPER_NAME);
        if sibling.is_file() {
            return Ok(sibling);
        }
    }
    #[cfg(debug_assertions)]
    {
        let staged = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../native/staged")
            .join(HELPER_NAME);
        if staged.is_file() {
            return Ok(staged);
        }
    }
    Err(AppError::new(
        "E_WEBAPP_HELPER_MISSING",
        "webapp_helper_missing",
    ))
}

fn sanitize_name(value: &str) -> Result<String, AppError> {
    let trimmed = value.trim();
    if trimmed.is_empty()
        || trimmed == "."
        || trimmed == ".."
        || trimmed.starts_with('.')
        || trimmed.chars().count() > 64
        || trimmed
            .chars()
            .any(|character| character.is_control() || matches!(character, '/' | ':' | '\\'))
    {
        return Err(AppError::new(
            "E_WEBAPP_NAME_INVALID",
            "webapp_name_invalid",
        ));
    }
    let without_extension = trimmed
        .strip_suffix(".app")
        .or_else(|| trimmed.strip_suffix(".APP"))
        .unwrap_or(trimmed)
        .trim();
    if without_extension.is_empty() {
        return Err(AppError::new(
            "E_WEBAPP_NAME_INVALID",
            "webapp_name_invalid",
        ));
    }
    Ok(without_extension.to_owned())
}

fn validate_url(value: &str) -> Result<reqwest::Url, AppError> {
    let url = reqwest::Url::parse(value.trim())
        .map_err(|_| AppError::new("E_WEBAPP_URL_INVALID", "webapp_url_invalid"))?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err(AppError::new("E_WEBAPP_URL_INVALID", "webapp_url_invalid"));
    }
    Ok(url)
}

fn build_bundle(
    directory: &Path,
    helper: &Path,
    name: &str,
    url: &str,
    icon: Option<&RgbaImage>,
    created_at: DateTime<Utc>,
) -> Result<WebApp, AppError> {
    std::fs::create_dir_all(directory).map_err(io_error)?;
    let app_path = directory.join(format!("{name}.app"));
    if app_path.exists() {
        return Err(AppError::new("E_WEBAPP_EXISTS", "webapp_exists"));
    }

    let id = uuid::Uuid::new_v4().simple().to_string();
    let staging_path = directory.join(format!(".opennavo-webapp-{id}.tmp"));
    let built = (|| -> Result<WebApp, AppError> {
        let contents = staging_path.join("Contents");
        let macos = contents.join("MacOS");
        let resources = contents.join("Resources");
        std::fs::create_dir_all(&macos).map_err(io_error)?;
        std::fs::create_dir_all(&resources).map_err(io_error)?;

        let executable = macos.join(HELPER_NAME);
        std::fs::copy(helper, &executable).map_err(io_error)?;
        make_executable(&executable)?;

        let bundle_id = format!("com.opennavo.webapp.{}", &id[..12]);
        let created_at = created_at.to_rfc3339_opts(SecondsFormat::Secs, true);
        let config = RuntimeConfig {
            marker: MARKER.to_owned(),
            id: id.clone(),
            name: name.to_owned(),
            url: url.to_owned(),
            bundle_id: bundle_id.clone(),
            created_at: created_at.clone(),
            width: DEFAULT_WIDTH,
            height: DEFAULT_HEIGHT,
        };
        let config_json = serde_json::to_vec_pretty(&config)?;
        std::fs::write(resources.join(CONFIG_NAME), config_json).map_err(io_error)?;
        let icon = app_icon(icon, &resources)?;
        write_info_plist(
            &contents.join("Info.plist"),
            name,
            &bundle_id,
            icon.is_some(),
        )?;

        Ok(WebApp {
            id: id.clone(),
            name: name.to_owned(),
            url: url.to_owned(),
            path: app_path.to_string_lossy().into_owned(),
            bundle_id,
            created_at,
            icon,
        })
    })();

    let app = match built {
        Ok(app) => app,
        Err(error) => {
            let _ = std::fs::remove_dir_all(&staging_path);
            return Err(error);
        }
    };
    if let Err(error) = std::fs::rename(&staging_path, &app_path) {
        let _ = std::fs::remove_dir_all(&staging_path);
        if app_path.exists() {
            return Err(AppError::new("E_WEBAPP_EXISTS", "webapp_exists"));
        }
        return Err(io_error(error));
    }
    Ok(app)
}

static LINK_TAG: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?is)<link\b[^>]*>").expect("fixed link expression"));
static BASE_TAG: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?is)<base\b[^>]*>").expect("fixed base expression"));
static ATTRIBUTE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?is)([a-z][a-z0-9_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))"#)
        .expect("fixed attribute expression")
});
static SIZE_ATTRIBUTE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?i)(\d{1,4})\s*[x×]\s*(\d{1,4})").expect("fixed size expression")
});

#[derive(Debug, Clone, PartialEq, Eq)]
struct IconCandidate {
    url: reqwest::Url,
    priority: u8,
    size: u32,
}

#[derive(Debug, Default, Deserialize)]
struct WebManifest {
    #[serde(default)]
    icons: Vec<ManifestIcon>,
}

#[derive(Debug, Deserialize)]
struct ManifestIcon {
    src: String,
    #[serde(default)]
    sizes: String,
}

/// Reads the page's declared icons first, then the manifest, then the usual
/// paths. Icons below 128px are kept as a fallback but are never enlarged into
/// a full tile; anything smaller than 64px becomes a letter tile instead.
async fn site_icon(url: &reqwest::Url) -> Option<RgbaImage> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .connect_timeout(Duration::from_secs(2))
        .user_agent(
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 \
             (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
        )
        .build()
        .ok()?;

    let mut candidates = Vec::new();
    let mut base = url.clone();
    if let Some((page, final_url)) = fetch_page(&client, url).await {
        base = document_base(&page, &final_url);
        let (links, manifest) = link_candidates(&page, &base);
        candidates.extend(links);
        candidates.sort_by(|left, right| {
            right
                .size
                .cmp(&left.size)
                .then(right.priority.cmp(&left.priority))
        });
        candidates.truncate(8);
        let manifest_icons_future = async {
            let Some(manifest) = manifest else {
                return Vec::new();
            };
            let Some(json) = fetch_text(&client, &manifest, MANIFEST_LIMIT).await else {
                return Vec::new();
            };
            let mut icons = manifest_candidates(&json, &manifest);
            icons.sort_by_key(|icon| std::cmp::Reverse(icon.size));
            icons.truncate(4);
            fetch_candidates(&client, icons).await
        };
        let (mut fetched, from_manifest) =
            tokio::join!(fetch_candidates(&client, candidates), manifest_icons_future);
        fetched.extend(from_manifest);
        if let Some(image) = best_icon(fetched) {
            return Some(image);
        }
        candidates = Vec::new();
    }
    for (path, priority) in [
        ("/apple-touch-icon.png", 80),
        ("/apple-touch-icon-precomposed.png", 75),
        ("/favicon.ico", 40),
        ("/favicon.png", 40),
        ("/icon.png", 40),
    ] {
        if let Ok(candidate) = base.join(path) {
            candidates.push(IconCandidate {
                url: candidate,
                priority,
                size: 0,
            });
        }
    }

    candidates.sort_by(|left, right| {
        right
            .size
            .cmp(&left.size)
            .then(right.priority.cmp(&left.priority))
    });
    let mut seen = std::collections::HashSet::new();
    candidates.retain(|candidate| seen.insert(candidate.url.to_string()));
    best_icon(fetch_candidates(&client, candidates.into_iter().take(8).collect()).await)
}

async fn fetch_candidates(
    client: &reqwest::Client,
    candidates: Vec<IconCandidate>,
) -> Vec<(IconCandidate, RgbaImage)> {
    futures_util::stream::iter(candidates)
        .map(|candidate| async move {
            let image = fetch_image(client, &candidate.url).await;
            (candidate, image)
        })
        .buffer_unordered(6)
        .filter_map(|(candidate, image)| async move { Some((candidate, image?)) })
        .collect()
        .await
}

fn best_icon(mut fetched: Vec<(IconCandidate, RgbaImage)>) -> Option<RgbaImage> {
    fetched.sort_by(|left, right| {
        let quality = |(_, image): &(IconCandidate, RgbaImage)| image.width().min(image.height());
        quality(right)
            .cmp(&quality(left))
            .then(right.0.size.cmp(&left.0.size))
            .then(right.0.priority.cmp(&left.0.priority))
    });
    if let Some(index) = fetched
        .iter()
        .position(|(_, image)| image.width().min(image.height()) >= 128)
    {
        return Some(fetched.swap_remove(index).1);
    }
    fetched
        .into_iter()
        .find(|(_, image)| image.width().min(image.height()) >= 64)
        .map(|(_, image)| image)
}

async fn fetch_page(
    client: &reqwest::Client,
    url: &reqwest::Url,
) -> Option<(String, reqwest::Url)> {
    let response = client
        .get(url.clone())
        .timeout(Duration::from_secs(3))
        .header(reqwest::header::ACCEPT, "text/html,application/xhtml+xml")
        .send()
        .await
        .ok()?;
    if !response.status().is_success() {
        return None;
    }
    let final_url = response.url().clone();
    let bytes = read_prefix(response, PAGE_LIMIT).await?;
    Some((String::from_utf8_lossy(&bytes).into_owned(), final_url))
}

async fn fetch_text(client: &reqwest::Client, url: &reqwest::Url, limit: usize) -> Option<String> {
    let bytes = fetch_bytes(client, url, limit).await?;
    Some(String::from_utf8_lossy(&bytes).into_owned())
}

async fn fetch_image(client: &reqwest::Client, url: &reqwest::Url) -> Option<RgbaImage> {
    let bytes = fetch_bytes(client, url, IMAGE_LIMIT).await?;
    let image = image::load_from_memory(&bytes).ok()?;
    if image.width() > 4096 || image.height() > 4096 {
        return None;
    }
    let image = image.to_rgba8();
    (image.width().min(image.height()) >= 32).then_some(image)
}

async fn fetch_bytes(
    client: &reqwest::Client,
    url: &reqwest::Url,
    limit: usize,
) -> Option<Vec<u8>> {
    let response = client.get(url.clone()).send().await.ok()?;
    if !response.status().is_success()
        || response
            .content_length()
            .is_some_and(|length| length > limit as u64)
    {
        return None;
    }
    read_limited(response, limit).await
}

async fn read_limited(mut response: reqwest::Response, limit: usize) -> Option<Vec<u8>> {
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await.ok()? {
        if bytes.len() + chunk.len() > limit {
            return None;
        }
        bytes.extend_from_slice(&chunk);
    }
    Some(bytes)
}

/// Keeps the part of an HTML document that has already arrived. Large pages
/// still declare their icons in the head, so a truncated read is enough.
async fn read_prefix(mut response: reqwest::Response, limit: usize) -> Option<Vec<u8>> {
    let mut bytes = Vec::new();
    loop {
        let Ok(Some(chunk)) = response.chunk().await else {
            break;
        };
        let remaining = limit.saturating_sub(bytes.len());
        if remaining == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..chunk.len().min(remaining)]);
        if bytes.len() == limit {
            break;
        }
    }
    Some(bytes)
}

fn attributes(tag: &str) -> Vec<(String, String)> {
    ATTRIBUTE
        .captures_iter(tag)
        .filter_map(|captures| {
            let name = captures.get(1)?.as_str().to_ascii_lowercase();
            let value = captures.get(2).or(captures.get(3)).or(captures.get(4))?;
            Some((name, value.as_str().to_owned()))
        })
        .collect()
}

fn attribute<'a>(attributes: &'a [(String, String)], name: &str) -> Option<&'a str> {
    attributes
        .iter()
        .find(|(key, _)| key == name)
        .map(|(_, value)| value.as_str())
}

fn absolute_url(base: &reqwest::Url, value: &str) -> Option<reqwest::Url> {
    let value = value.trim();
    if value.is_empty() || value.starts_with("data:") {
        return None;
    }
    let url = base.join(value).ok()?;
    matches!(url.scheme(), "http" | "https").then_some(url)
}

fn icon_size(value: &str) -> u32 {
    SIZE_ATTRIBUTE
        .captures_iter(value)
        .filter_map(|captures| {
            let width = captures.get(1)?.as_str().parse::<u32>().ok()?;
            let height = captures.get(2)?.as_str().parse::<u32>().ok()?;
            Some(width.min(height))
        })
        .max()
        .unwrap_or(0)
}

fn document_base(page: &str, fallback: &reqwest::Url) -> reqwest::Url {
    BASE_TAG
        .find(page)
        .and_then(|tag| {
            let attributes = attributes(tag.as_str());
            attribute(&attributes, "href").and_then(|href| absolute_url(fallback, href))
        })
        .unwrap_or_else(|| fallback.clone())
}

fn link_candidates(page: &str, base: &reqwest::Url) -> (Vec<IconCandidate>, Option<reqwest::Url>) {
    let mut icons = Vec::new();
    let mut manifest = None;
    for tag in LINK_TAG.find_iter(page) {
        let attributes = attributes(tag.as_str());
        let Some(rel) = attribute(&attributes, "rel") else {
            continue;
        };
        let relations: Vec<String> = rel
            .split_ascii_whitespace()
            .map(str::to_ascii_lowercase)
            .collect();
        let Some(href) = attribute(&attributes, "href").and_then(|href| absolute_url(base, href))
        else {
            continue;
        };
        if href.path().to_ascii_lowercase().ends_with(".svg")
            || attribute(&attributes, "type")
                .is_some_and(|kind| kind.eq_ignore_ascii_case("image/svg+xml"))
        {
            continue;
        }
        if relations.iter().any(|relation| relation == "manifest") {
            manifest.get_or_insert(href);
            continue;
        }
        let priority = if relations.iter().any(|relation| {
            relation == "apple-touch-icon" || relation == "apple-touch-icon-precomposed"
        }) {
            90
        } else if relations.iter().any(|relation| relation == "icon") {
            50
        } else {
            continue;
        };
        let size = attribute(&attributes, "sizes").map(icon_size).unwrap_or(0);
        icons.push(IconCandidate {
            url: href,
            priority,
            size,
        });
    }
    (icons, manifest)
}

fn manifest_candidates(json: &str, base: &reqwest::Url) -> Vec<IconCandidate> {
    let Ok(manifest) = serde_json::from_str::<WebManifest>(json) else {
        return Vec::new();
    };
    manifest
        .icons
        .into_iter()
        .filter_map(|icon| {
            let src = icon.src.trim();
            let path = src
                .split(['?', '#'])
                .next()
                .unwrap_or(src)
                .to_ascii_lowercase();
            if path.ends_with(".svg") {
                return None;
            }
            Some(IconCandidate {
                url: absolute_url(base, src)?,
                priority: 100,
                size: icon_size(&icon.sizes),
            })
        })
        .collect()
}

fn render_site_icon(helper: &Path, source: &RgbaImage) -> Result<RgbaImage, AppError> {
    let mode = icon_mode(source);
    let trimmed = (mode == "full").then(|| trim_tile(source)).flatten();
    let source = trimmed.as_ref().unwrap_or(source);
    let token = uuid::Uuid::new_v4().simple().to_string();
    let input = std::env::temp_dir().join(format!("opennavo-webapp-source-{token}.png"));
    let output = std::env::temp_dir().join(format!("opennavo-webapp-render-{token}.png"));
    let mut bytes = Vec::new();
    image::DynamicImage::ImageRgba8(source.clone())
        .write_to(&mut Cursor::new(&mut bytes), ImageFormat::Png)
        .map_err(|error| {
            AppError::new("E_WEBAPP_ICON", "webapp_icon").with_detail(error.to_string())
        })?;
    std::fs::write(&input, bytes).map_err(io_error)?;
    let status = Command::new(helper)
        .arg("--render-icon")
        .arg(&input)
        .arg(&output)
        .arg(mode)
        .arg(if mode == "full" {
            plate_color(source)
        } else {
            "#FFFFFF".to_owned()
        })
        .status()
        .map_err(io_error);
    let _ = std::fs::remove_file(&input);
    let status = status?;
    if !status.success() {
        let _ = std::fs::remove_file(&output);
        return Err(AppError::new("E_WEBAPP_ICON", "webapp_icon"));
    }
    let image = image::open(&output).map_err(|error| {
        AppError::new("E_WEBAPP_ICON", "webapp_icon").with_detail(error.to_string())
    });
    let _ = std::fs::remove_file(&output);
    Ok(image?.to_rgba8())
}

fn icon_mode(source: &RgbaImage) -> &'static str {
    let width = f64::from(source.width());
    let height = f64::from(source.height());
    let ratio = if height > 0.0 { width / height } else { 0.0 };
    if width >= 128.0 && height >= 128.0 && (0.8..=1.25).contains(&ratio) {
        "full"
    } else {
        "tile"
    }
}

/// A full-bleed icon may carry a transparent margin around its own rounded
/// tile. Painting the plate in the icon's dominant colour keeps that margin
/// invisible instead of drawing a white frame inside the squircle.
fn plate_color(source: &RgbaImage) -> String {
    const SIDE: usize = 16;
    let mut counts = vec![0u32; SIDE * SIDE * SIDE];
    let mut sums = vec![[0u64; 3]; SIDE * SIDE * SIDE];
    let step = (source.width().max(source.height()) / 128).max(1);
    for y in (0..source.height()).step_by(step as usize) {
        for x in (0..source.width()).step_by(step as usize) {
            let pixel = source.get_pixel(x, y);
            if pixel[3] < 200 {
                continue;
            }
            let index = (usize::from(pixel[0]) >> 4) * SIDE * SIDE
                + (usize::from(pixel[1]) >> 4) * SIDE
                + (usize::from(pixel[2]) >> 4);
            counts[index] += 1;
            for (sum, channel) in sums[index].iter_mut().zip(&pixel.0[..3]) {
                *sum += u64::from(*channel);
            }
        }
    }
    let Some((index, count)) = counts
        .iter()
        .enumerate()
        .max_by_key(|(_, count)| **count)
        .filter(|(_, count)| **count > 0)
    else {
        return "#FFFFFF".to_owned();
    };
    let sum = sums[index];
    format!(
        "#{:02X}{:02X}{:02X}",
        sum[0] / u64::from(*count),
        sum[1] / u64::from(*count),
        sum[2] / u64::from(*count)
    )
}

/// Drops a transparent margin that surrounds an icon which is already a tile.
/// A circular mark keeps its margin because its opaque share of the bounding
/// box is much lower than a rounded square's.
fn trim_tile(source: &RgbaImage) -> Option<RgbaImage> {
    let (mut left, mut top, mut right, mut bottom) =
        (source.width(), source.height(), 0_u32, 0_u32);
    let mut opaque = 0_u64;
    for (x, y, pixel) in source.enumerate_pixels() {
        if pixel[3] < 8 {
            continue;
        }
        left = left.min(x);
        top = top.min(y);
        right = right.max(x);
        bottom = bottom.max(y);
        opaque += 1;
    }
    if right == 0 || bottom == 0 {
        return None;
    }
    let width = right - left + 1;
    let height = bottom - top + 1;
    if width == source.width() && height == source.height() {
        return None;
    }
    if u64::from(width) * 10 < u64::from(source.width()) * 7
        || u64::from(height) * 10 < u64::from(source.height()) * 7
    {
        return None;
    }
    if opaque * 10 < u64::from(width) * u64::from(height) * 9 {
        return None;
    }
    Some(image::imageops::crop_imm(source, left, top, width, height).to_image())
}

fn letter_icon(helper: &Path, name: &str, url: &str) -> Result<RgbaImage, AppError> {
    let host = reqwest::Url::parse(url)
        .ok()
        .and_then(|parsed| parsed.host_str().map(str::to_owned))
        .unwrap_or_default();
    let path = std::env::temp_dir().join(format!(
        "opennavo-webapp-icon-{}.png",
        uuid::Uuid::new_v4().simple()
    ));
    let status = Command::new(helper)
        .arg("--make-icon")
        .arg(letter_for(name))
        .arg(color_for(&host))
        .arg(&path)
        .status()
        .map_err(io_error)?;
    if !status.success() {
        let _ = std::fs::remove_file(&path);
        return Err(AppError::new("E_WEBAPP_ICON", "webapp_icon"));
    }
    let image = image::open(&path).map_err(|error| {
        AppError::new("E_WEBAPP_ICON", "webapp_icon").with_detail(error.to_string())
    })?;
    let _ = std::fs::remove_file(&path);
    Ok(image.to_rgba8())
}

fn letter_for(name: &str) -> String {
    name.chars()
        .next()
        .map(|character| character.to_uppercase().collect())
        .unwrap_or_else(|| "?".to_owned())
}

fn color_for(host: &str) -> &'static str {
    const COLORS: [&str; 10] = [
        "#4F7CFF", "#E05D5D", "#2FAE8F", "#C46A2D", "#8E5BD9", "#D4A017", "#3A9BDC", "#C24B8B",
        "#5A8F3D", "#6B7280",
    ];
    let mut hash: u32 = 2_166_136_261;
    for byte in host.bytes() {
        hash ^= u32::from(byte);
        hash = hash.wrapping_mul(16_777_619);
    }
    COLORS[(hash as usize) % COLORS.len()]
}

fn app_icon(source: Option<&RgbaImage>, resources: &Path) -> Result<Option<String>, AppError> {
    let Some(source) = source else {
        return Ok(None);
    };
    std::fs::write(resources.join("AppIcon.icns"), encode_icns(source)?).map_err(io_error)?;
    let preview = image::imageops::resize(source, 128, 128, FilterType::Lanczos3);
    let mut bytes = Vec::new();
    image::DynamicImage::ImageRgba8(preview)
        .write_to(&mut Cursor::new(&mut bytes), ImageFormat::Png)
        .map_err(|error| {
            AppError::new("E_WEBAPP_ICON", "webapp_icon").with_detail(error.to_string())
        })?;
    std::fs::write(resources.join("AppIcon.png"), &bytes).map_err(io_error)?;
    Ok(Some(format!(
        "data:image/png;base64,{}",
        STANDARD.encode(bytes)
    )))
}

fn encode_icns(source: &RgbaImage) -> Result<Vec<u8>, AppError> {
    // One bitmap covers both the 1x and 2x slot of the same pixel size. Walking
    // down by halves avoids resampling the 1024px source once per slot. macOS
    // reads 16/32px through the legacy ARGB slots (ic04/ic05); the PNG variants
    // (icp4/icp5) decode as noise at those sizes.
    const SIZES: [(u32, &[icns::IconType]); 7] = [
        (16, &[icns::IconType::RGBA32_16x16_ARGB]),
        (
            32,
            &[
                icns::IconType::RGBA32_16x16_2x,
                icns::IconType::RGBA32_16x16_2x_ARGB,
            ],
        ),
        (64, &[icns::IconType::RGBA32_32x32_2x]),
        (128, &[icns::IconType::RGBA32_128x128]),
        (
            256,
            &[
                icns::IconType::RGBA32_128x128_2x,
                icns::IconType::RGBA32_256x256,
            ],
        ),
        (
            512,
            &[
                icns::IconType::RGBA32_256x256_2x,
                icns::IconType::RGBA32_512x512,
            ],
        ),
        (1024, &[icns::IconType::RGBA32_512x512_2x]),
    ];
    let mut family = icns::IconFamily::new();
    let mut current = if source.width() == 1024 && source.height() == 1024 {
        source.clone()
    } else {
        image::imageops::resize(source, 1024, 1024, FilterType::Lanczos3)
    };
    for (size, icon_types) in SIZES.iter().rev() {
        let size = *size;
        if current.width() != size {
            current = image::imageops::resize(&current, size, size, FilterType::Triangle);
        }
        for icon_type in icon_types.iter() {
            let image = icns::Image::from_data(
                icns::PixelFormat::RGBA,
                size,
                size,
                current.as_raw().clone(),
            )
            .map_err(io_error)?;
            family
                .add_icon_with_type(&image, *icon_type)
                .map_err(io_error)?;
        }
    }
    let mut bytes = Vec::new();
    family.write(&mut bytes).map_err(io_error)?;
    Ok(bytes)
}

fn write_info_plist(
    path: &Path,
    name: &str,
    bundle_id: &str,
    has_icon: bool,
) -> Result<(), AppError> {
    let mut info = plist::Dictionary::new();
    info.insert(
        "CFBundleDevelopmentRegion".into(),
        plist::Value::String("en".into()),
    );
    info.insert(
        "CFBundleDisplayName".into(),
        plist::Value::String(name.to_owned()),
    );
    info.insert(
        "CFBundleExecutable".into(),
        plist::Value::String(HELPER_NAME.into()),
    );
    info.insert(
        "CFBundleIdentifier".into(),
        plist::Value::String(bundle_id.to_owned()),
    );
    info.insert(
        "CFBundleInfoDictionaryVersion".into(),
        plist::Value::String("6.0".into()),
    );
    if has_icon {
        info.insert(
            "CFBundleIconFile".into(),
            plist::Value::String("AppIcon".into()),
        );
    }
    info.insert("CFBundleName".into(), plist::Value::String(name.to_owned()));
    info.insert(
        "CFBundlePackageType".into(),
        plist::Value::String("APPL".into()),
    );
    info.insert(
        "CFBundleShortVersionString".into(),
        plist::Value::String("1.0".into()),
    );
    info.insert("CFBundleVersion".into(), plist::Value::String("1".into()));
    info.insert(
        "LSMinimumSystemVersion".into(),
        plist::Value::String("13.0".into()),
    );
    info.insert(
        "NSHighResolutionCapable".into(),
        plist::Value::Boolean(true),
    );
    info.insert(
        "NSPrincipalClass".into(),
        plist::Value::String("NSApplication".into()),
    );
    info.insert(
        "NSCameraUsageDescription".into(),
        plist::Value::String(format!(
            "{name} uses the camera when the website asks for it."
        )),
    );
    info.insert(
        "NSMicrophoneUsageDescription".into(),
        plist::Value::String(format!(
            "{name} uses the microphone when the website asks for it."
        )),
    );
    let mut transport = plist::Dictionary::new();
    transport.insert("NSAllowsArbitraryLoads".into(), plist::Value::Boolean(true));
    info.insert(
        "NSAppTransportSecurity".into(),
        plist::Value::Dictionary(transport),
    );
    plist::Value::Dictionary(info)
        .to_file_xml(path)
        .map_err(|error| AppError::new("E_WEBAPP_IO", "webapp_io").with_detail(error.to_string()))
}

fn sign_bundle(path: &Path) -> Result<(), AppError> {
    let helper = path.join("Contents/MacOS").join(HELPER_NAME);
    code_sign(&helper)?;
    code_sign(path)
}

fn code_sign(path: &Path) -> Result<(), AppError> {
    let output = Command::new("/usr/bin/codesign")
        .args(["--force", "--sign", "-"])
        .arg(path)
        .output()
        .map_err(io_error)?;
    if output.status.success() {
        return Ok(());
    }
    Err(AppError::new("E_WEBAPP_SIGN_FAILED", "webapp_sign_failed")
        .with_detail(String::from_utf8_lossy(&output.stderr).trim().to_owned()))
}

fn run_open(arguments: &[&OsStr], code: &str) -> Result<(), AppError> {
    let output = Command::new("/usr/bin/open")
        .args(arguments)
        .output()
        .map_err(io_error)?;
    if output.status.success() {
        return Ok(());
    }
    Err(AppError::new(code, "webapp_open_failed")
        .with_detail(String::from_utf8_lossy(&output.stderr).trim().to_owned()))
}

fn list_at(directory: &Path) -> Result<Vec<WebApp>, AppError> {
    if !directory.is_dir() {
        return Ok(Vec::new());
    }
    let mut apps = Vec::new();
    for entry in std::fs::read_dir(directory).map_err(io_error)? {
        let entry = entry.map_err(io_error)?;
        if !entry.file_type().map_err(io_error)?.is_dir() {
            continue;
        }
        let path = entry.path();
        if path.extension().and_then(OsStr::to_str) != Some("app") {
            continue;
        }
        let config_path = path.join("Contents/Resources").join(CONFIG_NAME);
        let Ok(bytes) = std::fs::read(&config_path) else {
            continue;
        };
        let Ok(config) = serde_json::from_slice::<RuntimeConfig>(&bytes) else {
            continue;
        };
        if config.marker != MARKER {
            continue;
        }
        let icon = std::fs::read(path.join("Contents/Resources/AppIcon.png"))
            .ok()
            .filter(|bytes| bytes.len() <= 1_000_000)
            .map(|bytes| format!("data:image/png;base64,{}", STANDARD.encode(bytes)));
        apps.push(WebApp {
            id: config.id,
            name: config.name,
            url: config.url,
            path: path.to_string_lossy().into_owned(),
            bundle_id: config.bundle_id,
            created_at: config.created_at,
            icon,
        });
    }
    apps.sort_by(|left, right| right.created_at.cmp(&left.created_at));
    Ok(apps)
}

fn unique_destination(directory: &Path, filename: &OsStr) -> PathBuf {
    let mut candidate = directory.join(filename);
    if !candidate.exists() {
        return candidate;
    }
    let stem = Path::new(filename)
        .file_stem()
        .and_then(OsStr::to_str)
        .unwrap_or("Web App");
    let extension = Path::new(filename).extension().and_then(OsStr::to_str);
    for index in 2..=999 {
        let name = match extension {
            Some(extension) => format!("{stem} {index}.{extension}"),
            None => format!("{stem} {index}"),
        };
        candidate = directory.join(name);
        if !candidate.exists() {
            return candidate;
        }
    }
    candidate
}

fn copy_dir_all(source: &Path, destination: &Path) -> std::io::Result<()> {
    std::fs::create_dir_all(destination)?;
    for entry in std::fs::read_dir(source)? {
        let entry = entry?;
        let file_type = entry.file_type()?;
        let target = destination.join(entry.file_name());
        if file_type.is_dir() {
            copy_dir_all(&entry.path(), &target)?;
        } else {
            std::fs::copy(entry.path(), target)?;
        }
    }
    Ok(())
}

fn make_executable(path: &Path) -> Result<(), AppError> {
    use std::os::unix::fs::PermissionsExt;
    let mut permissions = std::fs::metadata(path).map_err(io_error)?.permissions();
    permissions.set_mode(0o755);
    std::fs::set_permissions(path, permissions).map_err(io_error)
}

fn io_error(error: std::io::Error) -> AppError {
    AppError::new("E_WEBAPP_IO", "webapp_io").with_detail(error.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_names_that_cannot_be_a_bundle_name() {
        assert!(sanitize_name("Docs").is_ok());
        assert_eq!(sanitize_name("  Docs.app ").unwrap(), "Docs");
        assert!(sanitize_name("").is_err());
        assert!(sanitize_name("a/b").is_err());
        assert!(sanitize_name("a:b").is_err());
        assert!(sanitize_name(".hidden").is_err());
        assert!(sanitize_name(&"a".repeat(65)).is_err());
    }

    #[test]
    fn accepts_only_http_and_https_urls() {
        assert_eq!(
            validate_url("https://example.com/a?b=1").unwrap().as_str(),
            "https://example.com/a?b=1"
        );
        assert!(validate_url("http://example.com").is_ok());
        assert!(validate_url("file:///tmp/a").is_err());
        assert!(validate_url("javascript:alert(1)").is_err());
        assert!(validate_url("https://user:pass@example.com").is_err());
        assert!(validate_url("not a url").is_err());
    }

    #[test]
    fn build_writes_a_thin_bundle_and_lists_it() {
        let root = tempfile::tempdir().expect("temp");
        let apps = root.path().join("Applications");
        let helper = root.path().join("helper");
        std::fs::write(&helper, b"fake helper").expect("helper");
        let icon = image::RgbaImage::from_pixel(128, 128, image::Rgba([0x4F, 0x7C, 0xFF, 0xFF]));
        let app = build_bundle(
            &apps,
            &helper,
            "Docs",
            "https://example.com/docs",
            Some(&icon),
            Utc::now(),
        )
        .expect("build");
        let bundle = Path::new(&app.path);
        assert!(bundle.join("Contents/MacOS").join(HELPER_NAME).is_file());
        assert!(
            bundle
                .join("Contents/Resources")
                .join(CONFIG_NAME)
                .is_file()
        );
        assert!(bundle.join("Contents/Resources/AppIcon.icns").is_file());
        assert!(bundle.join("Contents/Resources/AppIcon.png").is_file());
        assert!(
            app.icon
                .as_deref()
                .unwrap_or_default()
                .starts_with("data:image/png;base64,")
        );
        let info = plist::Value::from_file(bundle.join("Contents/Info.plist")).expect("plist");
        assert_eq!(
            info.as_dictionary()
                .and_then(|dict| dict.get("CFBundleIdentifier"))
                .and_then(plist::Value::as_string),
            Some(app.bundle_id.as_str())
        );
        let listed = list_at(&apps).expect("list");
        assert_eq!(listed, vec![app]);
    }

    #[test]
    fn failed_build_leaves_no_bundle_or_staging_directory() {
        let root = tempfile::tempdir().expect("temp");
        let apps = root.path().join("Applications");
        let missing_helper = root.path().join("missing-helper");
        let result = build_bundle(
            &apps,
            &missing_helper,
            "Docs",
            "https://example.com/docs",
            None,
            Utc::now(),
        );
        assert!(result.is_err());
        assert!(!apps.join("Docs.app").exists());
        assert_eq!(std::fs::read_dir(&apps).expect("apps").count(), 0);

        let helper = root.path().join("helper");
        std::fs::write(&helper, b"fake helper").expect("helper");
        let app = build_bundle(
            &apps,
            &helper,
            "Docs",
            "https://example.com/docs",
            None,
            Utc::now(),
        )
        .expect("retry");
        assert!(Path::new(&app.path).is_dir());
    }

    #[test]
    fn letter_icon_uses_the_staged_helper() {
        let helper =
            Path::new(env!("CARGO_MANIFEST_DIR")).join("../native/staged/opennavo-webapp-helper");
        if !helper.is_file() {
            return;
        }
        let icon = letter_icon(&helper, "Docs", "https://docs.example.com").expect("icon");
        assert_eq!(icon.dimensions(), (1024, 1024));
    }

    #[test]
    fn reads_icon_sizes_in_every_common_form() {
        assert_eq!(icon_size("32x32"), 32);
        assert_eq!(icon_size("16x16 32x32 180x180"), 180);
        assert_eq!(icon_size("180×180"), 180);
        assert_eq!(icon_size("any"), 0);
        assert_eq!(icon_size(""), 0);
    }

    #[test]
    fn reads_declared_icons_and_the_manifest_link() {
        let url = reqwest::Url::parse("https://example.com/docs/page").expect("url");
        let page = r#"<html><head>
            <base href="/static/">
            <link rel="shortcut icon" href="/favicon-32.png" sizes="32x32">
            <link rel="apple-touch-icon" sizes="180x180" href="touch.png">
            <link rel="manifest" href="/app.webmanifest">
        </head></html>"#;
        let base = document_base(page, &url);
        assert_eq!(base.as_str(), "https://example.com/static/");
        let (mut icons, manifest) = link_candidates(page, &base);
        assert_eq!(
            manifest.expect("manifest").as_str(),
            "https://example.com/app.webmanifest"
        );
        icons.sort_by_key(|icon| std::cmp::Reverse(icon.size));
        assert_eq!(icons[0].size, 180);
        assert_eq!(icons[0].priority, 90);
        assert_eq!(
            icons[0].url.as_str(),
            "https://example.com/static/touch.png"
        );
        assert_eq!(icons[1].size, 32);
        assert_eq!(icons[1].priority, 50);
        assert_eq!(icons[1].url.as_str(), "https://example.com/favicon-32.png");
    }

    #[test]
    fn reads_manifest_icons_and_skips_vector_sources() {
        let base = reqwest::Url::parse("https://example.com/app.webmanifest").expect("url");
        let manifest = r#"{"icons":[
            {"src":"/icon-192.png","sizes":"192x192"},
            {"src":"/icon-512.png","sizes":"512x512"},
            {"src":"/maskable.png","sizes":"512x512","purpose":"maskable"},
            {"src":"/icon.svg","sizes":"any"}
        ]}"#;
        let icons = manifest_candidates(manifest, &base);
        assert_eq!(icons.len(), 3);
        assert!(
            icons
                .iter()
                .all(|icon| icon.priority == 100 && icon.url.scheme() == "https")
        );
        assert!(icons.iter().any(|icon| {
            icon.url.as_str() == "https://example.com/icon-512.png" && icon.size == 512
        }));
    }

    #[test]
    fn icns_uses_the_slots_macos_reads() {
        let source = RgbaImage::from_pixel(1024, 1024, image::Rgba([0x4F, 0x7C, 0xFF, 0xFF]));
        let family = icns::IconFamily::read(Cursor::new(encode_icns(&source).expect("icns")))
            .expect("family");
        let icons = family.available_icons();
        for expected in [
            icns::IconType::RGBA32_16x16_ARGB,
            icns::IconType::RGBA32_16x16_2x_ARGB,
            icns::IconType::RGBA32_16x16_2x,
            icns::IconType::RGBA32_32x32_2x,
            icns::IconType::RGBA32_128x128,
            icns::IconType::RGBA32_128x128_2x,
            icns::IconType::RGBA32_256x256,
            icns::IconType::RGBA32_256x256_2x,
            icns::IconType::RGBA32_512x512,
            icns::IconType::RGBA32_512x512_2x,
        ] {
            assert!(icons.contains(&expected), "missing {expected:?}");
        }
        assert!(!icons.contains(&icns::IconType::RGBA32_16x16));
        assert!(!icons.contains(&icns::IconType::RGBA32_32x32));
        let small = family
            .get_icon_with_type(icns::IconType::RGBA32_16x16_ARGB)
            .expect("small icon");
        assert_eq!(&small.data()[..4], &[0x4F, 0x7C, 0xFF, 0xFF]);
    }

    #[test]
    fn renders_a_fetched_icon_through_the_staged_helper() {
        let helper =
            Path::new(env!("CARGO_MANIFEST_DIR")).join("../native/staged/opennavo-webapp-helper");
        if !helper.is_file() {
            return;
        }
        let source = image::RgbaImage::from_pixel(512, 512, image::Rgba([0x1E, 0x6F, 0xE8, 0xFF]));
        let icon = render_site_icon(&helper, &source).expect("icon");
        assert_eq!(icon.dimensions(), (1024, 1024));
        assert_eq!(icon.get_pixel(0, 0)[3], 0);
        assert_eq!(icon.get_pixel(512, 512)[3], 255);
    }

    #[test]
    fn refuses_a_duplicate_name() {
        let root = tempfile::tempdir().expect("temp");
        let apps = root.path().join("Applications");
        let helper = root.path().join("helper");
        std::fs::write(&helper, b"fake helper").expect("helper");
        build_bundle(
            &apps,
            &helper,
            "Docs",
            "https://example.com",
            None,
            Utc::now(),
        )
        .expect("first");
        let error = build_bundle(
            &apps,
            &helper,
            "Docs",
            "https://example.org",
            None,
            Utc::now(),
        )
        .expect_err("duplicate");
        assert_eq!(error.code, "E_WEBAPP_EXISTS");
    }
}
