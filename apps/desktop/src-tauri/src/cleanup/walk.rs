//! Descriptor-relative traversal: no symlink traversal, including ancestor replacement.
use crate::AppError;
use nix::libc;
use std::{
    ffi::{CStr, CString, OsString},
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd, RawFd},
        unix::ffi::{OsStrExt, OsStringExt},
    },
    path::{Component, Path},
};

pub struct Directory(pub OwnedFd);
#[derive(Clone, Debug)]
pub struct Node {
    pub name: OsString,
    pub device: u64,
    pub inode: u64,
    pub uid: u32,
    pub kind: u8,
    pub bytes: u64,
    pub links: u64,
    pub modified: i64,
}
fn cstring(path: &Path) -> Result<CString, AppError> {
    CString::new(path.as_os_str().as_bytes())
        .map_err(|_| AppError::new("E_PROTECTED", "cleanup_path"))
}
impl Directory {
    pub fn open(path: &Path) -> Result<Self, AppError> {
        let path = cstring(path)?;
        Self::from_fd(unsafe {
            libc::open(
                path.as_ptr(),
                libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC,
            )
        })
    }
    pub fn verified_parent(
        path: &Path,
        snapshot: &crate::uninstall::rules::Snapshot,
    ) -> Result<Self, AppError> {
        let parent = path.parent().ok_or_else(super::cached_error)?;
        let mut directory = Self::open(Path::new("/"))?;
        let mut prefix = std::path::PathBuf::from("/");
        let verify = |directory: &Self, prefix: &Path| -> Result<(), AppError> {
            let identity = directory.identity()?;
            if snapshot
                .parents
                .iter()
                .any(|(p, d, i)| p == prefix && *d == identity.device && *i == identity.inode)
            {
                Ok(())
            } else {
                Err(AppError::new("E_PLAN_EXPIRED", "cleanup_parent_changed"))
            }
        };
        verify(&directory, &prefix)?;
        for component in parent.components() {
            match component {
                Component::RootDir => (),
                Component::Normal(name) => {
                    directory = directory.child(Path::new(name))?;
                    prefix.push(name);
                    verify(&directory, &prefix)?;
                }
                _ => return Err(AppError::new("E_PROTECTED", "cleanup_parent")),
            }
        }
        Ok(directory)
    }
    fn from_fd(fd: RawFd) -> Result<Self, AppError> {
        if fd < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(Self(unsafe { OwnedFd::from_raw_fd(fd) }))
    }
    pub fn child(&self, name: &Path) -> Result<Self, AppError> {
        let name = cstring(name)?;
        Self::from_fd(unsafe {
            libc::openat(
                self.0.as_raw_fd(),
                name.as_ptr(),
                libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC,
            )
        })
    }
    pub fn identity(&self) -> Result<Node, AppError> {
        self.stat(Path::new("."))
    }
    pub fn stat(&self, name: &Path) -> Result<Node, AppError> {
        let text = cstring(name)?;
        let mut stat: libc::stat = unsafe { std::mem::zeroed() };
        if unsafe {
            libc::fstatat(
                self.0.as_raw_fd(),
                text.as_ptr(),
                &mut stat,
                libc::AT_SYMLINK_NOFOLLOW,
            )
        } != 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        let kind = match stat.st_mode & libc::S_IFMT {
            libc::S_IFREG => 0,
            libc::S_IFDIR => 1,
            _ => 2,
        };
        Ok(Node {
            name: name.as_os_str().to_owned(),
            device: stat.st_dev as u64,
            inode: stat.st_ino,
            uid: stat.st_uid,
            kind,
            bytes: (stat.st_blocks.max(0) as u64).saturating_mul(512),
            links: stat.st_nlink as u64,
            modified: stat.st_mtime.saturating_mul(1000) + stat.st_mtime_nsec / 1_000_000,
        })
    }
    /// Lists this directory from its own descriptor, rewound first. Two threads must not list the same `Directory`
    /// at once; the descriptor-relative calls (`stat`, `child`, `unlink`) are safe to share.
    pub fn entries(&self) -> Result<Vec<Node>, AppError> {
        #[cfg(target_os = "macos")]
        if let Ok(entries) = self.bulk() {
            return Ok(entries);
        }
        self.fallback()
    }
    fn fallback(&self) -> Result<Vec<Node>, AppError> {
        use std::os::fd::IntoRawFd;
        let fd = self.child(Path::new("."))?.0.into_raw_fd();
        let stream = unsafe { libc::fdopendir(fd) };
        if stream.is_null() {
            unsafe {
                libc::close(fd);
            }
            return Err(std::io::Error::last_os_error().into());
        }
        struct Close(*mut libc::DIR);
        impl Drop for Close {
            fn drop(&mut self) {
                unsafe {
                    libc::closedir(self.0);
                }
            }
        }
        let _guard = Close(stream);
        let mut entries = vec![];
        loop {
            #[cfg(target_os = "macos")]
            unsafe {
                *libc::__error() = 0;
            }
            #[cfg(target_os = "linux")]
            unsafe {
                *libc::__errno_location() = 0;
            }
            let entry = unsafe { libc::readdir(stream) };
            if entry.is_null() {
                if std::io::Error::last_os_error().raw_os_error().unwrap_or(0) != 0 {
                    return Err(std::io::Error::last_os_error().into());
                }
                break;
            }
            let name = unsafe { CStr::from_ptr((*entry).d_name.as_ptr()) }.to_bytes();
            if name == b"." || name == b".." {
                continue;
            }
            let name = OsString::from_vec(name.to_vec());
            match self.stat(Path::new(&name)) {
                Ok(node) => entries.push(node),
                Err(error) => return Err(error),
            }
        }
        Ok(entries)
    }
    /// Permission bits of this directory, read from its own descriptor.
    pub fn mode(&self) -> Result<u16, AppError> {
        let mut stat: libc::stat = unsafe { std::mem::zeroed() };
        if unsafe { libc::fstat(self.0.as_raw_fd(), &mut stat) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok((stat.st_mode & 0o7777) as u16)
    }
    /// fchmod on the descriptor, so a path swapped underneath can never be the one changed.
    pub fn set_mode(&self, mode: u16) -> Result<(), AppError> {
        if unsafe { libc::fchmod(self.0.as_raw_fd(), mode as libc::mode_t) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(())
    }
    pub fn unlink(&self, name: &Path, directory: bool) -> Result<(), AppError> {
        let name = cstring(name)?;
        if unsafe {
            libc::unlinkat(
                self.0.as_raw_fd(),
                name.as_ptr(),
                if directory { libc::AT_REMOVEDIR } else { 0 },
            )
        } != 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        Ok(())
    }
    #[cfg(target_os = "macos")]
    fn bulk(&self) -> Result<Vec<Node>, AppError> {
        // Darwin sys/attr.h: attributes are packed in bitmap order, aligned to four bytes.
        // RETURNED_ATTRS identifies unavailable fields. Directory records omit the
        // file attribute block even with PACK_INVAL_ATTRS, so never decode it for dirs.
        let common = libc::ATTR_CMN_RETURNED_ATTRS
            | libc::ATTR_CMN_NAME
            | libc::ATTR_CMN_DEVID
            | libc::ATTR_CMN_OBJTYPE
            | libc::ATTR_CMN_MODTIME
            | libc::ATTR_CMN_OWNERID
            | libc::ATTR_CMN_FILEID;
        let files = libc::ATTR_FILE_LINKCOUNT | libc::ATTR_FILE_ALLOCSIZE;
        let mut attrs = libc::attrlist {
            bitmapcount: libc::ATTR_BIT_MAP_COUNT,
            reserved: 0,
            commonattr: common,
            volattr: 0,
            dirattr: 0,
            fileattr: files,
            forkattr: 0,
        };
        // getattrlistbulk continues from the descriptor's offset; rewinding it is cheaper than opening the directory
        // again for every listing.
        if unsafe { libc::lseek(self.0.as_raw_fd(), 0, libc::SEEK_SET) } < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        thread_local! {
            // One listing buffer per thread instead of a fresh 256 KiB allocation for every directory.
            static BUFFER: std::cell::RefCell<Vec<u8>> = std::cell::RefCell::new(vec![0u8; 256 * 1024]);
        }
        BUFFER.with_borrow_mut(|buffer| self.bulk_into(&mut attrs, common, files, buffer))
    }
    #[cfg(target_os = "macos")]
    fn bulk_into(
        &self,
        attrs: &mut libc::attrlist,
        common: u32,
        files: u32,
        buffer: &mut [u8],
    ) -> Result<Vec<Node>, AppError> {
        let mut entries = vec![];
        loop {
            let count = unsafe {
                libc::getattrlistbulk(
                    self.0.as_raw_fd(),
                    (attrs as *mut libc::attrlist).cast(),
                    buffer.as_mut_ptr().cast(),
                    buffer.len(),
                    (libc::FSOPT_NOFOLLOW | libc::FSOPT_PACK_INVAL_ATTRS) as u64,
                )
            };
            if count < 0 {
                return Err(std::io::Error::last_os_error().into());
            }
            if count == 0 {
                break;
            }
            let mut offset = 0;
            for _ in 0..count {
                let length = read_u32(buffer, offset)? as usize;
                let record = buffer.get(offset..offset + length).ok_or_else(bulk_error)?;
                // Advance first: a skipped record must not be read again in place of the next one.
                offset += length;
                if record.len() < 68 {
                    return Err(bulk_error());
                }
                let returned_common = read_u32(record, 4)?;
                let returned_file = read_u32(record, 16)?;
                if returned_common & libc::ATTR_CMN_NAME == 0 {
                    return Err(bulk_error());
                }
                let start = 24i64 + read_u32(record, 24)? as i32 as i64;
                let end = start + read_u32(record, 28)? as i64;
                if start < 0 || end <= start {
                    return Err(bulk_error());
                }
                let name = record
                    .get(start as usize..end as usize)
                    .ok_or_else(bulk_error)?;
                if name.last() != Some(&0)
                    || name[..name.len() - 1].contains(&b'/')
                    || name[..name.len() - 1].contains(&0)
                {
                    return Err(bulk_error());
                }
                let name = OsString::from_vec(name[..name.len() - 1].to_vec());
                if name == "." || name == ".." {
                    continue;
                }
                let kind = match read_u32(record, 36)? {
                    1 => 0,
                    2 => 1,
                    _ => 2,
                };
                let required = common & !libc::ATTR_CMN_RETURNED_ATTRS;
                let node = if returned_common & required != required
                    || (kind == 0 && returned_file & files != files)
                {
                    self.stat(Path::new(&name))?
                } else {
                    Node {
                        name,
                        device: read_u32(record, 32)? as u64,
                        inode: read_u64(record, 60)?,
                        uid: read_u32(record, 56)?,
                        kind,
                        bytes: if kind == 0 { read_u64(record, 72)? } else { 0 },
                        links: if kind == 0 {
                            read_u32(record, 68)? as u64
                        } else {
                            0
                        },
                        modified: (read_u64(record, 40)? as i64).saturating_mul(1000)
                            + (read_u64(record, 48)? as i64) / 1_000_000,
                    }
                };
                entries.push(node);
            }
        }
        Ok(entries)
    }
}
#[cfg(target_os = "macos")]
fn bulk_error() -> AppError {
    AppError::new("E_UNKNOWN", "cleanup_bulk_attributes")
}
#[cfg(target_os = "macos")]
fn read_u32(bytes: &[u8], offset: usize) -> Result<u32, AppError> {
    Ok(u32::from_ne_bytes(
        bytes
            .get(offset..offset + 4)
            .ok_or_else(bulk_error)?
            .try_into()
            .map_err(|_| bulk_error())?,
    ))
}
#[cfg(target_os = "macos")]
fn read_u64(bytes: &[u8], offset: usize) -> Result<u64, AppError> {
    Ok(u64::from_ne_bytes(
        bytes
            .get(offset..offset + 8)
            .ok_or_else(bulk_error)?
            .try_into()
            .map_err(|_| bulk_error())?,
    ))
}

#[cfg(all(test, target_os = "macos"))]
mod tests {
    use super::*;
    #[test]
    fn bulk_attributes_match_descriptor_stat_including_directories_and_symlinks() {
        let temporary = tempfile::tempdir().unwrap();
        let path = temporary.path().canonicalize().unwrap();
        std::fs::create_dir(path.join("sub")).unwrap();
        std::fs::write(path.join("file with spaces"), [1u8; 65536]).unwrap();
        std::os::unix::fs::symlink(path.join("sub"), path.join("link")).unwrap();
        let directory = Directory::open(&path).unwrap();
        let bulk = directory.bulk().unwrap();
        assert_eq!(bulk.len(), 3);
        for node in bulk {
            let stat = directory.stat(Path::new(&node.name)).unwrap();
            assert_eq!(
                (node.device, node.inode, node.uid, node.kind, node.modified),
                (stat.device, stat.inode, stat.uid, stat.kind, stat.modified)
            );
            if node.kind == 0 {
                assert_eq!((node.bytes, node.links), (stat.bytes, stat.links));
            }
        }
        assert_eq!(directory.fallback().unwrap().len(), 3);
    }
}
