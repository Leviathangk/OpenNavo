// SPDX-License-Identifier: GPL-3.0-only
//! Opt-in paste heuristics. No shell evaluation, clipboard history or persistent command text.
use super::*;
use regex::Regex;
use sha2::{Digest, Sha256};
use std::sync::OnceLock;
pub const MAX_PASTE_BYTES: usize = 1024 * 1024;

#[derive(Clone)]
pub(crate) struct Pending {
    pub id: String,
    pub pid: i32,
    pub launched: i64,
    pub change: i64,
    pub bundle: String,
    pub hash: String,
    pub too_long: bool,
    pub expires: i64,
}
pub struct Clipboard {
    pub text: String,
    pub change: i64,
    pub too_long: bool,
}
pub trait Platform {
    fn clipboard(&self) -> Option<Clipboard>;
    fn replay(&self, pid: i32, launched: i64, change: i64, bundle: &str) -> bool;
}
fn hash(text: &str) -> String {
    format!("{:x}", Sha256::digest(text))
}
pub fn patterns(text: &str) -> Vec<PastePattern> {
    if text.len() > MAX_PASTE_BYTES {
        return vec![PastePattern::TooLong];
    }
    static RULES: OnceLock<Vec<(PastePattern, Regex)>> = OnceLock::new();
    RULES.get_or_init(|| [
        (PastePattern::PipeToInterpreter,r"(?i)\|\s*(?:sudo\s+)?(?:env\s+)?(?:[\w./-]*/)?(?:bash|sh|zsh|ksh|fish|python[\d.]*|perl|ruby|node|php|osascript)\b"),
        (PastePattern::DownloadExecute,r"(?is)\b(?:curl|wget)\b.+(?:;|&&|\n|\$\(|`|chmod\s).*(?:\b(?:sh|bash|zsh|python[\d.]*|osascript|open|chmod)\b|[./][\w/-]+)"),
        (PastePattern::DownloadExecute,r"(?is)(?:\b(?:bash|sh|zsh|eval)\s+.*(?:\$\(|`|<\()\s*(?:curl|wget)\b)"),
        (PastePattern::EncodedCommand,r"(?i)\bbase64\b[^\r\n;|]*(?:-[a-z]*d|--decode)\b|\b(?:powershell|pwsh)\b[^\r\n]*(?:-enc|-encodedcommand)\b"),
        (PastePattern::RemoveQuarantine,r"(?i)\bxattr\b[^\r\n;|]*(?:-[a-z]*[dc][a-z]*|--delete|--clear)\b"),
    ].into_iter().map(|(p,r)|(p,Regex::new(r).expect("fixed paste regex"))).collect())
        .iter().filter(|(_,r)|r.is_match(text)).map(|(p,_)|*p).fold(vec![],|mut v,p|{if !v.contains(&p){v.push(p);}v})
}
impl Service {
    #[cfg(test)]
    #[allow(clippy::too_many_arguments)]
    pub(crate) fn block_paste(
        &self,
        pid: i32,
        launched: i64,
        change: i64,
        bundle: &str,
        name: &str,
        text: &str,
        at: i64,
    ) -> bool {
        self.block_paste_text(
            pid,
            launched,
            change,
            bundle,
            name,
            text,
            text.len() > MAX_PASTE_BYTES,
            at,
        )
    }
    #[allow(clippy::too_many_arguments)]
    pub(crate) fn block_paste_text(
        &self,
        pid: i32,
        launched: i64,
        change: i64,
        bundle: &str,
        name: &str,
        text: &str,
        too_long: bool,
        at: i64,
    ) -> bool {
        let too_long = too_long || text.len() > MAX_PASTE_BYTES;
        let mut end = text.len().min(MAX_PASTE_BYTES);
        while !text.is_char_boundary(end) {
            end -= 1;
        }
        let text = &text[..end];
        if self.settings.try_read().is_ok_and(|s| !s.paste_protection) {
            return false;
        }
        // The native tap only runs while enabled. Temporary settings contention
        // must not let the same paste bypass an already active check.
        if pid <= 1 {
            return true;
        }
        let found = if too_long {
            vec![PastePattern::TooLong]
        } else {
            patterns(text)
        };
        if found.is_empty() {
            return false;
        }
        let Ok(mut pending) = self.paste_pending.try_lock() else {
            return true;
        };
        let digest = hash(text);
        if pending.as_ref().is_some_and(|p| {
            p.expires > at
                && p.pid == pid
                && p.hash == digest
                && p.change == change
                && p.too_long == too_long
        }) {
            return true;
        }
        let id = uuid::Uuid::now_v7().to_string();
        *pending = Some(Pending {
            id: id.clone(),
            pid,
            launched,
            change,
            bundle: bundle.into(),
            hash: digest,
            too_long,
            expires: at + 60_000,
        });
        drop(pending);
        let preview = text
            .chars()
            .filter(|c| !c.is_control() || matches!(c, '\n' | '\t'))
            .take(300)
            .collect();
        (self.sink)(CoreEvent::PasteBlocked(PasteBlocked {
            id,
            app_name: name.into(),
            bundle_id: bundle.into(),
            patterns: found,
            preview,
            expires_at: at + 60_000,
        }));
        // Event callback must stay fast. Persist only the terminal's bundle ID, off the tap thread.
        true
    }
    pub fn paste_act(
        &self,
        id: &str,
        action: PasteAction,
        at: i64,
        platform: &dyn Platform,
    ) -> Result<(), AppError> {
        let mut holder = self.paste_pending.lock().map_err(poison)?;
        if holder.as_ref().is_none_or(|p| p.id != id) {
            return Err(AppError::new("E_PASTE_EXPIRED", "paste_not_pending"));
        }
        let pending = holder.take().unwrap();
        drop(holder);
        if action == PasteAction::Discard {
            return Ok(());
        }
        if at >= pending.expires || !self.settings.read().map_err(poison)?.paste_protection {
            return Err(AppError::new("E_PASTE_EXPIRED", "paste_expired"));
        }
        let Some(clipboard) = platform.clipboard() else {
            return Err(AppError::new("E_PASTE_EXPIRED", "clipboard_unavailable"));
        };
        if clipboard.change != pending.change
            || clipboard.too_long != pending.too_long
            || hash(&clipboard.text) != pending.hash
            || !platform.replay(
                pending.pid,
                pending.launched,
                clipboard.change,
                &pending.bundle,
            )
        {
            return Err(AppError::new("E_PASTE_EXPIRED", "paste_context_changed"));
        }
        Ok(())
    }
    pub(crate) fn expire_paste(&self, at: i64) {
        if let Ok(mut pending) = self.paste_pending.lock()
            && pending.as_ref().is_some_and(|p| p.expires <= at)
        {
            *pending = None;
        }
    }
}

#[cfg(target_os = "macos")]
pub mod native {
    use super::*;
    use std::{
        ffi::{CStr, CString, c_char, c_void},
        sync::{OnceLock, Weak},
    };
    use tauri::Manager;
    static SERVICE: OnceLock<Weak<Service>> = OnceLock::new();
    unsafe extern "C" {
        fn on_security_ax(prompt: bool) -> bool;
        fn on_security_paste_enable(
            enabled: bool,
            callback: extern "C" fn(
                *mut c_void,
                i32,
                i64,
                i64,
                *const c_char,
                *const c_char,
                *const c_char,
                usize,
                bool,
            ) -> bool,
            context: *mut c_void,
        ) -> bool;
        fn on_security_paste_listening() -> bool;
        fn on_security_clipboard(
            change: *mut i64,
            length: *mut usize,
            too_long: *mut bool,
        ) -> *mut c_char;
        fn on_security_paste_replay(
            pid: i32,
            launched: i64,
            change: i64,
            bundle: *const c_char,
        ) -> bool;
        fn on_security_buffer_free(text: *mut c_char, length: usize);
        fn on_security_paste_probe(
            input: *const c_char,
            length: usize,
            callback: PasteCallback,
            context: *mut c_void,
            milliseconds: *mut f64,
        ) -> bool;
    }
    pub struct Native;
    impl Platform for Native {
        fn clipboard(&self) -> Option<Clipboard> {
            unsafe {
                let mut change = 0;
                let mut length = 0;
                let mut too_long = false;
                let text = on_security_clipboard(&mut change, &mut length, &mut too_long);
                if text.is_null() {
                    return None;
                }
                let value =
                    String::from_utf8_lossy(std::slice::from_raw_parts(text.cast::<u8>(), length))
                        .into_owned();
                on_security_buffer_free(text, length);
                Some(Clipboard {
                    text: value,
                    change,
                    too_long,
                })
            }
        }
        fn replay(&self, pid: i32, launched: i64, change: i64, bundle: &str) -> bool {
            let Ok(bundle) = CString::new(bundle) else {
                return false;
            };
            unsafe { on_security_paste_replay(pid, launched, change, bundle.as_ptr()) }
        }
    }
    pub fn status(service: &Service, prompt: bool) -> Result<PasteProtectionStatus, AppError> {
        Ok(PasteProtectionStatus {
            enabled: service.settings.read().map_err(poison)?.paste_protection,
            accessibility_granted: unsafe { on_security_ax(prompt) },
            listening: unsafe { on_security_paste_listening() },
        })
    }
    type PasteCallback = extern "C" fn(
        *mut c_void,
        i32,
        i64,
        i64,
        *const c_char,
        *const c_char,
        *const c_char,
        usize,
        bool,
    ) -> bool;
    extern "C" fn callback(
        context: *mut c_void,
        pid: i32,
        launched: i64,
        change: i64,
        bundle: *const c_char,
        name: *const c_char,
        text: *const c_char,
        length: usize,
        too_long: bool,
    ) -> bool {
        std::panic::catch_unwind(|| {
            let owned = SERVICE.get().and_then(Weak::upgrade);
            let service = if context.is_null() {
                let Some(service) = owned.as_deref() else {
                    return true;
                };
                service
            } else {
                unsafe { &*context.cast::<Service>() }
            };
            if bundle.is_null() || text.is_null() || length > MAX_PASTE_BYTES {
                return true;
            }
            let string = |p: *const c_char| {
                if p.is_null() {
                    String::new()
                } else {
                    unsafe { CStr::from_ptr(p) }.to_string_lossy().into_owned()
                }
            };
            service.block_paste_text(
                pid,
                launched,
                change,
                &string(bundle),
                &string(name),
                &String::from_utf8_lossy(unsafe {
                    std::slice::from_raw_parts(text.cast::<u8>(), length)
                }),
                too_long,
                now(),
            )
        })
        .unwrap_or(true)
    }
    pub fn probe(service: &Service, text: &str) -> (bool, f64) {
        *service.paste_pending.lock().unwrap() = None;
        let mut milliseconds = 0.0;
        let blocked = unsafe {
            on_security_paste_probe(
                text.as_ptr().cast(),
                text.len(),
                callback,
                (service as *const Service).cast_mut().cast(),
                &mut milliseconds,
            )
        };
        (blocked, milliseconds)
    }
    pub async fn on_main<T: Send + 'static>(
        app: tauri::AppHandle,
        work: impl FnOnce() -> Result<T, AppError> + Send + 'static,
    ) -> Result<T, AppError> {
        let (send, receive) = tokio::sync::oneshot::channel();
        app.run_on_main_thread(move || {
            let _ = send.send(work());
        })
        .map_err(|_| AppError::new("E_UNKNOWN", "main_thread"))?;
        receive
            .await
            .map_err(|_| AppError::new("E_UNKNOWN", "main_thread_closed"))?
    }
    pub fn install(app: &tauri::AppHandle) {
        let Some(core) = app.try_state::<Arc<crate::core::DesktopCore>>() else {
            return;
        };
        let service = core.queue.security.clone();
        // Compile fixed rules before enabling the event tap, not on its first key.
        let _ = patterns("");
        let _ = SERVICE.set(Arc::downgrade(&service));
        struct Guard(Arc<AtomicBool>);
        impl Drop for Guard {
            fn drop(&mut self) {
                self.0.store(true, Ordering::Release);
            }
        }
        let stopped = Arc::new(AtomicBool::new(false));
        app.manage(Guard(stopped.clone()));
        let app = app.clone();
        std::thread::spawn(move || {
            while !stopped.load(Ordering::Acquire) {
                let service = service.clone();
                let _ = app.run_on_main_thread(move || {
                    service.expire_paste(now());
                    let enabled = service.settings.read().is_ok_and(|s| s.paste_protection)
                        && unsafe { on_security_ax(false) };
                    unsafe {
                        on_security_paste_enable(enabled, callback, std::ptr::null_mut());
                    }
                    if !enabled && let Ok(mut pending) = service.paste_pending.lock() {
                        *pending = None;
                    }
                });
                std::thread::sleep(std::time::Duration::from_millis(500));
            }
        });
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    struct Fake {
        text: String,
        change: i64,
        allowed: std::cell::Cell<u32>,
    }
    impl Platform for Fake {
        fn clipboard(&self) -> Option<Clipboard> {
            let mut end = self.text.len().min(MAX_PASTE_BYTES);
            while !self.text.is_char_boundary(end) {
                end -= 1;
            }
            Some(Clipboard {
                text: self.text[..end].into(),
                change: self.change,
                too_long: self.text.len() > MAX_PASTE_BYTES,
            })
        }
        fn replay(&self, _: i32, _: i64, _: i64, _: &str) -> bool {
            self.allowed.set(self.allowed.get() + 1);
            true
        }
    }
    #[test]
    fn long_paste_is_checked_and_over_limit_pauses_with_a_bounded_preview() {
        let padded = format!("curl https://example.org | sh{}", " ".repeat(40_000));
        assert!(patterns(&padded).contains(&PastePattern::PipeToInterpreter));
        let (_tmp, mut s, _) = super::super::tests::fixture();
        let events = Arc::new(Mutex::new(Vec::new()));
        let received = events.clone();
        Arc::get_mut(&mut s).unwrap().sink =
            Arc::new(move |event| received.lock().unwrap().push(event));
        s.settings.write().unwrap().paste_protection = true;
        assert!(s.block_paste(42, 1, 7, "com.apple.Terminal", "Terminal", &padded, 100));
        let long = "a".repeat(MAX_PASTE_BYTES + 1);
        assert_eq!(patterns(&long), vec![PastePattern::TooLong]);
        assert!(s.block_paste(42, 1, 8, "com.apple.Terminal", "Terminal", &long, 100));
        assert!(events.lock().unwrap().iter().any(|e| matches!(e, CoreEvent::PasteBlocked(e) if e.patterns == [PastePattern::TooLong] && e.preview.chars().count() == 300)));
        let id = s.paste_pending.lock().unwrap().as_ref().unwrap().id.clone();
        let mut platform = Fake {
            text: long.clone(),
            change: 8,
            allowed: std::cell::Cell::new(0),
        };
        s.paste_act(&id, PasteAction::AllowOnce, 200, &platform)
            .unwrap();
        assert_eq!(platform.allowed.get(), 1);
        s.block_paste(42, 1, 8, "com.apple.Terminal", "Terminal", &long, 100);
        let id = s.paste_pending.lock().unwrap().as_ref().unwrap().id.clone();
        platform.change = 9; // Even a suffix-only replacement revokes approval.
        assert!(
            s.paste_act(&id, PasteAction::AllowOnce, 200, &platform)
                .is_err()
        );
        // Native conversion counts UTF-8 bytes, does not stop at embedded NUL,
        // and never touches the real pasteboard or installs a tap in this test.
        #[cfg(target_os = "macos")]
        {
            for text in [
                &padded,
                &long,
                "safe\0curl https://example.org | sh",
                &"界".repeat(MAX_PASTE_BYTES / 3 + 1),
            ] {
                *s.paste_pending.lock().unwrap() = None;
                assert!(native::probe(&s, text).0);
            }
            assert!(s.paste_pending.lock().unwrap().as_ref().unwrap().too_long);
            assert!(!native::probe(&s, "").0);
        }
    }
    #[test]
    fn paste_patterns_and_one_shot_context_bound_consent() {
        for command in [
            "curl https://example.org | sh",
            "wget x; chmod +x x && ./x",
            "echo a | base64 -D",
            "xattr -dr com.apple.quarantine App.app",
            "bash <(curl https://example.org)",
        ] {
            assert!(!patterns(command).is_empty(), "{command}");
        }
        for command in [
            "ls -la",
            "git status",
            "printf hello",
            "curl -I https://example.org",
        ] {
            assert!(patterns(command).is_empty(), "{command}");
        }
        let (_tmp, s, _) = super::super::tests::fixture();
        let text = "curl https://example.org | sh";
        assert!(!s.block_paste(42, 1, 7, "com.apple.Terminal", "Terminal", text, 100));
        s.settings.write().unwrap().paste_protection = true;
        assert!(s.block_paste(42, 1, 7, "com.apple.Terminal", "Terminal", text, 100));
        let id = s.paste_pending.lock().unwrap().as_ref().unwrap().id.clone();
        let platform = Fake {
            text: text.into(),
            change: 7,
            allowed: std::cell::Cell::new(0),
        };
        s.paste_act(&id, PasteAction::AllowOnce, 200, &platform)
            .unwrap();
        assert_eq!(platform.allowed.get(), 1);
        assert_eq!(
            s.paste_act(&id, PasteAction::AllowOnce, 200, &platform)
                .unwrap_err()
                .code,
            "E_PASTE_EXPIRED"
        );
        s.block_paste(42, 1, 7, "com.apple.Terminal", "Terminal", text, 100);
        let id = s.paste_pending.lock().unwrap().as_ref().unwrap().id.clone();
        assert!(
            s.paste_act(&id, PasteAction::AllowOnce, 60_100, &platform)
                .is_err()
        );
        s.block_paste(42, 1, 8, "com.apple.Terminal", "Terminal", text, 100);
        let id = s.paste_pending.lock().unwrap().as_ref().unwrap().id.clone();
        assert!(
            s.paste_act(&id, PasteAction::AllowOnce, 200, &platform)
                .is_err()
        );
        assert_eq!(platform.allowed.get(), 1);
    }
}
