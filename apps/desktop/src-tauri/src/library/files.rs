use crate::{AppError, db::Database};
use rusqlite::{OptionalExtension, params};
use sha1::{Digest, Sha1};
use std::{
    collections::HashSet,
    io::Read,
    path::{Path, PathBuf},
};
fn failure(message: &str) -> AppError {
    AppError::new("E_UNKNOWN", message)
}
pub fn mtime(path: &Path) -> Result<i64, AppError> {
    Ok(std::fs::metadata(path)?
        .modified()?
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| failure("mtime"))?
        .as_millis()
        .min(i64::MAX as u128) as i64)
}
pub fn plist(path: &Path) -> Result<plist::Dictionary, AppError> {
    let path = path.join("Contents/Info.plist");
    if std::fs::metadata(&path)?.len() > 4 * 1024 * 1024 {
        return Err(failure("plist_too_large"));
    }
    let value =
        plist::Value::from_file(path).map_err(|e| failure("plist").with_detail(e.to_string()))?;
    value
        .into_dictionary()
        .ok_or_else(|| failure("plist_dictionary"))
}
pub fn actual_version(path: &Path) -> Option<String> {
    let dict = plist(path).ok()?;
    ["CFBundleShortVersionString", "CFBundleVersion"]
        .iter()
        .find_map(|key| {
            dict.get(key)
                .and_then(plist::Value::as_string)
                .filter(|v| !v.trim().is_empty())
                .map(str::to_owned)
        })
}
pub fn cached_size(database: &Database, path: &Path) -> Result<Option<u64>, AppError> {
    let time = mtime(path)?;
    database.with(|c| {
        let result: Option<i64> = c
            .query_row(
                "SELECT bytes FROM size_cache WHERE path=?1 AND mtime=?2",
                params![path.to_string_lossy(), time],
                |r| r.get(0),
            )
            .optional()?;
        Ok(result.and_then(|v| v.try_into().ok()))
    })
}
pub fn directory_size(path: &Path) -> Result<u64, AppError> {
    let mut stack = vec![path.to_owned()];
    let mut bytes = 0u64;
    let mut count = 0u32;
    let mut directories = HashSet::new();
    while let Some(path) = stack.pop() {
        let meta = std::fs::symlink_metadata(&path)?;
        count += 1;
        if count > 2_000_000 {
            return Err(failure("size_file_limit"));
        }
        if meta.is_symlink() {
            continue;
        }
        if meta.is_file() {
            bytes = bytes.saturating_add(meta.len());
        } else if meta.is_dir() {
            if !directories.insert(path.clone()) {
                continue;
            }
            for entry in std::fs::read_dir(path)? {
                stack.push(entry?.path());
            }
        }
    }
    Ok(bytes)
}
pub fn size(database: &Database, path: &Path) -> Result<u64, AppError> {
    if let Some(size) = cached_size(database, path)? {
        return Ok(size);
    }
    let before = mtime(path)?;
    let bytes = directory_size(path)?;
    if mtime(path)? == before {
        let integer = i64::try_from(bytes).map_err(|_| failure("size_range"))?;
        database.with(|c|{c.execute("INSERT INTO size_cache(path,mtime,bytes) VALUES(?1,?2,?3) ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime,bytes=excluded.bytes",params![path.to_string_lossy(),before,integer])?;Ok(())})?;
    }
    Ok(bytes)
}
pub fn cached_icon(database: &Database, path: &Path) -> Result<Option<String>, AppError> {
    let time = icon_mtime(path)?;
    database.with(|c| {
        let cached: Option<String> = c
            .query_row(
                "SELECT png_path FROM icon_cache WHERE app_path=?1 AND mtime=?2",
                params![path.to_string_lossy(), time],
                |r| r.get(0),
            )
            .optional()?;
        Ok(cached.filter(|p| p.ends_with("-normalized-v1.png") && Path::new(p).is_file()))
    })
}
fn icon_mtime(path: &Path) -> Result<i64, AppError> {
    Ok(mtime(path)?.max(mtime(&path.join("Contents/Info.plist")).unwrap_or(0)))
}
fn icns(path: &Path) -> Result<image::DynamicImage, AppError> {
    let dict = plist(path)?;
    let raw = dict
        .get("CFBundleIconFile")
        .and_then(plist::Value::as_string)
        .ok_or_else(|| failure("icon_missing"))?;
    if raw.contains(['/', '\\']) || raw == "." || raw == ".." {
        return Err(failure("icon_name"));
    }
    let name = if raw.ends_with(".icns") {
        raw.into()
    } else {
        format!("{raw}.icns")
    };
    let path = path.join("Contents/Resources").join(name);
    let bytes = std::fs::File::open(path)?.take(32 * 1024 * 1024);
    let family = icns::IconFamily::read(bytes)?;
    let mut types = family.available_icons();
    types.retain(|kind| kind.pixel_width() <= 512 && kind.pixel_height() <= 512);
    types.sort_by_key(|kind| std::cmp::Reverse(kind.pixel_width()));
    for kind in types {
        if let Ok(icon) = family.get_icon_with_type(kind) {
            let rgba = icon.convert_to(icns::PixelFormat::RGBA);
            if let Some(buffer) =
                image::RgbaImage::from_raw(rgba.width(), rgba.height(), rgba.data().to_vec())
            {
                return Ok(image::DynamicImage::ImageRgba8(buffer));
            }
        }
    }
    Err(failure("icon_decode"))
}
#[cfg(target_os = "macos")]
fn workspace_icon(path: &Path) -> Result<image::DynamicImage, AppError> {
    objc2::rc::autoreleasepool(|_| {
        let workspace = objc2_app_kit::NSWorkspace::sharedWorkspace();
        let image = workspace.iconForFile(&objc2_foundation::NSString::from_str(
            &path.to_string_lossy(),
        ));
        image.setSize(objc2_foundation::NSSize::new(512.0, 512.0));
        use objc2::AnyThread;
        use objc2_app_kit::{
            NSBitmapFormat, NSBitmapImageRep, NSCompositingOperation, NSDeviceRGBColorSpace,
            NSGraphicsContext,
        };
        let rect = objc2_foundation::NSRect::new(
            objc2_foundation::NSPoint::new(0.0, 0.0),
            objc2_foundation::NSSize::new(512.0, 512.0),
        );
        // Empty planes lets AppKit allocate; use fixed 8-bit RGBA to avoid system HDR icons' 16-bit float TIFFs.
        let bitmap=unsafe{NSBitmapImageRep::initWithBitmapDataPlanes_pixelsWide_pixelsHigh_bitsPerSample_samplesPerPixel_hasAlpha_isPlanar_colorSpaceName_bitmapFormat_bytesPerRow_bitsPerPixel(NSBitmapImageRep::alloc(),std::ptr::null_mut(),512,512,8,4,true,false,NSDeviceRGBColorSpace,NSBitmapFormat::empty(),2048,32)}.ok_or_else(||failure("workspace_bitmap"))?;
        let context = NSGraphicsContext::graphicsContextWithBitmapImageRep(&bitmap)
            .ok_or_else(|| failure("workspace_context"))?;
        NSGraphicsContext::saveGraphicsState_class();
        struct Restore;
        impl Drop for Restore {
            fn drop(&mut self) {
                NSGraphicsContext::restoreGraphicsState_class();
            }
        }
        let _restore = Restore;
        NSGraphicsContext::setCurrentContext(Some(&context));
        image.drawInRect_fromRect_operation_fraction(rect, rect, NSCompositingOperation::Copy, 1.0);
        let data = bitmap
            .TIFFRepresentation()
            .ok_or_else(|| failure("workspace_icon"))?;
        if data.length() > 32 * 1024 * 1024 {
            return Err(failure("workspace_icon_size"));
        }
        image::load_from_memory(&data.to_vec())
            .map_err(|e| failure("workspace_icon_decode").with_detail(e.to_string()))
    })
}
#[cfg(not(target_os = "macos"))]
fn workspace_icon(_path: &Path) -> Result<image::DynamicImage, AppError> {
    Err(failure("workspace_icon_unsupported"))
}
pub fn icon(database: &Database, path: &Path, cache: &Path) -> Result<String, AppError> {
    if let Some(cached) = cached_icon(database, path)? {
        return Ok(cached);
    }
    let time = icon_mtime(path)?;
    let decoded = icns(path).or_else(|_| workspace_icon(path))?;
    let decoded = if decoded.width() > 512 || decoded.height() > 512 {
        decoded.thumbnail(512, 512)
    } else {
        decoded
    };
    let decoded = normalized_icon(decoded)?;
    std::fs::create_dir_all(cache)?;
    let hash = format!("{:x}", Sha1::digest(path.to_string_lossy().as_bytes()));
    let destination = cache.join(format!("{hash}-{time}-normalized-v1.png"));
    let temporary = cache.join(format!("icon_{}.tmp", uuid::Uuid::now_v7()));
    let result = (|| {
        let mut file = std::fs::File::create(&temporary)?;
        decoded
            .write_to(&mut file, image::ImageFormat::Png)
            .map_err(|e| failure("icon_png").with_detail(e.to_string()))?;
        std::fs::rename(&temporary, &destination)?;
        Ok::<_, AppError>(())
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(temporary);
    }
    result?;
    let png = destination.to_string_lossy().into_owned();
    database.with(|c|{c.execute("INSERT INTO icon_cache(app_path,mtime,png_path) VALUES(?1,?2,?3) ON CONFLICT(app_path) DO UPDATE SET mtime=excluded.mtime,png_path=excluded.png_path",params![path.to_string_lossy(),time,png])?;Ok(())})?;
    Ok(png)
}

// Normalize visible artwork rather than the source canvas. Keep native contours
// and aspect ratio; a small optical correction balances circles and solid tiles.
fn normalized_icon(decoded: image::DynamicImage) -> Result<image::DynamicImage, AppError> {
    let rgba = decoded.to_rgba8();
    let (width, height) = rgba.dimensions();
    let bounds = |threshold: u8| {
        let mut bounds = (width, height, 0, 0);
        let mut count = 0u32;
        for (x, y, pixel) in rgba.enumerate_pixels() {
            if pixel[3] >= threshold {
                bounds.0 = bounds.0.min(x);
                bounds.1 = bounds.1.min(y);
                bounds.2 = bounds.2.max(x + 1);
                bounds.3 = bounds.3.max(y + 1);
                count += 1;
            }
        }
        (bounds, count)
    };
    // Ignore faint shadows for sizing, but preserve a narrow edge around artwork.
    let (mut rect, mut count) = bounds(32);
    if count == 0 {
        (rect, count) = bounds(1);
    }
    if count == 0 {
        return Err(failure("icon_empty"));
    }
    let coverage = count as f64 / ((rect.2 - rect.0) * (rect.3 - rect.1)) as f64;
    let compensation = (0.9 / coverage).sqrt().clamp(1.0, 1.08);
    let target = (240.0 * compensation).round().min(256.0) as u32;
    let bleed = (width.max(height) / 256).max(1);
    rect.0 = rect.0.saturating_sub(bleed);
    rect.1 = rect.1.saturating_sub(bleed);
    rect.2 = (rect.2 + bleed).min(width);
    rect.3 = (rect.3 + bleed).min(height);
    let cropped =
        image::imageops::crop_imm(&rgba, rect.0, rect.1, rect.2 - rect.0, rect.3 - rect.1)
            .to_image();
    let scale = target as f64 / cropped.width().max(cropped.height()) as f64;
    let resized = image::imageops::resize(
        &cropped,
        (cropped.width() as f64 * scale).round().max(1.0) as u32,
        (cropped.height() as f64 * scale).round().max(1.0) as u32,
        image::imageops::FilterType::Lanczos3,
    );
    let mut normalized = image::RgbaImage::new(256, 256);
    image::imageops::overlay(
        &mut normalized,
        &resized,
        ((256 - resized.width()) / 2) as i64,
        ((256 - resized.height()) / 2) as i64,
    );
    Ok(image::DynamicImage::ImageRgba8(normalized))
}
pub fn formula_path(prefix: &str, token: &str, version: &str) -> Option<PathBuf> {
    if crate::brew::args::validate_token(token).is_err()
        || version.contains(['/', '\\'])
        || version.is_empty()
        || version == "."
        || version == ".."
    {
        return None;
    }
    Some(Path::new(prefix).join("Cellar").join(token).join(version))
}

#[cfg(test)]
mod icon_tests {
    use super::*;

    fn visible_bounds(image: &image::RgbaImage) -> (u32, u32, u32, u32) {
        let mut bounds = (image.width(), image.height(), 0, 0);
        for (x, y, pixel) in image.enumerate_pixels() {
            if pixel[3] >= 128 {
                bounds.0 = bounds.0.min(x);
                bounds.1 = bounds.1.min(y);
                bounds.2 = bounds.2.max(x + 1);
                bounds.3 = bounds.3.max(y + 1);
            }
        }
        bounds
    }

    #[test]
    fn different_padding_and_off_center_artwork_have_balanced_visible_sizes() {
        let mut sizes = Vec::new();
        for (x, y, size) in [(100, 32, 160), (30, 80, 384), (0, 0, 512)] {
            let mut source = image::RgbaImage::new(512, 512);
            for row in y..y + size {
                for column in x..x + size {
                    source.put_pixel(column, row, image::Rgba([40, 120, 220, 255]));
                }
            }
            let output = normalized_icon(image::DynamicImage::ImageRgba8(source))
                .unwrap()
                .to_rgba8();
            assert_eq!(output.dimensions(), (256, 256));
            let bounds = visible_bounds(&output);
            assert!((bounds.0 + bounds.2).abs_diff(256) <= 2);
            assert!((bounds.1 + bounds.3).abs_diff(256) <= 2);
            sizes.push(bounds.2 - bounds.0);
        }
        assert!(sizes.iter().max().unwrap() - sizes.iter().min().unwrap() <= 6);
    }

    #[test]
    fn circle_is_optically_compensated_and_tall_artwork_keeps_its_aspect_ratio() {
        let mut circle = image::RgbaImage::new(256, 256);
        for y in 0..256 {
            for x in 0..256 {
                if (x as f64 - 128.0).powi(2) + (y as f64 - 128.0).powi(2) < 80.0f64.powi(2) {
                    circle.put_pixel(x, y, image::Rgba([20, 220, 80, 255]));
                }
            }
        }
        let output = normalized_icon(image::DynamicImage::ImageRgba8(circle))
            .unwrap()
            .to_rgba8();
        let bounds = visible_bounds(&output);
        assert!(bounds.2 - bounds.0 > 240);
        assert_eq!(bounds.2 - bounds.0, bounds.3 - bounds.1);
        let mut tall = image::RgbaImage::new(256, 256);
        for y in 20..220 {
            for x in 70..170 {
                tall.put_pixel(x, y, image::Rgba([240, 240, 240, 255]));
            }
        }
        let output = normalized_icon(image::DynamicImage::ImageRgba8(tall))
            .unwrap()
            .to_rgba8();
        let bounds = visible_bounds(&output);
        assert!(((bounds.2 - bounds.0) * 2).abs_diff(bounds.3 - bounds.1) <= 3);
    }

    #[test]
    fn empty_icons_fall_back_and_translucent_artwork_is_preserved() {
        assert!(normalized_icon(image::DynamicImage::new_rgba8(256, 256)).is_err());
        let translucent = image::RgbaImage::from_pixel(64, 64, image::Rgba([80, 90, 100, 20]));
        let output = normalized_icon(image::DynamicImage::ImageRgba8(translucent))
            .unwrap()
            .to_rgba8();
        assert!(output.pixels().any(|pixel| pixel[3] > 0));
    }
}
