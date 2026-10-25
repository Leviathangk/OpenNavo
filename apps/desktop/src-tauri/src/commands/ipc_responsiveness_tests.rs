use crate::{commands, core::DesktopCore};
use std::{
    sync::{Arc, mpsc},
    time::Duration,
};

// Dispatch must return while the worker is waiting on a lock. This tests the real
// generated IPC wrappers, not just whether their source spells `async`.
fn dispatch(
    webview: &tauri::Webview<tauri::test::MockRuntime>,
    command: &str,
    body: serde_json::Value,
) -> (
    mpsc::Receiver<()>,
    mpsc::Receiver<tauri::ipc::InvokeResponse>,
    std::thread::JoinHandle<()>,
) {
    let webview = webview.clone();
    let command = command.to_owned();
    let (returned, returns) = mpsc::channel();
    let (response, responses) = mpsc::channel();
    let thread = std::thread::spawn(move || {
        webview.on_message(
            tauri::webview::InvokeRequest {
                cmd: command,
                callback: tauri::ipc::CallbackFn(0),
                error: tauri::ipc::CallbackFn(1),
                url: "tauri://localhost".parse().unwrap(),
                body: tauri::ipc::InvokeBody::Json(body),
                headers: Default::default(),
                invoke_key: tauri::test::INVOKE_KEY.into(),
            },
            Box::new(move |_, _, result, _, _| {
                response.send(result).unwrap();
            }),
        );
        returned.send(()).unwrap();
    });
    (returns, responses, thread)
}

#[test]
fn library_reads_and_update_preferences_do_not_block_ipc_dispatch_on_locks() {
    let tmp = tempfile::tempdir().unwrap();
    let core = DesktopCore::open(
        tmp.path().join("data"),
        tmp.path().join("cache"),
        tmp.path().join("askpass"),
        Arc::new(|_| {}),
    )
    .unwrap();
    let app = tauri::test::mock_builder()
        .manage(core.clone())
        .invoke_handler(tauri::generate_handler![
            commands::library::library_list,
            commands::library::library_storage,
            commands::updates::update_ignore,
            commands::updates::update_skip,
            commands::updates::update_set_channel,
        ])
        .build(tauri::test::mock_context(tauri::test::noop_assets()))
        .unwrap();
    let window = tauri::WebviewWindowBuilder::new(&app, "main", Default::default())
        .build()
        .unwrap();
    for command in ["library_list", "library_storage"] {
        let library = core.library.write().unwrap();
        let storage = core.storage.write().unwrap();
        let (returned, responses, thread) =
            dispatch(window.as_ref(), command, serde_json::json!({}));
        let responsive = returned.recv_timeout(Duration::from_secs(2)).is_ok();
        let waiting = responses.try_recv().is_err();
        drop(storage);
        drop(library);
        thread.join().unwrap();
        assert!(responsive, "{command} blocked IPC dispatch");
        assert!(waiting, "{command} did not wait for its data lock");
        assert!(matches!(
            responses.recv_timeout(Duration::from_secs(5)).unwrap(),
            tauri::ipc::InvokeResponse::Ok(_)
        ));
    }
    for (command, body) in [
        (
            "update_ignore",
            serde_json::json!({"appKey":"homebrew:cask:fixture", "ignored":true}),
        ),
        (
            "update_skip",
            serde_json::json!({"appKey":"homebrew:cask:fixture", "version":"2"}),
        ),
        (
            "update_set_channel",
            serde_json::json!({"appKey":"homebrew:cask:fixture", "channel":"beta"}),
        ),
    ] {
        let database = core.database.clone();
        let (locked, ready) = mpsc::channel();
        let (release, released) = mpsc::channel();
        let holder = std::thread::spawn(move || {
            database
                .with(|_| {
                    locked.send(()).unwrap();
                    released.recv().unwrap();
                    Ok(())
                })
                .unwrap()
        });
        ready.recv().unwrap();
        let (returned, responses, thread) = dispatch(window.as_ref(), command, body);
        let responsive = returned.recv_timeout(Duration::from_secs(2)).is_ok();
        let waiting = responses.try_recv().is_err();
        release.send(()).unwrap();
        holder.join().unwrap();
        thread.join().unwrap();
        assert!(responsive, "{command} blocked IPC dispatch");
        assert!(waiting, "{command} did not wait for its database lock");
        assert!(matches!(
            responses.recv_timeout(Duration::from_secs(5)).unwrap(),
            tauri::ipc::InvokeResponse::Ok(_)
        ));
    }
}
