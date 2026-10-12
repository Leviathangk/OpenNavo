use opennavo_desktop_lib::{
    brewfile::export_list,
    model::{Kind, TaskTarget},
};

fn target(token: &str) -> TaskTarget {
    TaskTarget {
        kind: Kind::Cask.into(),
        token: token.into(),
    }
}

#[test]
fn export_selected_list_preserves_order_and_deduplicates_without_homebrew() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("Brewfile");
    let count = export_list(
        path.to_str().unwrap(),
        &[target("zed"), target("firefox"), target("zed")],
    )
    .unwrap();
    assert_eq!(count, 2);
    assert_eq!(
        std::fs::read_to_string(path).unwrap(),
        "cask \"zed\"\ncask \"firefox\"\n"
    );
}

#[test]
fn invalid_list_does_not_overwrite_existing_file() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("Brewfile");
    std::fs::write(&path, "existing").unwrap();
    for targets in [
        vec![target("zed"), target("../bad")],
        vec![TaskTarget {
            kind: Kind::Formula.into(),
            token: "wget".into(),
        }],
        vec![target("zed"); 201],
    ] {
        assert!(export_list(path.to_str().unwrap(), &targets).is_err());
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "existing");
    }
    assert!(export_list("relative/Brewfile", &[target("zed")]).is_err());
}
