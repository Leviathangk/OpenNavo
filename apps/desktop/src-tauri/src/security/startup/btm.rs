// Copyright (c) 2023 Objective-See. All rights reserved.
// 移植自 KnockKnock，Objective-See, LLC，GPL-3.0。
// Modified: bounded Rust v16/v18 archive graph traversal; see UPSTREAM.md.
//! Bounded NSKeyedArchiver reader. No unarchiver, sfltool or BTM daemon calls.
use crate::{AppError, security::BtmFormat};
use plist::{Dictionary, Value};
use std::collections::{BTreeMap, BTreeSet};
use std::path::PathBuf;
pub struct Record {
    pub name: String,
    pub identifier: Option<String>,
    pub path: String,
    pub disposition: u64,
    /// Item type bits: 2 app, 4 login item, 8 launch agent, 16 launch daemon.
    pub kind: u64,
    /// Property list a launch agent or daemon was registered from.
    pub plist: Option<String>,
    /// App macOS lists this item under in Login Items: the item itself, its container, or an associated app.
    pub app: Option<String>,
    /// AssociatedBundleIdentifiers, for apps this archive has no record of.
    pub bundle_ids: Vec<String>,
}
impl Record {
    /// A job ServiceManagement registered from inside an app: a login item helper, or an agent
    /// or daemon whose plist ships in the app bundle. launchctl switches these by label.
    pub fn service_managed(&self) -> bool {
        if self.kind & 4 != 0 {
            return self.plist.is_none();
        }
        self.kind & (8 | 16) != 0
            && self
                .plist
                .as_deref()
                .zip(self.app.as_deref())
                .is_some_and(|(plist, app)| std::path::Path::new(plist).starts_with(app))
    }
    /// The app itself opening at login, as System Settings lists it under Open at Login.
    pub fn opens_at_login(&self) -> bool {
        self.kind & 2 != 0
            && self.kind & (4 | 8 | 16) == 0
            && self.path.trim_end_matches('/').ends_with(".app")
    }
}
fn file_path(url: String) -> Option<String> {
    let path = if url.starts_with("file:") {
        reqwest::Url::parse(&url)
            .ok()?
            .to_file_path()
            .ok()?
            .to_string_lossy()
            .into_owned()
    } else {
        url
    };
    path.starts_with('/').then_some(path)
}
/// Paths inside a bundle are stored relative to it ("Contents/…"); never let one climb out.
fn inside(base: Option<&PathBuf>, relative: &str) -> Option<String> {
    if !relative.starts_with("Contents/") || relative.split('/').any(|s| s == "..") {
        return None;
    }
    Some(base?.join(relative).to_string_lossy().into_owned())
}
pub struct Archive {
    objects: Vec<Value>,
    top: Dictionary,
}
fn invalid() -> AppError {
    AppError::new("E_FULL_DISK_ACCESS", "btm_archive_unsupported")
}
impl Archive {
    pub fn parse(bytes: &[u8]) -> Result<Self, AppError> {
        let value: Value = plist::from_bytes(bytes).map_err(|_| invalid())?;
        let d = value.as_dictionary().ok_or_else(invalid)?;
        if d.get("$archiver").and_then(Value::as_string) != Some("NSKeyedArchiver") {
            return Err(invalid());
        }
        let objects = d
            .get("$objects")
            .and_then(Value::as_array)
            .ok_or_else(invalid)?
            .clone();
        if objects.len() > 200_000 {
            return Err(invalid());
        }
        Ok(Self {
            objects,
            top: d
                .get("$top")
                .and_then(Value::as_dictionary)
                .ok_or_else(invalid)?
                .clone(),
        })
    }
    fn resolve<'a>(&'a self, v: &'a Value) -> Result<&'a Value, AppError> {
        match v.as_uid() {
            Some(id) => self.objects.get(id.get() as usize).ok_or_else(invalid),
            None => Ok(v),
        }
    }
    fn class<'a>(&'a self, v: &'a Value) -> Option<&'a str> {
        self.resolve(v.as_dictionary()?.get("$class")?)
            .ok()?
            .as_dictionary()?
            .get("$classname")?
            .as_string()
    }
    fn string(&self, v: &Value, depth: usize) -> Option<String> {
        if depth > 32 {
            return None;
        }
        let v = self.resolve(v).ok()?;
        if let Some(s) = v.as_string() {
            return (s != "$null").then(|| s.to_owned());
        }
        let d = v.as_dictionary()?;
        if let Some(relative) = d.get("NS.relative").and_then(|v| self.string(v, depth + 1)) {
            if let Some(base) = d.get("NS.base").and_then(|v| self.string(v, depth + 1)) {
                return reqwest::Url::parse(&base)
                    .ok()?
                    .join(&relative)
                    .ok()
                    .map(|u| u.to_string());
            }
            return Some(relative);
        }
        for k in ["NS.relative", "NS.string", "path", "URLString"] {
            if let Some(v) = d.get(k)
                && let Some(s) = self.string(v, depth + 1)
            {
                return Some(s);
            }
        }
        None
    }
    /// An NSArray of strings, or a single string.
    fn strings(&self, v: &Value) -> Vec<String> {
        if let Some(s) = self.string(v, 0) {
            return vec![s];
        }
        self.resolve(v)
            .ok()
            .and_then(Value::as_dictionary)
            .and_then(|d| d.get("NS.objects"))
            .and_then(Value::as_array)
            .map(|a| a.iter().filter_map(|v| self.string(v, 0)).collect())
            .unwrap_or_default()
    }
    fn visit(
        &self,
        v: &Value,
        seen: &mut BTreeSet<u64>,
        records: &mut Vec<usize>,
        depth: usize,
    ) -> Result<(), AppError> {
        if depth > 128 {
            return Err(invalid());
        }
        if let Some(id) = v.as_uid() {
            if !seen.insert(id.get()) {
                return Ok(());
            }
            let item = self.resolve(v)?;
            if self.class(item) == Some("ItemRecord") {
                records.push(id.get() as usize);
            }
            return self.visit(item, seen, records, depth + 1);
        }
        match v {
            Value::Array(a) => {
                for v in a {
                    self.visit(v, seen, records, depth + 1)?;
                }
            }
            Value::Dictionary(d) => {
                for (k, v) in d {
                    if k != "$class" {
                        self.visit(v, seen, records, depth + 1)?;
                    }
                }
            }
            _ => {}
        }
        Ok(())
    }
    pub fn records(&self) -> Result<(BtmFormat, Vec<Record>), AppError> {
        let (format, root) = if let Some(root) = self.top.get("userStore") {
            if self.class(self.resolve(root)?) != Some("BTMUserStore") {
                return Err(invalid());
            }
            (BtmFormat::V18, root)
        } else if let Some(root) = self.top.get("store") {
            if self.top.get("version").and_then(Value::as_unsigned_integer) != Some(16)
                || self.class(self.resolve(root)?) != Some("Storage")
            {
                return Err(invalid());
            }
            (BtmFormat::V16, root)
        } else if self.top.get("version").and_then(Value::as_unsigned_integer) == Some(18)
            && self.top.contains_key("infoByUserIdentifier")
        {
            return Ok((BtmFormat::V18, vec![]));
        } else {
            return Err(invalid());
        };
        let mut ids = vec![];
        self.visit(root, &mut BTreeSet::new(), &mut ids, 0)?;
        let kind_of = |d: &Dictionary| {
            d.get("type")
                .and_then(|v| self.resolve(v).ok()?.as_unsigned_integer())
                .unwrap_or(0)
        };
        // Every app record, parents with embedded items included: what containers and
        // associated bundle identifiers point to.
        let (mut by_identifier, mut by_bundle) = (BTreeMap::new(), BTreeMap::new());
        for &id in &ids {
            let Some(d) = self.objects[id].as_dictionary() else {
                continue;
            };
            let get = |k: &str| d.get(k).and_then(|v| self.string(v, 0));
            let Some(url) = get("url").and_then(file_path) else {
                continue;
            };
            let url = PathBuf::from(url.trim_end_matches('/'));
            if kind_of(d) & 2 == 0 || url.extension().is_none_or(|e| e != "app") {
                continue;
            }
            if let Some(bundle) = get("bundleIdentifier") {
                by_bundle.insert(bundle, url.clone());
            }
            if let Some(identifier) = get("identifier") {
                by_identifier.insert(identifier, url);
            }
        }
        let mut records = vec![];
        for &id in &ids {
            let d = self.objects[id].as_dictionary().ok_or_else(invalid)?;
            let get = |k: &str| d.get(k).and_then(|v| self.string(v, 0));
            let kind = kind_of(d);
            // Leaf application registrations can themselves persist; only developer
            // containers/metadata and parents with embedded items are excluded.
            if kind & (2 | 4 | 8 | 16) == 0 {
                continue;
            }
            let embedded = d
                .get("items")
                .and_then(|v| self.resolve(v).ok())
                .and_then(Value::as_dictionary)
                .and_then(|d| d.get("NS.objects"))
                .and_then(Value::as_array)
                .is_some_and(|v| !v.is_empty());
            if embedded {
                continue;
            }
            let container = get("container").and_then(|c| by_identifier.get(&c));
            // Items registered from inside an app store their URL and executable relative to it.
            let embedded_url = (|| {
                let url = self.resolve(d.get("url")?).ok()?.as_dictionary()?;
                inside(container, &self.string(url.get("NS.relative")?, 0)?)
            })();
            let executable = get("executablePath").and_then(|p| {
                if p.starts_with('/') || p.starts_with("file:") {
                    Some(p)
                } else {
                    inside(container, &p)
                }
            });
            let url = embedded_url
                .clone()
                .or_else(|| get("url").and_then(file_path));
            let path = executable
                .or(embedded_url)
                .or_else(|| get("url"))
                .or_else(|| get("bundleURL"));
            let Some(path) = path.and_then(file_path) else {
                continue;
            };
            let bundle_ids = d
                .get("associatedBundleIdentifiers")
                .map(|v| self.strings(v))
                .unwrap_or_default();
            let app = if kind & 2 != 0 {
                url.as_deref()
                    .map(|u| u.trim_end_matches('/'))
                    .filter(|u| u.ends_with(".app"))
                    .map(str::to_owned)
            } else {
                container
                    .or_else(|| bundle_ids.iter().find_map(|b| by_bundle.get(b)))
                    .map(|p| p.to_string_lossy().into_owned())
            };
            records.push(Record {
                name: get("name").unwrap_or_default(),
                identifier: get("identifier"),
                path,
                disposition: d
                    .get("disposition")
                    .and_then(|v| self.resolve(v).ok()?.as_unsigned_integer())
                    .unwrap_or(0),
                kind,
                plist: url.filter(|_| kind & (8 | 16) != 0),
                app,
                bundle_ids,
            });
        }
        Ok((format, records))
    }
}
/// A v18 user store holding these ItemRecords, each given as its key/value pairs.
#[cfg(test)]
pub(crate) fn test_archive(records: Vec<Vec<(&str, Value)>>) -> Vec<u8> {
    let uid = |i: u64| Value::Uid(plist::Uid::new(i));
    let dict = |pairs: Vec<(&str, Value)>| {
        Value::Dictionary(pairs.into_iter().map(|(k, v)| (k.to_owned(), v)).collect())
    };
    let count = records.len() as u64;
    let mut objects = vec![
        Value::String("$null".into()),
        dict(vec![
            ("$class", uid(2)),
            (
                "items",
                Value::Array((0..count).map(|i| uid(4 + i)).collect()),
            ),
        ]),
        dict(vec![("$classname", Value::String("BTMUserStore".into()))]),
        dict(vec![("$classname", Value::String("ItemRecord".into()))]),
    ];
    for mut record in records {
        record.push(("$class", uid(3)));
        objects.push(dict(record));
    }
    let mut top = Dictionary::new();
    top.insert("userStore".into(), uid(1));
    top.insert("version".into(), Value::Integer(18.into()));
    let root = dict(vec![
        ("$archiver", Value::String("NSKeyedArchiver".into())),
        ("$objects", Value::Array(objects)),
        ("$top", Value::Dictionary(top)),
    ]);
    let mut bytes = vec![];
    root.to_writer_binary(&mut bytes).unwrap();
    bytes
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn records_name_their_app_by_container_or_associated_bundle_identifier() {
        let text = |s: &str| Value::String(s.into());
        let number = |n: u64| Value::Integer(n.into());
        let array = |items: &[&str]| {
            Value::Dictionary(
                [(
                    "NS.objects".to_owned(),
                    Value::Array(items.iter().map(|s| text(s)).collect()),
                )]
                .into_iter()
                .collect(),
            )
        };
        let bytes = test_archive(vec![
            vec![
                ("type", number(2)),
                ("identifier", text("2.com.example.app")),
                ("bundleIdentifier", text("com.example.app")),
                ("url", text("file:///Applications/Example%20App.app/")),
                ("name", text("Example")),
            ],
            // Grouped under a developer, not the app: the plist's associated app decides.
            vec![
                ("type", number(65552)),
                ("identifier", text("16.com.example.daemon")),
                ("container", text("Example Developer")),
                ("associatedBundleIdentifiers", array(&["com.example.app"])),
                (
                    "executablePath",
                    text("/Library/PrivilegedHelperTools/com.example.daemon"),
                ),
                (
                    "url",
                    text("file:///Library/LaunchDaemons/com.example.daemon.plist"),
                ),
                ("disposition", number(11)),
            ],
            // Registered from inside the app: plist and program are relative to it.
            vec![
                ("type", number(16)),
                ("identifier", text("16.com.example.helper")),
                ("container", text("2.com.example.app")),
                ("executablePath", text("Contents/MacOS/Helper")),
                (
                    "url",
                    Value::Dictionary(
                        [
                            ("NS.base".to_owned(), text("file:///")),
                            (
                                "NS.relative".to_owned(),
                                text("Contents/Library/LaunchDaemons/com.example.helper.plist"),
                            ),
                        ]
                        .into_iter()
                        .collect(),
                    ),
                ),
            ],
            vec![
                ("type", number(8)),
                ("identifier", text("8.org.unknown.agent")),
                ("container", text("Unknown Developer")),
                ("associatedBundleIdentifiers", array(&["org.unknown.app"])),
                ("executablePath", text("/opt/tool/bin/agent")),
                (
                    "url",
                    text("file:///Users/me/Library/LaunchAgents/org.unknown.agent.plist"),
                ),
            ],
        ]);
        let (_, rows) = Archive::parse(&bytes).unwrap().records().unwrap();
        let row = |id: &str| {
            rows.iter()
                .find(|r| r.identifier.as_deref() == Some(id))
                .unwrap()
        };
        let app = Some("/Applications/Example App.app".to_owned());
        assert_eq!(row("2.com.example.app").app, app);
        assert_eq!(row("2.com.example.app").plist, None);
        let daemon = row("16.com.example.daemon");
        assert_eq!(daemon.app, app);
        assert_eq!(
            daemon.path,
            "/Library/PrivilegedHelperTools/com.example.daemon"
        );
        assert_eq!(
            daemon.plist.as_deref(),
            Some("/Library/LaunchDaemons/com.example.daemon.plist")
        );
        let helper = row("16.com.example.helper");
        assert_eq!(helper.app, app);
        assert_eq!(
            helper.path,
            "/Applications/Example App.app/Contents/MacOS/Helper"
        );
        assert_eq!(
            helper.plist.as_deref(),
            Some(
                "/Applications/Example App.app/Contents/Library/LaunchDaemons/com.example.helper.plist"
            )
        );
        // An app this store does not know is left for LaunchServices.
        let agent = row("8.org.unknown.agent");
        assert_eq!(agent.app, None);
        assert_eq!(agent.bundle_ids, vec!["org.unknown.app".to_owned()]);
    }
    fn fixture(v: u32) -> Vec<u8> {
        fixture_with(v, "Fixture", 1)
    }
    fn fixture_with(v: u32, name: &str, disposition: u64) -> Vec<u8> {
        let uid = |i| Value::Uid(plist::Uid::new(i));
        let mut top = Dictionary::new();
        top.insert(if v == 16 { "store" } else { "userStore" }.into(), uid(1));
        top.insert("version".into(), Value::Integer(v.into()));
        let dict = |pairs: Vec<(&str, Value)>| {
            Value::Dictionary(pairs.into_iter().map(|(k, v)| (k.to_owned(), v)).collect())
        };
        let objs = vec![
            Value::String("$null".into()),
            dict(vec![
                ("$class", uid(2)),
                ("items", Value::Array(vec![uid(3), uid(1)])),
            ]),
            dict(vec![(
                "$classname",
                Value::String(if v == 16 { "Storage" } else { "BTMUserStore" }.into()),
            )]),
            dict(vec![
                ("$class", uid(4)),
                ("name", Value::String(name.into())),
                (
                    "url",
                    Value::String("file:///Applications/Fixture.app".into()),
                ),
                ("type", Value::Integer(4.into())),
                ("disposition", Value::Integer(disposition.into())),
            ]),
            dict(vec![("$classname", Value::String("ItemRecord".into()))]),
        ];
        let root = dict(vec![
            ("$archiver", Value::String("NSKeyedArchiver".into())),
            ("$objects", Value::Array(objs)),
            ("$top", Value::Dictionary(top)),
        ]);
        let mut bytes = vec![];
        root.to_writer_binary(&mut bytes).unwrap();
        bytes
    }
    #[test]
    fn both_formats_follow_reachable_records_and_bound_cycles() {
        for (v, f) in [(16, BtmFormat::V16), (18, BtmFormat::V18)] {
            let a = Archive::parse(&fixture(v)).unwrap();
            let (format, rows) = a.records().unwrap();
            assert_eq!(format, f);
            assert_eq!(rows.len(), 1);
            assert_eq!(rows[0].path, "/Applications/Fixture.app");
        }
    }
    #[test]
    fn fingerprint_ignores_bookkeeping_rewrites_but_not_item_changes() {
        let dir = tempfile::tempdir().unwrap();
        let store = dir
            .path()
            .canonicalize()
            .unwrap()
            .join("BackgroundItems-v18.btm");
        let print = |bytes: Vec<u8>| {
            std::fs::write(&store, bytes).unwrap();
            crate::security::startup::btm_fingerprint(&store, None)
        };
        let enabled = print(fixture_with(18, "Fixture", 1)).unwrap();
        assert_eq!(print(fixture_with(18, "Fixture", 1)), Some(enabled));
        // Other disposition bits are BTM's own bookkeeping; the scan reads only bit 0.
        assert_eq!(print(fixture_with(18, "Fixture", 0b1011)), Some(enabled));
        assert_ne!(print(fixture_with(18, "Fixture", 0)), Some(enabled));
        assert_ne!(print(fixture_with(18, "Renamed", 1)), Some(enabled));
        assert_eq!(print(b"not an archive".to_vec()), None);
    }
    #[test]
    fn bad_reference_fails_closed() {
        let mut a = Archive::parse(&fixture(18)).unwrap();
        a.top
            .insert("userStore".into(), Value::Uid(plist::Uid::new(900_000)));
        assert!(a.records().is_err());
    }
    #[test]
    fn relative_login_url_and_leaf_app_are_preserved_but_parent_is_not() {
        let mut a = Archive::parse(&fixture(18)).unwrap();
        let record = a.objects[3].as_dictionary_mut().unwrap();
        record.insert(
            "url".into(),
            Value::Dictionary(
                [
                    (
                        "NS.base".to_owned(),
                        Value::String("file:///Applications/Fixture.app/".into()),
                    ),
                    (
                        "NS.relative".to_owned(),
                        Value::String("Contents/Library/LoginItems/Helper.app".into()),
                    ),
                ]
                .into_iter()
                .collect(),
            ),
        );
        assert_eq!(
            a.records().unwrap().1[0].path,
            "/Applications/Fixture.app/Contents/Library/LoginItems/Helper.app"
        );
        a.objects[3]
            .as_dictionary_mut()
            .unwrap()
            .insert("type".into(), Value::Integer(2.into()));
        assert_eq!(a.records().unwrap().1.len(), 1);
        a.objects[3].as_dictionary_mut().unwrap().insert(
            "items".into(),
            Value::Dictionary(
                [(
                    "NS.objects".to_owned(),
                    Value::Array(vec![Value::Uid(plist::Uid::new(1))]),
                )]
                .into_iter()
                .collect(),
            ),
        );
        assert!(a.records().unwrap().1.is_empty());
    }
}
