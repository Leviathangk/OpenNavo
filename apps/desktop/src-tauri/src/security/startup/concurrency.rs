use super::*;
use std::{
    fs,
    sync::mpsc::{self, Receiver, Sender},
    time::Duration,
};

#[derive(Default)]
struct CommandsFixture {
    disabled: AtomicBool,
    fail_after_disable: AtomicBool,
    pause: Mutex<Option<(Sender<()>, Receiver<()>)>>,
}
impl Commands for CommandsFixture {
    fn run(&self, program: &str, args: &[&str]) -> Result<String, AppError> {
        if program != "/bin/launchctl" {
            return Ok(String::new());
        }
        match args.first().copied() {
            Some("print-disabled") if args[1] != "system" => {
                // Capture the old state before pausing the inventory. Actions must
                // finish while this read remains blocked, without publishing it later.
                let output = format!(
                    "\"example.concurrent\" => {}",
                    self.disabled.load(Ordering::SeqCst)
                );
                let pause = self.pause.lock().unwrap().take();
                if let Some((started, resume)) = pause {
                    started.send(()).unwrap();
                    resume.recv_timeout(Duration::from_secs(10)).unwrap();
                }
                Ok(output)
            }
            Some("disable") => {
                self.disabled.store(true, Ordering::SeqCst);
                if self.fail_after_disable.load(Ordering::SeqCst) {
                    Err(AppError::new("E_UNKNOWN", "fixture_after_disable"))
                } else {
                    Ok(String::new())
                }
            }
            Some("enable") => {
                self.disabled.store(false, Ordering::SeqCst);
                Ok(String::new())
            }
            Some("print") => Err(AppError::new("E_UNKNOWN", "fixture_not_loaded")),
            _ => Ok(String::new()),
        }
    }
}

fn fixture(
    disabled: bool,
) -> (
    super::super::tests::Fixture,
    Arc<Service>,
    Arc<CommandsFixture>,
    Roots,
    String,
) {
    let (temp, mut service, _) = super::super::tests::fixture();
    let commands = Arc::new(CommandsFixture::default());
    commands.disabled.store(disabled, Ordering::SeqCst);
    Arc::get_mut(&mut service).unwrap().commands = commands.clone();
    let dir = service.home.join("Library/LaunchAgents");
    fs::create_dir_all(&dir).unwrap();
    let program = service.home.join("fixture-program");
    fs::write(&program, b"fixture").unwrap();
    fs::write(
        dir.join("example.concurrent.plist"),
        format!(r#"<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>example.concurrent</string><key>Program</key><string>{}</string><key>RunAtLoad</key><true/></dict></plist>"#, program.display()),
    ).unwrap();
    let roots = Roots {
        home: service.home.clone(),
        root: temp.path().canonicalize().unwrap(),
        applications: vec![],
    };
    let id = service
        .startup_scan_at(&roots)
        .unwrap()
        .items
        .into_iter()
        .find(|item| item.label.as_deref() == Some("example.concurrent"))
        .unwrap()
        .id;
    (temp, service, commands, roots, id)
}

fn act_during_scan(
    service: &Arc<Service>,
    commands: &CommandsFixture,
    roots: &Roots,
    id: &str,
    action: StartupItemAction,
) -> (ActOutcome, StartupSnapshot) {
    let (started, waiting) = mpsc::channel();
    let (resume, paused) = mpsc::channel();
    *commands.pause.lock().unwrap() = Some((started, paused));
    let scanner = service.clone();
    let roots = roots.clone();
    let scan = std::thread::spawn(move || scanner.startup_scan_at(&roots).unwrap());
    waiting.recv_timeout(Duration::from_secs(3)).unwrap();
    let (finished, result) = mpsc::channel();
    let actor = service.clone();
    let id = id.to_owned();
    let act = std::thread::spawn(move || {
        finished
            .send(actor.startup_act(vec![id], action).unwrap())
            .unwrap();
    });
    let outcome = result.recv_timeout(Duration::from_secs(2));
    // Always release/join, including when the old whole-scan lock makes the action time out.
    resume.send(()).unwrap();
    act.join().unwrap();
    let snapshot = scan.join().unwrap();
    (
        outcome.expect("startup action waited for the inventory"),
        snapshot,
    )
}

#[test]
fn startup_switches_finish_during_scan_and_stale_inventory_never_reverts_them() {
    for disabled in [false, true] {
        let (_temp, service, commands, roots, id) = fixture(disabled);
        let action = if disabled {
            StartupItemAction::Enable
        } else {
            StartupItemAction::Disable
        };
        let expected = if disabled {
            StartupState::Enabled
        } else {
            StartupState::Disabled
        };
        let (outcome, snapshot) = act_during_scan(&service, &commands, &roots, &id, action);
        assert_eq!(outcome.done.as_slice(), std::slice::from_ref(&id));
        assert!(outcome.failed.is_empty());
        assert_eq!(
            snapshot
                .items
                .iter()
                .find(|item| item.id == id)
                .unwrap()
                .state,
            expected
        );
        assert_eq!(service.startup_cached().unwrap().unwrap(), snapshot);
    }
}

#[test]
fn startup_removal_during_scan_is_not_recreated_by_the_old_inventory() {
    let (_temp, service, commands, roots, id) = fixture(false);
    let (outcome, snapshot) =
        act_during_scan(&service, &commands, &roots, &id, StartupItemAction::Remove);
    assert_eq!(outcome.done.as_slice(), std::slice::from_ref(&id));
    assert!(!snapshot.items.iter().any(|item| item.id == id));
    assert!(
        !service
            .saved_startup()
            .unwrap()
            .iter()
            .any(|item| item.id == id)
    );
    assert_eq!(service.quarantine_list().unwrap().len(), 1);
}

#[test]
fn startup_scan_rechecks_even_when_an_action_fails_after_changing_the_os() {
    let (_temp, service, commands, roots, id) = fixture(false);
    commands.fail_after_disable.store(true, Ordering::SeqCst);
    let (outcome, snapshot) =
        act_during_scan(&service, &commands, &roots, &id, StartupItemAction::Disable);
    assert!(outcome.done.is_empty());
    assert_eq!(outcome.failed[0].id, id);
    assert_eq!(
        snapshot
            .items
            .iter()
            .find(|item| item.id == id)
            .unwrap()
            .state,
        StartupState::Disabled
    );
    assert_eq!(service.startup_cached().unwrap().unwrap(), snapshot);
}
