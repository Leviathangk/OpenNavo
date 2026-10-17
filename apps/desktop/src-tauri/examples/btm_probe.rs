//! M0 archive-format probe, authored from local plist structure only.
//! No Objective-See source, NSKeyedUnarchiver, sfltool, daemon API or writes.
//! This is NOT the product startup scanner (which is pending D1).
use plist::{Dictionary, Value};
use serde_json::json;
use std::{collections::BTreeSet, path::Path};

struct Archive {
    objects: Vec<Value>,
    top: Dictionary,
}

impl Archive {
    fn parse(value: Value) -> Result<Self, String> {
        let dict = value.as_dictionary().ok_or("archive dictionary")?;
        if dict.get("$archiver").and_then(Value::as_string) != Some("NSKeyedArchiver") {
            return Err("unknown archive type".into());
        }
        let objects = dict
            .get("$objects")
            .and_then(Value::as_array)
            .ok_or("objects")?
            .clone();
        let top = dict
            .get("$top")
            .and_then(Value::as_dictionary)
            .ok_or("top")?
            .clone();
        if objects.len() > 200_000 {
            return Err("object limit".into());
        }
        Ok(Self { objects, top })
    }

    fn resolve<'a>(&'a self, value: &'a Value) -> Result<&'a Value, String> {
        match value.as_uid() {
            Some(uid) => self
                .objects
                .get(usize::try_from(uid.get()).map_err(|_| "UID overflow")?)
                .ok_or_else(|| "UID out of range".into()),
            None => Ok(value),
        }
    }

    fn class<'a>(&'a self, value: &'a Value) -> Option<&'a str> {
        self.resolve(value.as_dictionary()?.get("$class")?)
            .ok()?
            .as_dictionary()?
            .get("$classname")?
            .as_string()
    }

    fn visit(
        &self,
        value: &Value,
        visited: &mut BTreeSet<u64>,
        records: &mut BTreeSet<u64>,
        depth: usize,
    ) -> Result<(), String> {
        if depth > 128 {
            return Err("archive depth limit".into());
        }
        if let Some(uid) = value.as_uid() {
            if !visited.insert(uid.get()) {
                return Ok(());
            }
            let value = self.resolve(value)?;
            if self.class(value) == Some("ItemRecord") {
                records.insert(uid.get());
            }
            return self.visit(value, visited, records, depth + 1);
        }
        match value {
            Value::Array(values) => {
                for item in values {
                    self.visit(item, visited, records, depth + 1)?;
                }
            }
            Value::Dictionary(dict) => {
                for (key, item) in dict {
                    if key != "$class" {
                        self.visit(item, visited, records, depth + 1)?;
                    }
                }
            }
            _ => {}
        }
        Ok(())
    }

    fn summary(&self) -> Result<serde_json::Value, String> {
        let (format, root) = if let Some(root) = self.top.get("userStore") {
            if self.class(self.resolve(root)?) != Some("BTMUserStore") {
                return Err("unrecognized userStore class".into());
            }
            ("v18", root)
        } else if let Some(root) = self.top.get("store") {
            if self.top.get("version").and_then(Value::as_unsigned_integer) != Some(16) {
                return Err("unknown legacy BTM version".into());
            }
            if self.class(self.resolve(root)?) != Some("Storage") {
                return Err("unrecognized store class".into());
            }
            ("v16", root)
        } else if self.top.contains_key("infoByUserIdentifier") {
            if self.top.get("version").and_then(Value::as_unsigned_integer) != Some(18) {
                return Err("unknown BTM index version".into());
            }
            return Ok(json!({"format": "v18-index", "records": 0}));
        } else {
            return Err("unknown BTM root; must be reported as missing".into());
        };
        let mut records = BTreeSet::new();
        self.visit(root, &mut BTreeSet::new(), &mut records, 0)?;
        let total = self
            .objects
            .iter()
            .filter(|value| self.class(value) == Some("ItemRecord"))
            .count();
        let mut missing_fields = 0;
        for index in &records {
            let record = self.objects[*index as usize]
                .as_dictionary()
                .ok_or("record dictionary")?;
            for field in ["uuid", "type", "disposition", "identifier", "name", "url"] {
                if !record.contains_key(field) {
                    missing_fields += 1;
                }
            }
        }
        Ok(json!({"format": format, "reachableRecords": records.len(),
            "archivedRecords": total, "unreachableRecords": total - records.len(),
            "missingFields": missing_fields, "objects": self.objects.len()}))
    }
}

fn probe(path: &Path) -> Result<serde_json::Value, String> {
    use std::os::unix::fs::OpenOptionsExt;
    let file = std::fs::OpenOptions::new()
        .read(true)
        .custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK)
        .open(path)
        .map_err(|e| e.to_string())?;
    let meta = file.metadata().map_err(|e| e.to_string())?;
    if !meta.is_file() || meta.len() > 64 * 1024 * 1024 {
        return Err("not a regular bounded archive".into());
    }
    Archive::parse(Value::from_reader(file).map_err(|e| e.to_string())?)?.summary()
}

fn main() {
    if std::env::args().skip(1).collect::<Vec<_>>() != ["--real-read-only"] {
        eprintln!("Usage: btm_probe --real-read-only (counts only; no personal paths)");
        std::process::exit(2);
    }
    let entries = match std::fs::read_dir("/private/var/db/com.apple.backgroundtaskmanagement") {
        Ok(entries) => entries,
        Err(error) => {
            println!("{}", json!({"missing": "btm", "error": error.to_string()}));
            std::process::exit(1);
        }
    };
    let mut results = Vec::new();
    for entry in entries.flatten() {
        if entry.path().extension().is_some_and(|ext| ext == "btm") {
            results.push(match probe(&entry.path()) {
                Ok(value) => value,
                Err(error) => json!({"missing": "btm", "error": error}),
            });
        }
    }
    results.sort_by_key(|value| value.to_string());
    println!("{}", json!({"readOnly": true, "archives": results}));
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn archive_rejects_unknown_root_and_invalid_reference() {
        let archive = Archive {
            objects: vec![],
            top: Dictionary::new(),
        };
        assert!(archive.summary().is_err());
        assert!(archive.resolve(&Value::Uid(plist::Uid::new(999))).is_err());
    }

    #[test]
    fn cycles_are_bounded() {
        let archive = Archive {
            objects: vec![Value::Array(vec![Value::Uid(plist::Uid::new(0))])],
            top: Dictionary::new(),
        };
        let mut visited = BTreeSet::new();
        archive
            .visit(
                &Value::Uid(plist::Uid::new(0)),
                &mut visited,
                &mut BTreeSet::new(),
                0,
            )
            .unwrap();
        assert_eq!(visited.len(), 1);
    }
}
