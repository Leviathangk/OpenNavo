use base64::{Engine, engine::general_purpose::STANDARD};
use opennavo_desktop_lib::updater::{
    UpdateChannel,
    feed::{self, Selection},
    gate::{self, Identity},
};
use ring::signature::{Ed25519KeyPair, KeyPair};
fn feed(items: &str) -> Vec<feed::Item> {
    feed::parse(format!(r#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>{items}</channel></rss>"#).as_bytes()).unwrap()
}
fn selection() -> Selection<'static> {
    Selection {
        installed: "100",
        os: "15.0",
        arch: "arm64",
        channel: UpdateChannel::Stable,
        skipped: None,
        manual: true,
        now: 0,
        rollout_group: 6,
    }
}
#[test]
fn selects_full_macos_channel_architecture_and_os_build_versions() {
    let items = feed(
        r#"
 <item><s:version>101</s:version><s:shortVersionString>2.0</s:shortVersionString><enclosure url="https://example.org/a.zip" s:os="macos"/></item>
 <item><s:version>102</s:version><s:channel>beta</s:channel><enclosure url="https://example.org/b.zip"/></item>
 <item><s:version>103</s:version><enclosure url="https://example.org/c.zip" s:os="windows"/></item>
 <item><s:version>104</s:version><enclosure url="https://example.org/d.zip" s:deltaFrom="100"/></item>
 <item><s:version>105</s:version><s:minimumSystemVersion>26.0</s:minimumSystemVersion><enclosure url="https://example.org/e.zip"/></item>
 <item><s:version>106</s:version><s:hardwareRequirements>x86_64</s:hardwareRequirements><enclosure url="https://example.org/f.zip"/></item>
 <item><s:version>107</s:version><s:maximumSystemVersion>14.0</s:maximumSystemVersion><enclosure url="https://example.org/g.zip"/></item>"#,
    );
    let mut s = selection();
    assert_eq!(feed::select(&items, &s).unwrap().version, "101");
    s.channel = UpdateChannel::Beta;
    assert_eq!(feed::select(&items, &s).unwrap().version, "102");
    s.skipped = Some("102");
    assert_eq!(feed::select(&items, &s).unwrap().short_version, "2.0");
    s.channel = UpdateChannel::Stable;
    s.skipped = Some("2.0");
    assert!(feed::select(&items, &s).is_none());
}
#[test]
fn informational_and_critical_are_version_dependent_and_rollout_is_manual_aware() {
    let items = feed(
        r#"<item><s:version>200</s:version><s:informationalUpdate><s:belowVersion>110</s:belowVersion></s:informationalUpdate><s:criticalUpdate s:version="120"/><s:phasedRolloutInterval>86400</s:phasedRolloutInterval><pubDate>Mon, 05 Oct 2015 19:20:11 +0000</pubDate><link>https://example.org/upgrade</link><enclosure url="https://example.org/a.zip"/></item>"#,
    );
    let i = &items[0];
    assert!(i.informational_for("100"));
    assert!(!i.informational_for("111"));
    assert!(i.critical_for("111"));
    assert!(!i.critical_for("120"));
    let mut s = selection();
    s.installed = "150";
    s.manual = false;
    assert!(feed::select(&items, &s).is_none());
    s.manual = true;
    assert!(feed::select(&items, &s).is_some());
    s.manual = false;
    s.installed = "100";
    assert!(feed::select(&items, &s).is_some());
}
#[test]
fn xml_and_urls_reject_unsafe_inputs_and_false_namespaces() {
    for xml in [
        r#"<!DOCTYPE rss [<!ENTITY attack SYSTEM "file:///etc/passwd">]><rss/>"#,
        r#"<rss><channel><item><sparkle:version>1</sparkle:version></item></channel></rss>"#,
    ] {
        assert!(
            feed::parse(xml.as_bytes()).is_err() || feed::parse(xml.as_bytes()).unwrap().is_empty()
        );
    }
    for url in [
        "http://example.org/a",
        "file:///x",
        "javascript:alert(1)",
        "https://user:pass@example.org/a",
    ] {
        assert!(feed::https(url).is_err());
    }
    let xml = r#"<rss xmlns:s="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><s:version>2</s:version><s:informationalUpdate/><link>http://example.org/upgrade</link></item></channel></rss>"#;
    assert_eq!(
        feed::parse(xml.as_bytes()).unwrap_err().code,
        "E_UPDATE_FEED"
    );
    let false_ns = r#"<rss xmlns:s="https://attacker.example/ns"><channel><item><s:version>999</s:version><enclosure url="https://example.org/a"/></item></channel></rss>"#;
    assert!(feed::parse(false_ns.as_bytes()).unwrap().is_empty());
}
fn identity(version: &str) -> Identity {
    Identity {
        valid: true,
        team_id: "TESTTEAM01".into(),
        identifier: "test.app".into(),
        requirement: "anchor apple generic and identifier test.app".into(),
        notarization_unavailable: false,
        notarized: true,
        architectures: ["arm64".into(), "x86_64".into()].into(),
        minimum_os: "13.0".into(),
        minimum_os_by_arch: Default::default(),
        version: version.into(),
        marketing_version: String::new(),
    }
}
#[test]
fn every_bundle_gate_has_a_failure_case() {
    let old = identity("1");
    let new = identity("2");
    let check =
        |new: &Identity| gate::identities(&old, new, true, "15.0", "arm64", Some("TESTTEAM01"));
    assert!(check(&new).is_ok());
    for (code, mut bad) in [
        ("E_UPDATE_SIGNATURE", new.clone()),
        ("E_UPDATE_TEAM_MISMATCH", new.clone()),
        ("E_UPDATE_NOT_NOTARIZED", new.clone()),
        ("E_UPDATE_ARCH", new.clone()),
        ("E_UPDATE_MIN_OS", new.clone()),
    ] {
        match code {
            "E_UPDATE_SIGNATURE" => bad.valid = false,
            "E_UPDATE_TEAM_MISMATCH" => bad.team_id = "ANOTHER001".into(),
            "E_UPDATE_NOT_NOTARIZED" => bad.notarized = false,
            "E_UPDATE_ARCH" => {
                bad.architectures.remove("arm64");
            }
            _ => bad.minimum_os = "26.0".into(),
        };
        assert_eq!(check(&bad).unwrap_err().code, code);
    }
    assert_eq!(
        gate::identities(&old, &new, false, "15.0", "arm64", None)
            .unwrap_err()
            .code,
        "E_UPDATE_TEAM_MISMATCH"
    );
    let mut bad = new.clone();
    bad.requirement.push_str(" or true");
    assert!(check(&bad).is_ok());
    bad = new.clone();
    bad.identifier = "other.app".into();
    assert_eq!(check(&bad).unwrap_err().code, "E_UPDATE_TEAM_MISMATCH");
    bad = new.clone();
    bad.architectures.remove("arm64");
    assert_eq!(
        gate::identities(&old, &bad, true, "15.0", "x86_64", None)
            .unwrap_err()
            .code,
        "E_UPDATE_ARCH"
    );
    assert_eq!(check(&old).unwrap_err().code, "E_UPDATE_NOT_NEWER");
}
#[test]
fn eddsa_and_sha256_verify_actual_bytes_and_fail_on_mutation() {
    let key = Ed25519KeyPair::from_seed_unchecked(&[42; 32]).unwrap();
    let bytes = b"test signed Sparkle archive";
    let public = STANDARD.encode(key.public_key().as_ref());
    let signature = STANDARD.encode(key.sign(bytes).as_ref());
    let digest =
        gate::verify_archive(bytes, &public, &signature, Some(bytes.len() as u64), None).unwrap();
    assert!(gate::verify_archive(bytes, &public, &signature, None, Some(&digest)).is_ok());
    for (index, result) in [
        gate::verify_archive(b"changed", &public, &signature, None, None),
        gate::verify_archive(bytes, &public, &signature, None, Some(&"0".repeat(64))),
        gate::verify_archive(bytes, &public, &signature, Some(1), None),
        gate::verify_archive(bytes, "wrong", &signature, None, None),
    ]
    .into_iter()
    .enumerate()
    {
        assert_eq!(
            result.unwrap_err().code,
            if matches!(index, 0 | 3) {
                "E_UPDATE_FEED_SIGNATURE"
            } else {
                "E_UPDATE_DIGEST"
            }
        );
    }
}

#[test]
fn marketing_versions_allow_build_counter_reset_but_never_downgrade() {
    let mut old = identity("1000");
    old.marketing_version = "2.9.0".into();
    let mut new = identity("1");
    new.marketing_version = "3.0.0".into();
    let check = |new: &Identity| gate::identities(&old, new, true, "15.0", "arm64", None);
    assert!(check(&new).is_ok());
    new.marketing_version = "2.8.0".into();
    new.version = "2000".into();
    assert_eq!(check(&new).unwrap_err().code, "E_UPDATE_NOT_NEWER");
    new.marketing_version = "2.9.0".into();
    assert!(check(&new).is_ok());
    new.version = "1000".into();
    assert_eq!(check(&new).unwrap_err().code, "E_UPDATE_NOT_NEWER");
    new.marketing_version.clear();
    new.version = "1".into();
    assert_eq!(check(&new).unwrap_err().code, "E_UPDATE_NOT_NEWER");
}

#[test]
fn preserves_inline_notes_and_marks_bare_or_archived_packages() {
    let items = feed(
        r#"<item><s:version>101</s:version><description><p>Fix &amp; improve <b>launch</b></p></description><enclosure url="https://example.org/a.pkg"/></item><item><s:version>102</s:version><enclosure url="https://example.org/a.zip" s:installationType="package"/></item>"#,
    );
    assert!(items.iter().all(|i| i.package));
    let notes = serde_json::to_value(&items[0].notes).unwrap();
    assert_eq!(notes["format"], "html");
    assert_eq!(notes["body"], "<p>Fix &amp; improve <b>launch</b></p>");
}

#[test]
fn explicit_reinstall_can_select_equal_or_older_but_preserves_compatibility_filters() {
    let items = feed(
        r#"
      <item><s:version>99</s:version><enclosure url="https://example.org/a.zip"/></item>
      <item><s:version>100</s:version><s:minimumSystemVersion>26.0</s:minimumSystemVersion><enclosure url="https://example.org/b.zip"/></item>
      <item><s:version>101</s:version><s:channel>beta</s:channel><enclosure url="https://example.org/c.zip"/></item>
    "#,
    );
    let mut s = selection();
    s.skipped = Some("99");
    assert!(feed::select(&items, &s).is_none());
    assert_eq!(
        feed::select_for_reinstall(&items, &s).unwrap().version,
        "99"
    );
    s.installed = "99";
    assert!(feed::select(&items, &s).is_none());
    assert_eq!(
        feed::select_for_reinstall(&items, &s).unwrap().version,
        "99"
    );
}
