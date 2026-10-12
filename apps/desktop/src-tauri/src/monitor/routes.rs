//! Bounded Darwin sockaddr parser used by the read-only default-route query.
#[cfg(target_os = "macos")]
pub fn interface_counters(buffer: &[u8], index: u32) -> Option<(u64, u64)> {
    use nix::libc;
    let mut offset = 0;
    while offset + 4 <= buffer.len() {
        let size = u16::from_ne_bytes(buffer[offset..offset + 2].try_into().ok()?) as usize;
        if size < 4 || offset + size > buffer.len() {
            return None;
        }
        if buffer[offset + 3] as i32 == libc::RTM_IFINFO2
            && size >= std::mem::size_of::<libc::if_msghdr2>()
        {
            // Routing messages need not be aligned; counters are the complete if_data64 values.
            let msg = unsafe {
                std::ptr::read_unaligned(buffer.as_ptr().add(offset).cast::<libc::if_msghdr2>())
            };
            if u32::from(msg.ifm_index) == index {
                return Some((msg.ifm_data.ifi_ibytes, msg.ifm_data.ifi_obytes));
            }
        }
        offset += size;
    }
    None
}

pub fn is_default(addresses: &[u8], mask: u32, family: u8) -> bool {
    let mut offset = 0;
    let mut destination = None;
    let mut netmask = None;
    for index in 0..8 {
        if mask & (1 << index) == 0 {
            continue;
        }
        if offset + 2 > addresses.len() {
            return false;
        }
        let len = addresses[offset] as usize;
        let step = if len == 0 { 4 } else { (len + 3) & !3 };
        if offset + step > addresses.len() || (len > 0 && len < 2) {
            return false;
        }
        let value = &addresses[offset..offset + len];
        if index == 0 {
            destination = Some(value);
        }
        if index == 2 {
            netmask = Some(value);
        }
        offset += step;
    }
    let Some(dst) = destination else {
        return false;
    };
    if dst.len() < 2 || dst[1] != family {
        return false;
    }
    let (start, end) = match family {
        2 => (4, 8),
        30 => (8, 24),
        _ => return false,
    };
    if dst.len() < end || dst[start..end].iter().any(|v| *v != 0) {
        return false;
    }
    netmask.is_some_and(|mask| {
        mask.len() <= start || mask[start..mask.len().min(end)].iter().all(|v| *v == 0)
    })
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn accepts_only_bounded_zero_destinations_and_masks() {
        let mut dst = vec![0u8; 16];
        dst[0] = 16;
        dst[1] = 2;
        let mut all = dst.clone();
        all.extend([0u8; 4]);
        assert!(is_default(&all, 5, 2));
        all[4] = 10;
        assert!(!is_default(&all, 5, 2));
        all[4] = 0;
        all[16] = 8;
        all.extend([0, 0, 0, 0]);
        all[20] = 128;
        assert!(!is_default(&all, 5, 2));
        assert!(!is_default(&dst[..7], 1, 2));
        assert!(!is_default(&dst, 5, 2));
        assert!(!is_default(&all, 5, 30));
    }
}
