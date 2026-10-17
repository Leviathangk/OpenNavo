// SPDX-License-Identifier: GPL-3.0-only
//! Bounded metadata checks for container coverage which clamd can silently omit.
use crate::{AppError, security::files};
use std::{
    fs::{self, File},
    io::{Cursor, Seek, SeekFrom},
    os::unix::fs::FileExt,
    path::Path,
};
#[derive(Default)]
pub struct Coverage {
    pub dmg: bool,
    pub expected_dmg_bytes: u64,
    /// Metadata-derived top-level expanded size; unknown formats remain None.
    pub expanded_bytes: Option<u64>,
    pub incomplete: Option<&'static str>,
}
fn unreadable() -> AppError {
    AppError::new("E_AV_SCAN_FAILED", "av_scan_unreadable")
}
fn be32(b: &[u8], at: usize) -> Result<u32, AppError> {
    Ok(u32::from_be_bytes(
        b.get(at..at + 4)
            .ok_or_else(unreadable)?
            .try_into()
            .unwrap(),
    ))
}
fn be64(b: &[u8], at: usize) -> Result<u64, AppError> {
    Ok(u64::from_be_bytes(
        b.get(at..at + 8)
            .ok_or_else(unreadable)?
            .try_into()
            .unwrap(),
    ))
}
pub fn inspect(file: &File) -> Result<Coverage, AppError> {
    let size = file.metadata()?.len();
    let mut trailer = [0; 512];
    if size >= 512 && file.read_at(&mut trailer, size - 512)? == 512 && &trailer[..4] == b"koly" {
        return dmg(file, size, &trailer);
    }
    let mut magic = [0; 4];
    file.read_at(&mut magic, 0)?;
    if &magic[..2] == b"PK" {
        return zip(file, size);
    }
    Ok(Coverage::default())
}
fn dmg(file: &File, size: u64, trailer: &[u8]) -> Result<Coverage, AppError> {
    let mut coverage = Coverage {
        dmg: true,
        ..Default::default()
    };
    let offset = be64(trailer, 216)?;
    let length = be64(trailer, 224)?;
    if length == 0
        || length > 8 * 1024 * 1024
        || offset
            .checked_add(length)
            .is_none_or(|end| end > size - 512)
    {
        return Err(unreadable());
    }
    let mut xml = vec![0; length as usize];
    file.read_exact_at(&mut xml, offset)?;
    let value = plist::Value::from_reader(Cursor::new(xml)).map_err(|_| unreadable())?;
    let blocks = value
        .as_dictionary()
        .and_then(|v| v.get("resource-fork"))
        .and_then(plist::Value::as_dictionary)
        .and_then(|v| v.get("blkx"))
        .and_then(plist::Value::as_array)
        .ok_or_else(unreadable)?;
    if blocks.is_empty() || blocks.len() > 50000 {
        return Err(unreadable());
    }
    for block in blocks {
        let block = block.as_dictionary().ok_or_else(unreadable)?;
        if block
            .get("Name")
            .and_then(plist::Value::as_string)
            .is_some_and(|v| v.contains("Apple_APFS"))
        {
            coverage.incomplete = Some("av_scan_unreadable");
        }
        let data = block
            .get("Data")
            .and_then(plist::Value::as_data)
            .ok_or_else(unreadable)?;
        if data.get(..4) != Some(b"mish") {
            return Err(unreadable());
        }
        let count = be32(data, 200)? as usize;
        if count > 50000 || data.len() < 204 + 40 * count {
            return Err(unreadable());
        }
        let mut bytes = 0u64;
        for index in 0..count {
            let at = 204 + 40 * index;
            let kind = be32(data, at)?;
            let sectors = be64(data, at + 16)?;
            if !matches!(
                kind,
                0 | 1 | 2 | 0x80000004 | 0x80000005 | 0x80000006 | 0x7ffffffe | 0xffffffff
            ) && sectors > 0
            {
                coverage.incomplete = Some("av_scan_unreadable");
            }
            if !matches!(kind, 0x7ffffffe | 0xffffffff) {
                bytes = bytes
                    .checked_add(sectors.checked_mul(512).ok_or_else(unreadable)?)
                    .ok_or_else(unreadable)?;
            }
        }
        coverage.expected_dmg_bytes = coverage
            .expected_dmg_bytes
            .checked_add(bytes)
            .ok_or_else(unreadable)?;
    }
    if coverage.expected_dmg_bytes == 0 {
        coverage.incomplete = Some("av_scan_unreadable");
    }
    coverage.expanded_bytes = Some(coverage.expected_dmg_bytes);
    Ok(coverage)
}
fn zip(file: &File, size: u64) -> Result<Coverage, AppError> {
    // Bound central-directory allocation before passing it to the ZIP reader.
    let n = size.min(65557) as usize;
    let mut tail = vec![0; n];
    file.read_exact_at(&mut tail, size - n as u64)?;
    let offset = tail
        .windows(4)
        .rposition(|v| v == b"PK\x05\x06")
        .ok_or_else(unreadable)?;
    let eocd = tail.get(offset..offset + 22).ok_or_else(unreadable)?;
    let mut central = u64::from(u32::from_le_bytes(eocd[12..16].try_into().unwrap()));
    if central == u64::from(u32::MAX) || eocd[10..12] == [0xff, 0xff] || eocd[16..20] == [0xff; 4] {
        // ZIP64's end record precedes the locator immediately before EOCD.
        // Bound the central directory before ZipArchive allocates its index.
        let position = size - n as u64 + offset as u64;
        let mut locator = [0; 20];
        file.read_exact_at(
            &mut locator,
            position.checked_sub(20).ok_or_else(unreadable)?,
        )?;
        if &locator[..4] != b"PK\x06\x07" {
            return Err(unreadable());
        }
        let position = u64::from_le_bytes(locator[8..16].try_into().unwrap());
        if position.checked_add(56).is_none_or(|end| end > size) {
            return Err(unreadable());
        }
        let mut record = [0; 56];
        file.read_exact_at(&mut record, position)?;
        if &record[..4] != b"PK\x06\x06" {
            return Err(unreadable());
        }
        central = u64::from_le_bytes(record[40..48].try_into().unwrap());
    }
    if central > 16 * 1024 * 1024 {
        return Err(unreadable());
    }
    let mut clone = file.try_clone()?;
    clone.seek(SeekFrom::Start(0))?;
    let mut archive = zip::ZipArchive::new(clone).map_err(|_| unreadable())?;
    let mut coverage = Coverage::default();
    if archive.len() > 50000 {
        coverage.incomplete = Some("av_scan_too_large");
        return Ok(coverage);
    }
    let mut expanded = 0u64;
    for index in 0..archive.len() {
        let entry = archive.by_index_raw(index).map_err(|_| unreadable())?;
        expanded = expanded.saturating_add(entry.size());
        if entry.size() > files::MAX_INSTALLER_FILE || expanded > files::MAX_SCAN_SIZE {
            coverage.incomplete = Some("av_scan_too_large");
        }
    }
    coverage.expanded_bytes = Some(expanded);
    Ok(coverage)
}
pub fn extracted_dmg_bytes(root: &Path) -> u64 {
    let mut stack = vec![root.to_owned()];
    let mut bytes = 0u64;
    while let Some(path) = stack.pop() {
        let Ok(meta) = fs::symlink_metadata(&path) else {
            continue;
        };
        if meta.is_dir() {
            if let Ok(entries) = fs::read_dir(path) {
                stack.extend(entries.flatten().map(|e| e.path()));
            }
        } else if meta.is_file()
            && path
                .file_name()
                .is_some_and(|n| n.to_string_lossy().starts_with("dmg"))
        {
            bytes = bytes.saturating_add(meta.len());
        }
    }
    bytes
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    #[test]
    fn zip64_central_sizes_are_counted_without_extracting_members() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("large.zip");
        let mut writer = zip::ZipWriter::new(Cursor::new(Vec::new()));
        for name in ["one", "two", "three"] {
            writer
                .start_file(name, zip::write::SimpleFileOptions::default())
                .unwrap();
            writer.write_all(b"x").unwrap();
        }
        let mut bytes = writer.finish().unwrap().into_inner();
        let entries: Vec<_> = bytes
            .windows(4)
            .enumerate()
            .filter_map(|(i, v)| (v == b"PK\x01\x02").then_some(i))
            .collect();
        for at in entries {
            bytes[at + 24..at + 28].copy_from_slice(&(800u32 * 1024 * 1024).to_le_bytes());
        }
        let end = bytes.len() - 22;
        let mut eocd = bytes.split_off(end);
        let central = u32::from_le_bytes(eocd[12..16].try_into().unwrap());
        let offset = u32::from_le_bytes(eocd[16..20].try_into().unwrap());
        let mut record = [0u8; 56];
        record[..4].copy_from_slice(b"PK\x06\x06");
        record[4..12].copy_from_slice(&44u64.to_le_bytes());
        record[12..14].copy_from_slice(&45u16.to_le_bytes());
        record[14..16].copy_from_slice(&45u16.to_le_bytes());
        record[24..32].copy_from_slice(&3u64.to_le_bytes());
        record[32..40].copy_from_slice(&3u64.to_le_bytes());
        record[40..48].copy_from_slice(&u64::from(central).to_le_bytes());
        record[48..56].copy_from_slice(&u64::from(offset).to_le_bytes());
        bytes.extend_from_slice(&record);
        let mut locator = [0u8; 20];
        locator[..4].copy_from_slice(b"PK\x06\x07");
        locator[8..16].copy_from_slice(&(end as u64).to_le_bytes());
        locator[16..20].copy_from_slice(&1u32.to_le_bytes());
        bytes.extend_from_slice(&locator);
        eocd[8..20].fill(0xff);
        bytes.extend_from_slice(&eocd);
        fs::write(&path, bytes).unwrap();
        let c = inspect(&File::open(path).unwrap()).unwrap();
        assert_eq!(c.expanded_bytes, Some(2400 * 1024 * 1024));
        assert_eq!(c.incomplete, Some("av_scan_too_large"));
    }
    #[test]
    fn unknown_dmg_stripes_and_zero_extraction_are_incomplete() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("fixture.dmg");
        let mut block = vec![0; 244];
        block[..4].copy_from_slice(b"mish");
        block[200..204].copy_from_slice(&1u32.to_be_bytes());
        block[204..208].copy_from_slice(&0x80000007u32.to_be_bytes());
        block[220..228].copy_from_slice(&1u64.to_be_bytes());
        let mut entry = plist::Dictionary::new();
        entry.insert("Data".into(), plist::Value::Data(block));
        let mut fork = plist::Dictionary::new();
        fork.insert(
            "blkx".into(),
            plist::Value::Array(vec![plist::Value::Dictionary(entry)]),
        );
        let mut root = plist::Dictionary::new();
        root.insert("resource-fork".into(), plist::Value::Dictionary(fork));
        let mut xml = vec![];
        plist::Value::Dictionary(root)
            .to_writer_xml(&mut xml)
            .unwrap();
        let mut trailer = [0; 512];
        trailer[..4].copy_from_slice(b"koly");
        trailer[224..232].copy_from_slice(&(xml.len() as u64).to_be_bytes());
        let mut out = File::create(&path).unwrap();
        out.write_all(&xml).unwrap();
        out.write_all(&trailer).unwrap();
        let c = inspect(&File::open(path).unwrap()).unwrap();
        assert!(c.dmg);
        assert_eq!(c.incomplete, Some("av_scan_unreadable"));
        assert_eq!(c.expected_dmg_bytes, 512);
        assert_eq!(extracted_dmg_bytes(temp.path()), 0);
    }
}
