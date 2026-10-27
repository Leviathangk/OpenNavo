use opennavo_desktop_lib::{native_command, uninstall::identity::command, updater::admin};
use std::{path::Path, time::Duration};

#[test]
fn successful_warnings_do_not_contaminate_xml_file_lists_or_numbers() {
    // The shell is a test fixture, never a production command backend.
    let run = |script| command("/bin/sh", &["-c", script], Duration::from_secs(3)).unwrap();
    let xml =
        run("printf '<plist version=\"1.0\"><dict/></plist>'; printf 'deprecated command\n' >&2");
    assert!(plist::Value::from_reader_xml(xml.stdout.as_bytes()).is_ok());
    assert!(xml.stderr.contains("deprecated"));
    let listing = run("printf 'Example.app/Contents/Info.plist\n'; printf 'archive warning\n' >&2");
    let info =
        br#"<pkg-info install-location="/Applications"><bundle id="org.example.App"/></pkg-info>"#;
    assert!(
        admin::store_payload_scope(
            info,
            &listing.stdout,
            Path::new("/Applications/Example.app"),
            "org.example.App"
        )
        .is_ok()
    );
    let arm = run("printf '1\n'; printf 'diagnostic\n' >&2");
    assert_eq!(arm.stdout.trim(), "1");
    let signing = run("printf 'designated => fixture\n'; printf 'TeamIdentifier=TESTTEAM01\n' >&2");
    assert!(
        signing
            .text()
            .contains("designated => fixture\nTeamIdentifier=TESTTEAM01")
    );
}

#[test]
fn failed_commands_preserve_exit_code_and_bounded_stderr() {
    let error = command(
        "/bin/sh",
        &[
            "-c",
            "printf 'installer failed\n'; printf 'mount denied\n' >&2; exit 7",
        ],
        Duration::from_secs(3),
    )
    .unwrap_err();
    assert_eq!(error.message, "command_exit");
    let detail: serde_json::Value = serde_json::from_str(error.detail.as_deref().unwrap()).unwrap();
    assert_eq!(detail["exitCode"], 7);
    assert_eq!(detail["stdout"], "installer failed\n");
    assert_eq!(detail["stderr"], "mount denied\n");
    let output = native_command::output(
        "/bin/sh",
        &["-c", "printf 'assessments disabled\n' >&2; exit 1"],
        Duration::from_secs(3),
    )
    .unwrap();
    assert_eq!(output.code, Some(1));
    assert_eq!(output.stderr.trim(), "assessments disabled");
}

#[test]
fn oversized_output_is_rejected_instead_of_parsing_a_truncated_document() {
    let error = command(
        "/usr/bin/head",
        &["-c", "4194305", "/dev/zero"],
        Duration::from_secs(3),
    )
    .unwrap_err();
    assert_eq!(error.message, "command_output_limit");
}

#[test]
fn timeout_applies_to_descendants_holding_output_open_after_parent_exit() {
    let error = command(
        "/bin/sh",
        &["-c", "sleep 1 & exit 0"],
        Duration::from_millis(100),
    )
    .unwrap_err();
    assert_eq!(error.code, "E_TIMEOUT");
}
