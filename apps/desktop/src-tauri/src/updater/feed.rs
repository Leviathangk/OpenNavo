//! Bounded, namespace-aware appcast reader. No DTD, external entities, executable
//! feed expressions or delta payloads are accepted.
use super::{UpdateChannel, UpdateNotes, UpdateNotesFormat, version};
use crate::AppError;
use quick_xml::{NsReader, XmlVersion, events::Event, name::ResolveResult};
use serde::{Deserialize, Serialize};
use std::{cmp::Ordering, collections::BTreeMap};

const SPARKLE: &str = "http://www.andymatuschak.org/xml-namespaces/sparkle";
pub const MAX_FEED: usize = 2 * 1024 * 1024;
fn error(message: &str) -> AppError {
    AppError::new("E_UPDATE_FEED", message)
}
pub fn https(value: &str) -> Result<String, AppError> {
    let url = reqwest::Url::parse(value).map_err(|_| error("invalid_update_url"))?;
    if url.scheme() != "https"
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || value.len() > 8192
    {
        return Err(error("update_url_requires_https"));
    }
    Ok(url.into())
}
#[derive(Default)]
struct Node {
    name: String,
    attrs: BTreeMap<String, String>,
    text: String,
    children: Vec<Node>,
    content_start: usize,
}
impl Node {
    fn child(&self, name: &str) -> Option<&Self> {
        self.children.iter().find(|n| n.name == name)
    }
    fn text(&self, name: &str) -> Option<String> {
        self.child(name)
            .map(|n| n.text.trim().to_owned())
            .filter(|s| !s.is_empty())
    }
}
fn qualified(namespace: ResolveResult<'_>, local: &str) -> String {
    let name = local;
    match namespace {
        ResolveResult::Unbound => name.to_owned(),
        ResolveResult::Bound(ns) if ns.as_ref() == SPARKLE => format!("sparkle:{name}"),
        _ => format!("unknown:{name}"),
    }
}
fn tree(data: &[u8]) -> Result<Node, AppError> {
    if data.len() > MAX_FEED {
        return Err(error("appcast_size"));
    }
    let mut reader = NsReader::from_reader(data);
    let mut stack = vec![Node::default()];
    let mut count = 0;
    loop {
        let event_start = reader.buffer_position() as usize;
        match reader
            .read_event()
            .map_err(|_| error("invalid_appcast_xml"))?
        {
            event @ (Event::Start(_) | Event::Empty(_)) => {
                let empty = matches!(&event, Event::Empty(_));
                let element = match event {
                    Event::Start(e) | Event::Empty(e) => e,
                    _ => unreachable!(),
                };
                count += 1;
                if count > 20000 || stack.len() > 24 {
                    return Err(error("appcast_complexity"));
                }
                let (ns, local) = reader.resolver().resolve_element(element.name());
                let mut node = Node {
                    name: qualified(ns, local.as_ref()),
                    content_start: reader.buffer_position() as usize,
                    ..Default::default()
                };
                for attr in element.attributes() {
                    let attr = attr.map_err(|_| error("invalid_appcast_attribute"))?;
                    let (ns, local) = reader.resolver().resolve_attribute(attr.key);
                    node.attrs.insert(
                        qualified(ns, local.as_ref()),
                        attr.normalized_value(XmlVersion::Implicit1_0)
                            .map_err(|_| error("invalid_appcast_attribute"))?
                            .into_owned(),
                    );
                }
                if empty {
                    stack.last_mut().unwrap().children.push(node);
                } else {
                    stack.push(node);
                }
            }
            Event::End(_) => {
                if stack.len() < 2 {
                    return Err(error("unbalanced_appcast"));
                }
                let mut node = stack.pop().unwrap();
                if node.name == "description" && !node.children.is_empty() {
                    node.text = String::from_utf8(data[node.content_start..event_start].to_vec())
                        .map_err(|_| error("appcast_description_encoding"))?;
                }
                stack.last_mut().unwrap().children.push(node);
            }
            Event::Text(t) => stack
                .last_mut()
                .unwrap()
                .text
                .push_str(&t.xml_content(XmlVersion::Implicit1_0)),
            Event::CData(t) => stack
                .last_mut()
                .unwrap()
                .text
                .push_str(&t.xml_content(XmlVersion::Implicit1_0)),
            Event::GeneralRef(r) => {
                let text = if let Some(c) = r
                    .resolve_char_ref()
                    .map_err(|_| error("invalid_appcast_entity"))?
                {
                    c.to_string()
                } else {
                    quick_xml::escape::resolve_predefined_entity(r.as_ref())
                        .ok_or_else(|| error("unknown_appcast_entity"))?
                        .into()
                };
                stack.last_mut().unwrap().text.push_str(&text);
            }
            Event::DocType(_) => return Err(error("appcast_dtd_forbidden")),
            Event::Eof => break,
            _ => {}
        }
    }
    if stack.len() != 1 {
        return Err(error("unbalanced_appcast"));
    }
    let mut root = stack.pop().unwrap();
    if root.children.len() != 1 || root.children[0].name != "rss" {
        return Err(error("appcast_rss_required"));
    }
    Ok(root.children.remove(0))
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Item {
    pub version: String,
    pub short_version: String,
    pub channel: Option<String>,
    pub minimum_os: Option<String>,
    pub maximum_os: Option<String>,
    pub architectures: Vec<String>,
    pub url: Option<String>,
    pub length: Option<u64>,
    pub ed_signature: Option<String>,
    #[serde(default)]
    pub package: bool,
    pub notes: Option<UpdateNotes>,
    pub link: Option<String>,
    pub informational: bool,
    pub informational_versions: Vec<String>,
    pub informational_below: Option<String>,
    pub critical: bool,
    pub critical_below: Option<String>,
    pub minimum_auto_version: Option<String>,
    pub published_at: Option<i64>,
    pub rollout_interval: u64,
}
impl Item {
    pub fn informational_for(&self, installed: &str) -> bool {
        self.url.is_none()
            || (self.informational
                && ((self.informational_versions.is_empty() && self.informational_below.is_none())
                    || self
                        .informational_versions
                        .iter()
                        .any(|v| version::compare(v, installed) == Ordering::Equal)
                    || self
                        .informational_below
                        .as_ref()
                        .is_some_and(|v| version::compare(installed, v) == Ordering::Less)))
    }
    pub fn critical_for(&self, installed: &str) -> bool {
        self.critical
            && self
                .critical_below
                .as_ref()
                .is_none_or(|v| version::compare(installed, v) == Ordering::Less)
    }
}
pub fn parse(data: &[u8]) -> Result<Vec<Item>, AppError> {
    let root = tree(data)?;
    let channel = root
        .child("channel")
        .ok_or_else(|| error("appcast_channel_required"))?;
    let mut out = vec![];
    for node in channel.children.iter().filter(|n| n.name == "item") {
        let enclosures: Vec<_> = node
            .children
            .iter()
            .filter(|n| n.name == "enclosure")
            .collect();
        let sources: Vec<_> = if enclosures.is_empty() {
            vec![None]
        } else {
            enclosures.into_iter().map(Some).collect()
        };
        for enclosure in sources {
            let attr = |key: &str| enclosure.and_then(|e| e.attrs.get(key)).cloned();
            if attr("sparkle:deltaFrom").is_some()
                || attr("sparkle:os").is_some_and(|s| s != "macos")
            {
                continue;
            }
            let Some(build) = node
                .text("sparkle:version")
                .or_else(|| attr("sparkle:version"))
            else {
                continue;
            };
            if build.len() > 256 {
                return Err(error("appcast_version_size"));
            }
            let info = node.child("sparkle:informationalUpdate");
            let critical = node.child("sparkle:criticalUpdate").or_else(|| {
                node.child("sparkle:tags")
                    .and_then(|t| t.child("sparkle:criticalUpdate"))
            });
            let notes = if let Some(link) = node.text("sparkle:releaseNotesLink") {
                Some(UpdateNotes::Link { url: https(&link)? })
            } else {
                node.text("description").map(|body| UpdateNotes::Inline {
                    format: if node
                        .child("description")
                        .and_then(|n| n.attrs.get("type"))
                        .is_some_and(|t| t == "text/plain")
                    {
                        UpdateNotesFormat::Text
                    } else {
                        UpdateNotesFormat::Html
                    },
                    body,
                })
            };
            let arch = node
                .text("sparkle:hardwareRequirements")
                .or_else(|| attr("sparkle:arch"))
                .unwrap_or_default();
            let item = Item {
                short_version: node
                    .text("sparkle:shortVersionString")
                    .or_else(|| attr("sparkle:shortVersionString"))
                    .unwrap_or_else(|| build.clone()),
                version: build,
                channel: node.text("sparkle:channel"),
                minimum_os: node
                    .text("sparkle:minimumSystemVersion")
                    .or_else(|| attr("sparkle:minimumSystemVersion")),
                maximum_os: node.text("sparkle:maximumSystemVersion"),
                architectures: arch
                    .split([',', ' '])
                    .filter(|s| !s.is_empty())
                    .map(str::to_owned)
                    .collect(),
                url: attr("url").map(|v| https(&v)).transpose()?,
                length: attr("length")
                    .map(|v| v.parse::<u64>().map_err(|_| error("appcast_length")))
                    .transpose()?,
                ed_signature: attr("sparkle:edSignature"),
                package: attr("sparkle:installationType").as_deref() == Some("package")
                    || attr("url")
                        .and_then(|u| reqwest::Url::parse(&u).ok())
                        .is_some_and(|u| u.path().ends_with(".pkg") || u.path().ends_with(".mpkg")),
                notes,
                link: node.text("link").map(|v| https(&v)).transpose()?,
                informational: info.is_some(),
                informational_versions: info
                    .map(|n| {
                        n.children
                            .iter()
                            .filter(|n| n.name == "sparkle:version")
                            .map(|n| n.text.trim().into())
                            .collect()
                    })
                    .unwrap_or_default(),
                informational_below: info.and_then(|n| n.text("sparkle:belowVersion")),
                critical: critical.is_some(),
                critical_below: critical
                    .and_then(|n| n.attrs.get("sparkle:version"))
                    .cloned(),
                minimum_auto_version: node.text("sparkle:minimumAutoupdateVersion"),
                published_at: node
                    .text("pubDate")
                    .and_then(|s| chrono::DateTime::parse_from_rfc2822(&s).ok())
                    .map(|d| d.timestamp()),
                rollout_interval: node
                    .text("sparkle:phasedRolloutInterval")
                    .map(|v| v.parse::<u64>().map_err(|_| error("appcast_rollout")))
                    .transpose()?
                    .unwrap_or(0),
            };
            if (item.informational || item.url.is_none()) && item.link.is_none() {
                return Err(error("informational_link_missing"));
            }
            out.push(item);
        }
    }
    Ok(out)
}
pub struct Selection<'a> {
    pub installed: &'a str,
    pub os: &'a str,
    pub arch: &'a str,
    pub channel: UpdateChannel,
    pub skipped: Option<&'a str>,
    pub manual: bool,
    pub now: i64,
    pub rollout_group: u8,
}
pub fn select<'a>(items: &'a [Item], s: &Selection<'_>) -> Option<&'a Item> {
    select_inner(items, s, false)
}
pub fn select_for_reinstall<'a>(items: &'a [Item], s: &Selection<'_>) -> Option<&'a Item> {
    select_inner(items, s, true)
}
fn select_inner<'a>(
    items: &'a [Item],
    s: &Selection<'_>,
    include_current: bool,
) -> Option<&'a Item> {
    items
        .iter()
        .filter(|i| {
            (include_current || version::compare(&i.version, s.installed) == Ordering::Greater)
                && i.channel.as_ref().is_none_or(|c| {
                    c.is_empty() || (s.channel == UpdateChannel::Beta && c == "beta")
                })
                && i.minimum_os
                    .as_ref()
                    .is_none_or(|v| version::compare(s.os, v) != Ordering::Less)
                && i.maximum_os
                    .as_ref()
                    .is_none_or(|v| version::compare(s.os, v) != Ordering::Greater)
                && (i.architectures.is_empty()
                    || i.architectures
                        .iter()
                        .any(|a| a == s.arch || a == "universal"))
                && (include_current
                    || s.skipped
                        .is_none_or(|v| v != i.version && v != i.short_version))
                && (s.manual
                    || i.critical_for(s.installed)
                    || i.rollout_interval == 0
                    || i.published_at.is_some_and(|published| {
                        s.now.saturating_sub(published)
                            >= i.rollout_interval
                                .saturating_mul(u64::from(s.rollout_group.min(6)))
                                .min(i64::MAX as u64) as i64
                    }))
        })
        .max_by(|a, b| version::compare(&a.version, &b.version))
}
