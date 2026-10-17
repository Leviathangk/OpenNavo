//! Bounded read-only Mach-O metadata. No dependency on Xcode command-line tools.
use crate::AppError;
use std::{
    collections::BTreeMap,
    fs::File,
    io::{Read, Seek, SeekFrom},
    path::Path,
};
fn invalid() -> AppError {
    AppError::new("E_UPDATE_ARCH", "invalid_macho")
}
fn u32_at(bytes: &[u8], offset: usize, little: bool) -> Result<u32, AppError> {
    let v: [u8; 4] = bytes
        .get(offset..offset + 4)
        .ok_or_else(invalid)?
        .try_into()
        .map_err(|_| invalid())?;
    Ok(if little {
        u32::from_le_bytes(v)
    } else {
        u32::from_be_bytes(v)
    })
}
fn u64_at(bytes: &[u8], offset: usize, little: bool) -> Result<u64, AppError> {
    let v: [u8; 8] = bytes
        .get(offset..offset + 8)
        .ok_or_else(invalid)?
        .try_into()
        .map_err(|_| invalid())?;
    Ok(if little {
        u64::from_le_bytes(v)
    } else {
        u64::from_be_bytes(v)
    })
}
fn read(file: &mut File, offset: u64, size: usize) -> Result<Vec<u8>, AppError> {
    file.seek(SeekFrom::Start(offset))?;
    let mut bytes = vec![0; size];
    file.read_exact(&mut bytes).map_err(|_| invalid())?;
    Ok(bytes)
}
fn thin(file: &mut File, offset: u64, length: u64) -> Result<(String, String), AppError> {
    if length < 32 {
        return Err(invalid());
    };
    let header = read(file, offset, 32)?;
    let little = match &header[..4] {
        [0xcf, 0xfa, 0xed, 0xfe] => true,
        [0xfe, 0xed, 0xfa, 0xcf] => false,
        _ => return Err(invalid()),
    };
    let arch = match u32_at(&header, 4, little)? {
        0x01000007 => "x86_64",
        0x0100000c => "arm64",
        _ => return Err(invalid()),
    };
    let count = u32_at(&header, 16, little)?;
    let size = u32_at(&header, 20, little)? as usize;
    if count > 8192 || size > 4 * 1024 * 1024 || size as u64 > length - 32 {
        return Err(invalid());
    }
    let commands = read(file, offset + 32, size)?;
    let mut position = 0usize;
    let mut minimum = 0;
    for _ in 0..count {
        let command = u32_at(&commands, position, little)?;
        let size = u32_at(&commands, position + 4, little)? as usize;
        if size < 8
            || position
                .checked_add(size)
                .is_none_or(|end| end > commands.len())
        {
            return Err(invalid());
        }
        let packed = match command {
            0x32 => {
                if size < 24 || u32_at(&commands, position + 8, little)? != 1 {
                    return Err(invalid());
                };
                Some(u32_at(&commands, position + 12, little)?)
            }
            0x24 => {
                if size < 16 {
                    return Err(invalid());
                };
                Some(u32_at(&commands, position + 8, little)?)
            }
            _ => None,
        };
        if let Some(value) = packed {
            minimum = minimum.max(value)
        };
        position += size;
    }
    if position != commands.len() || minimum == 0 {
        return Err(AppError::new("E_UPDATE_MIN_OS", "missing_macho_minimum_os"));
    }
    Ok((
        arch.into(),
        format!(
            "{}.{}.{}",
            minimum >> 16,
            (minimum >> 8) & 255,
            minimum & 255
        ),
    ))
}
pub fn inspect(path: &Path) -> Result<BTreeMap<String, String>, AppError> {
    let mut file = File::open(path)?;
    let size = file.metadata()?.len();
    if size < 8 {
        return Err(invalid());
    };
    let header = read(&mut file, 0, 8)?;
    let (little, wide) = match &header[..4] {
        [0xca, 0xfe, 0xba, 0xbe] => (false, false),
        [0xbe, 0xba, 0xfe, 0xca] => (true, false),
        [0xca, 0xfe, 0xba, 0xbf] => (false, true),
        [0xbf, 0xba, 0xfe, 0xca] => (true, true),
        _ => {
            let (arch, minimum) = thin(&mut file, 0, size)?;
            return Ok([(arch, minimum)].into());
        }
    };
    let count = u32_at(&header, 4, little)? as usize;
    if count == 0 || count > 16 {
        return Err(invalid());
    };
    let stride = if wide { 32 } else { 20 };
    let table = read(&mut file, 8, count * stride)?;
    let mut out = BTreeMap::new();
    let mut ranges = vec![];
    for i in 0..count {
        let start = i * stride;
        let (offset, length) = if wide {
            (
                u64_at(&table, start + 8, little)?,
                u64_at(&table, start + 16, little)?,
            )
        } else {
            (
                u64::from(u32_at(&table, start + 8, little)?),
                u64::from(u32_at(&table, start + 12, little)?),
            )
        };
        let end = offset.checked_add(length).ok_or_else(invalid)?;
        if offset < (8 + count * stride) as u64
            || end > size
            || ranges.iter().any(|(a, b)| offset < *b && end > *a)
        {
            return Err(invalid());
        };
        ranges.push((offset, end));
        let (arch, minimum) = thin(&mut file, offset, length)?;
        if out.insert(arch, minimum).is_some() {
            return Err(invalid());
        }
    }
    Ok(out)
}
