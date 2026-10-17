// SPDX-License-Identifier: GPL-3.0-only
//! Explicit hash-only lookups. No upload API, automatic queries or caller-selected endpoint.
use super::*;
use serde::{Deserialize, Serialize};
use std::time::Duration;

pub trait Secrets: Send + Sync {
    fn get(&self) -> Result<Option<String>, AppError>;
    fn set(&self, key: Option<&str>) -> Result<(), AppError>;
}
pub struct Keychain;
impl Secrets for Keychain {
    fn get(&self) -> Result<Option<String>, AppError> {
        #[cfg(target_os = "macos")]
        unsafe {
            unsafe extern "C" {
                fn on_security_key_read(out: *mut *mut std::ffi::c_char) -> i32;
                fn on_security_free(value: *mut std::ffi::c_char);
            }
            let mut out = std::ptr::null_mut();
            let status = on_security_key_read(&mut out);
            if status != 0 {
                return Err(AppError::new("E_KEYCHAIN", "keychain_read"));
            }
            if out.is_null() {
                return Ok(None);
            }
            let key = std::ffi::CStr::from_ptr(out).to_string_lossy().into_owned();
            on_security_free(out);
            Ok(Some(key))
        }
        #[cfg(not(target_os = "macos"))]
        {
            Err(AppError::new("E_KEYCHAIN", "keychain_unavailable"))
        }
    }
    fn set(&self, key: Option<&str>) -> Result<(), AppError> {
        #[cfg(target_os = "macos")]
        unsafe {
            unsafe extern "C" {
                fn on_security_key_set(value: *const std::ffi::c_char) -> i32;
            }
            let key = key
                .map(std::ffi::CString::new)
                .transpose()
                .map_err(|_| AppError::new("E_INVALID_ARG", "vt_key"))?;
            if on_security_key_set(key.as_ref().map_or(std::ptr::null(), |s| s.as_ptr())) == 0 {
                Ok(())
            } else {
                Err(AppError::new("E_KEYCHAIN", "keychain_write"))
            }
        }
        #[cfg(not(target_os = "macos"))]
        {
            let _ = key;
            Err(AppError::new("E_KEYCHAIN", "keychain_unavailable"))
        }
    }
}
#[derive(Default, Serialize, Deserialize)]
struct Quota {
    next_at: i64,
    day: i64,
    used: u32,
}
const QUOTA: &str = "security_s2_vt_quota";
fn limited(seconds: u64) -> AppError {
    AppError::new("E_VT_RATE_LIMITED", "vt_rate_limited")
        .with_detail(serde_json::json!({"retryAfterSecs":seconds}).to_string())
}
impl Service {
    pub fn vt_status(&self) -> Result<VirusTotalStatus, AppError> {
        let quota: Quota = self.s2_get(QUOTA)?.unwrap_or_default();
        let at = now();
        let daily = if quota.day == at.div_euclid(86_400_000) && quota.used >= 500 {
            (quota.day + 1) * 86_400_000
        } else {
            0
        };
        Ok(VirusTotalStatus {
            enabled: self.settings.read().map_err(poison)?.virus_total_enabled,
            has_key: self.vt_secrets.get()?.is_some(),
            retry_after_secs: ((quota.next_at.max(daily) - at).max(0) as u64).div_ceil(1000),
        })
    }
    pub fn vt_set_key(&self, key: Option<String>) -> Result<VirusTotalStatus, AppError> {
        if key.as_ref().is_some_and(|s| {
            s.len() < 16
                || s.len() > 256
                || !s
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"_-".contains(&b))
        }) {
            return Err(AppError::new("E_INVALID_ARG", "vt_key"));
        }
        self.vt_secrets.set(key.as_deref())?;
        self.vt_status()
    }
    pub async fn vt_lookup(&self, sha256: String) -> Result<VirusTotalVerdict, AppError> {
        self.vt_lookup_at(sha256, "https://www.virustotal.com/api/v3/files/")
            .await
    }
    async fn vt_lookup_at(
        &self,
        sha256: String,
        base: &str,
    ) -> Result<VirusTotalVerdict, AppError> {
        if sha256.len() != 64 || !sha256.bytes().all(|b| b.is_ascii_hexdigit()) {
            return Err(AppError::new("E_INVALID_ARG", "sha256"));
        }
        let sha256 = sha256.to_ascii_lowercase();
        let _guard = self.vt_serial.lock().await;
        if !self.settings.read().map_err(poison)?.virus_total_enabled {
            return Err(AppError::new("E_VT_DISABLED", "vt_disabled"));
        }
        let key = self
            .vt_secrets
            .get()?
            .ok_or_else(|| AppError::new("E_VT_KEY_REQUIRED", "vt_key_required"))?;
        let mut quota: Quota = self.s2_get(QUOTA)?.unwrap_or_default();
        let at = now();
        let day = at.div_euclid(86_400_000);
        if quota.day != day {
            quota.day = day;
            quota.used = 0;
        }
        if quota.next_at > at {
            return Err(limited((quota.next_at - at) as u64 / 1000 + 1));
        }
        if quota.used >= 500 {
            return Err(limited(((day + 1) * 86_400_000 - at) as u64 / 1000 + 1));
        }
        quota.used += 1;
        quota.next_at = at + 15_000;
        self.s2_set(QUOTA, &quota)?;
        let mut header = reqwest::header::HeaderValue::from_str(&key)
            .map_err(|_| AppError::new("E_VT_KEY_INVALID", "vt_key_invalid"))?;
        header.set_sensitive(true);
        let client = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(Duration::from_secs(20))
            .build()
            .map_err(|_| AppError::new("E_VT_UNAVAILABLE", "vt_client"))?;
        let mut response = client
            .get(format!("{base}{sha256}"))
            .header("x-apikey", header)
            .send()
            .await
            .map_err(|_| AppError::new("E_VT_UNAVAILABLE", "vt_network"))?;
        let code = response.status().as_u16();
        if code == 401 || code == 403 {
            return Err(AppError::new("E_VT_KEY_INVALID", "vt_auth"));
        }
        if code == 429 {
            let seconds = response
                .headers()
                .get("Retry-After")
                .and_then(|h| h.to_str().ok())
                .and_then(|s| s.parse::<u64>().ok())
                .unwrap_or(60)
                .clamp(15, 86400);
            quota.next_at = now() + seconds as i64 * 1000;
            self.s2_set(QUOTA, &quota)?;
            return Err(limited(seconds));
        }
        if code == 404 {
            return Ok(verdict(&sha256));
        }
        if code != 200 {
            return Err(AppError::new("E_VT_UNAVAILABLE", "vt_http"));
        }
        let mut body = vec![];
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|_| AppError::new("E_VT_UNAVAILABLE", "vt_body"))?
        {
            if body.len() + chunk.len() > 1024 * 1024 {
                return Err(AppError::new("E_VT_UNAVAILABLE", "vt_body_limit"));
            }
            body.extend_from_slice(&chunk);
        }
        parse(&sha256, &body)
    }
}
fn verdict(hash: &str) -> VirusTotalVerdict {
    VirusTotalVerdict {
        sha256: hash.into(),
        found: false,
        malicious: 0,
        suspicious: 0,
        harmless: 0,
        undetected: 0,
        last_analysis_at: None,
        permalink: format!("https://www.virustotal.com/gui/file/{hash}"),
    }
}
fn parse(hash: &str, bytes: &[u8]) -> Result<VirusTotalVerdict, AppError> {
    let invalid = || AppError::new("E_VT_UNAVAILABLE", "vt_response");
    let value: serde_json::Value = serde_json::from_slice(bytes).map_err(|_| invalid())?;
    let data = &value["data"];
    if data["id"].as_str() != Some(hash) {
        return Err(invalid());
    }
    let stats = data["attributes"]["last_analysis_stats"]
        .as_object()
        .ok_or_else(invalid)?;
    let count = |key: &str| -> Result<u32, AppError> {
        stats
            .get(key)
            .map(|v| {
                v.as_u64()
                    .and_then(|v| v.try_into().ok())
                    .ok_or_else(invalid)
            })
            .unwrap_or(Ok(0))
    };
    let mut result = verdict(hash);
    result.found = true;
    result.malicious = count("malicious")?;
    result.suspicious = count("suspicious")?;
    result.harmless = count("harmless")?;
    result.undetected = count("undetected")?;
    result.last_analysis_at = data["attributes"]["last_analysis_date"]
        .as_i64()
        .and_then(|v| v.checked_mul(1000));
    Ok(result)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[derive(Default)]
    struct Fake(Mutex<Option<String>>);
    impl Secrets for Fake {
        fn get(&self) -> Result<Option<String>, AppError> {
            Ok(self.0.lock().unwrap().clone())
        }
        fn set(&self, k: Option<&str>) -> Result<(), AppError> {
            *self.0.lock().unwrap() = k.map(str::to_owned);
            Ok(())
        }
    }
    #[tokio::test]
    async fn vt_disabled_secret_storage_hash_only_and_persistent_rate_limit() {
        use std::{
            io::{Read, Write},
            net::TcpListener,
        };
        let (_tmp, mut s, _) = super::super::tests::fixture();
        Arc::get_mut(&mut s).unwrap().vt_secrets = Arc::new(Fake::default());
        let hash = "a".repeat(64);
        assert_eq!(
            s.vt_lookup(hash.clone()).await.unwrap_err().code,
            "E_VT_DISABLED"
        );
        s.settings.write().unwrap().virus_total_enabled = true;
        assert_eq!(
            s.vt_lookup(hash.clone()).await.unwrap_err().code,
            "E_VT_KEY_REQUIRED"
        );
        s.vt_set_key(Some("secret-fixture-key".into())).unwrap();
        assert!(
            !serde_json::to_string(&*s.settings.read().unwrap())
                .unwrap()
                .contains("secret-fixture-key")
        );
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/files/", listener.local_addr().unwrap());
        let server = std::thread::spawn(move || {
            let (mut c, _) = listener.accept().unwrap();
            let mut b = [0; 8192];
            let mut n = 0;
            while n < b.len() && !b[..n].windows(4).any(|v| v == b"\r\n\r\n") {
                let got = c.read(&mut b[n..]).unwrap();
                if got == 0 {
                    break;
                }
                n += got;
            }
            let request = String::from_utf8_lossy(&b[..n]);
            assert!(request.starts_with(&format!("GET /files/{} ", "a".repeat(64))));
            assert!(
                request
                    .to_ascii_lowercase()
                    .contains("x-apikey: secret-fixture-key")
            );
            assert!(!request.contains("multipart"));
            c.write_all(b"HTTP/1.1 429 Too Many Requests\r\nRetry-After: 120\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").unwrap();
        });
        assert_eq!(
            s.vt_lookup_at(hash.clone(), &url).await.unwrap_err().code,
            "E_VT_RATE_LIMITED"
        );
        server.join().unwrap();
        assert!(s.vt_status().unwrap().retry_after_secs >= 119);
        assert_eq!(
            s.vt_lookup_at(hash, &url).await.unwrap_err().code,
            "E_VT_RATE_LIMITED"
        );
        s.vt_set_key(None).unwrap();
        assert!(!s.vt_status().unwrap().has_key);
        let bytes=serde_json::to_vec(&serde_json::json!({"data":{"id":"abc","attributes":{"last_analysis_stats":{"malicious":2,"undetected":4},"last_analysis_date":100}}})).unwrap();
        let result = parse("abc", &bytes).unwrap();
        assert_eq!(result.malicious, 2);
        assert_eq!(result.last_analysis_at, Some(100000));
        assert!(parse("wrong", &bytes).is_err());
    }
}
