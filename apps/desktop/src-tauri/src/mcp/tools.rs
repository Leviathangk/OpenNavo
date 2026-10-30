//! MCP tool catalogue.
//!
//! B1 exposes no tools. Read tools are added in B2 when the app-side broker
//! exists, so `tools/list` is deliberately empty here.

use serde_json::{Value, json};

/// Tool definitions advertised through `tools/list`.
pub fn definitions() -> Vec<Value> {
    Vec::new()
}

/// Build the `tools/list` result object.
pub fn list_result() -> Value {
    json!({ "tools": definitions() })
}
