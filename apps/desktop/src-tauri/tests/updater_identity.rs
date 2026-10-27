use opennavo_desktop_lib::updater::gate::{self, Identity};
use serde::Deserialize;
use std::sync::Mutex;

#[derive(Deserialize)]
struct Sample {
    installed: Identity,
    candidate: Identity,
    os: String,
    arch: String,
}

struct GateLog(Mutex<Vec<String>>);
impl log::Log for GateLog {
    fn enabled(&self, metadata: &log::Metadata<'_>) -> bool {
        metadata.target().ends_with("updater::gate")
    }
    fn log(&self, record: &log::Record<'_>) {
        if self.enabled(record.metadata()) {
            self.0.lock().unwrap().push(record.args().to_string());
        }
    }
    fn flush(&self) {}
}
static LOG: GateLog = GateLog(Mutex::new(Vec::new()));

#[test]
fn real_duoupdater_and_zulip_identities_preserve_dr_and_log_each_rejection() {
    log::set_logger(&LOG).unwrap();
    log::set_max_level(log::LevelFilter::Warn);
    // Captured with NativeInspector on macOS 27.0.1, 2026-10-16:
    // DuoUpdater 0.5.7 -> 0.5.8 and Zulip 5.12.3 -> 5.13.2.
    // In particular, retain DR comments and Zulip's quoted Team ID verbatim.
    let samples: Vec<Sample> = serde_json::from_str(include_str!(
        "fixtures/updater/real-signing-identities.json"
    ))
    .unwrap();
    for sample in samples {
        let check = |new: &Identity, satisfied, expected| {
            gate::identities(
                &sample.installed,
                new,
                satisfied,
                &sample.os,
                &sample.arch,
                expected,
            )
        };
        assert!(check(&sample.candidate, true, None).is_ok());
        assert!(check(&sample.candidate, true, Some(&sample.installed.team_id)).is_ok());
        for reason in [
            "requirement_not_satisfied",
            "team_id_changed",
            "identifier_changed",
            "requirement_changed",
            "expected_team_mismatch",
        ] {
            let mut changed = sample.candidate.clone();
            match reason {
                "team_id_changed" => changed.team_id = "OTHERTEAM1".into(),
                "identifier_changed" => changed.identifier.push_str(".impostor"),
                "requirement_changed" => changed.requirement.push_str(" or true"),
                _ => {}
            }
            let error = check(
                &changed,
                reason != "requirement_not_satisfied",
                (reason == "expected_team_mismatch").then_some("OTHERTEAM1"),
            )
            .unwrap_err();
            assert_eq!(error.code, "E_UPDATE_TEAM_MISMATCH");
            assert_eq!(error.message, "signing_identity_changed");
            assert!(LOG.0.lock().unwrap().iter().any(|line| {
                line.contains(&format!("checks=[\"{reason}\"]"))
                    && line.contains(&sample.installed.identifier)
            }));
        }
    }
}

#[cfg(target_os = "macos")]
#[test]
fn native_codesign_verifies_literal_dr_instead_of_interpreting_it_as_a_filename() {
    use opennavo_desktop_lib::{
        uninstall::identity::command,
        updater::gate::{Inspector, NativeInspector},
    };
    use std::{fs, time::Duration};
    let dir = tempfile::tempdir().unwrap();
    let binary = dir.path().join("signed-fixture");
    // Sign only a disposable copy. No certificate, real app or LaunchServices.
    fs::copy("/usr/bin/true", &binary).unwrap();
    command(
        "/usr/bin/codesign",
        &[
            "--force",
            "--sign",
            "-",
            "--identifier",
            "test.opennavo.requirement",
            "--requirements",
            "=designated => identifier \"test.opennavo.requirement\"",
            binary.to_str().unwrap(),
        ],
        Duration::from_secs(15),
    )
    .unwrap();
    let info = command(
        "/usr/bin/codesign",
        &["-d", "-r-", binary.to_str().unwrap()],
        Duration::from_secs(15),
    )
    .unwrap();
    let info = info.text();
    let dr = info
        .lines()
        .find_map(|line| line.strip_prefix("designated => "))
        .unwrap();
    // This is the regression: the former bare -R argument always failed here.
    assert!(NativeInspector.satisfies(&binary, dr).unwrap());
    assert!(
        !NativeInspector
            .satisfies(&binary, "identifier \"other.app\"")
            .unwrap()
    );
    assert!(!NativeInspector.satisfies(&binary, "").unwrap());
    let requirement_file = dir.path().join("requirement.txt");
    fs::write(&requirement_file, "always").unwrap();
    assert!(
        !NativeInspector
            .satisfies(&binary, requirement_file.to_str().unwrap())
            .unwrap()
    );
}
