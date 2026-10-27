use serde::{Deserialize, Serialize};

/// Error type for all commands (06 §5.1): `code` is a 06 §5.4 client error code used to select UI copy.
#[derive(Debug, Clone, Serialize, Deserialize, specta::Type, thiserror::Error, PartialEq)]
#[serde(rename_all = "camelCase")]
#[error("{code}: {message}")]
pub struct AppError {
    pub code: String,
    pub message: String,
    pub detail: Option<String>,
}

impl From<std::io::Error> for AppError {
    fn from(error: std::io::Error) -> Self {
        Self::new("E_UNKNOWN", "io").with_detail(error.to_string())
    }
}
impl From<rusqlite::Error> for AppError {
    fn from(error: rusqlite::Error) -> Self {
        Self::new("E_UNKNOWN", "database").with_detail(error.to_string())
    }
}
impl From<serde_json::Error> for AppError {
    fn from(error: serde_json::Error) -> Self {
        Self::new("E_UNKNOWN", "json").with_detail(error.to_string())
    }
}
impl From<reqwest::Error> for AppError {
    fn from(error: reqwest::Error) -> Self {
        Self::new("E_NETWORK", "http").with_detail(error.to_string())
    }
}

impl AppError {
    pub fn new(code: &str, message: impl Into<String>) -> Self {
        Self {
            code: code.to_owned(),
            message: message.into(),
            detail: None,
        }
    }

    pub fn with_detail(mut self, detail: impl Into<String>) -> Self {
        self.detail = Some(detail.into());
        self
    }

    /// Add user-facing context without discarding the underlying command failure.
    pub fn context(self, code: &str, message: &str) -> Self {
        Self::new(code, message)
            .with_detail(serde_json::to_string(&self).unwrap_or_else(|_| self.to_string()))
    }
}

#[cfg(test)]
mod tests {
    use super::AppError;

    #[test]
    fn serializes_camel_case_with_optional_detail() {
        let error =
            AppError::new("E_BREW_NOT_FOUND", "找不到 brew").with_detail("/opt/homebrew/bin/brew");
        let json = serde_json::to_value(&error).expect("serializable");
        assert_eq!(json["code"], "E_BREW_NOT_FOUND");
        assert_eq!(json["message"], "找不到 brew");
        assert_eq!(json["detail"], "/opt/homebrew/bin/brew");
        assert_eq!(error.to_string(), "E_BREW_NOT_FOUND: 找不到 brew");
    }
}
