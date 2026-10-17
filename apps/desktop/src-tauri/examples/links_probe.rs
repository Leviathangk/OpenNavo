//! Read and validate the independent links feed using a disposable catalog DB.
//! Never launches the desktop app, opens a URL, or touches the installed app DB.
use opennavo_desktop_lib::{
    catalog::{
        Response,
        links::{self, LinkCatalog},
    },
    db::Database,
};
use std::{path::PathBuf, time::Duration};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    if args.len() != 3
        || args[0] != "--real-read-only"
        || !matches!(args[1].as_str(), "--api-base" | "--catalog")
    {
        return Err(
            "Usage: links_probe --real-read-only (--api-base URL | --catalog JSON_FILE)".into(),
        );
    }
    let raw = if args[1] == "--catalog" {
        let path = PathBuf::from(&args[2]);
        if std::fs::metadata(&path)?.len() > 16 * 1024 * 1024 {
            return Err("catalog too large".into());
        }
        std::fs::read(path)?
    } else {
        let mut response = reqwest::Client::builder()
            .timeout(Duration::from_secs(30))
            .build()?
            .get(format!("{}/catalog/links", args[2].trim_end_matches('/')))
            .send()
            .await?
            .error_for_status()?;
        let mut raw = Vec::new();
        while let Some(chunk) = response.chunk().await? {
            if raw.len() + chunk.len() > 16 * 1024 * 1024 {
                return Err("catalog too large".into());
            }
            raw.extend_from_slice(&chunk);
        }
        raw
    };
    let response: Response<LinkCatalog> = serde_json::from_slice(&raw)?;
    if response.code != "0000" {
        return Err("catalog API failure".into());
    }
    let catalog = response.data.ok_or("missing catalog")?;
    let temp = tempfile::tempdir()?;
    let database = Database::open(&temp.path().join("links.db"))?;
    let count = links::apply(&database, &catalog, chrono::Utc::now().timestamp_millis())?;
    println!(
        "{}",
        serde_json::json!({"readOnly":true,"items":count,"etag":catalog.etag,"tasks":database.tasks()?.len(),"temporaryDatabase":true,"source":args[1]})
    );
    Ok(())
}
