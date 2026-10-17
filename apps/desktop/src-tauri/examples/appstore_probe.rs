//! M1 production engine probe. No desktop startup, real data directory, purchases or URL opening.
//! Two Apple catalogue requests at most; the rolling limiter is tested offline in appstore::tests.
use opennavo_desktop_lib::{
    appstore::{
        Service,
        native::{Host, NativeHost},
    },
    db::Database,
    model::{Locale, Settings},
};
use serde_json::json;
use std::sync::{Arc, RwLock};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    if std::env::args().skip(1).collect::<Vec<_>>() != ["--real-read-only"] {
        return Err("Usage: appstore_probe --real-read-only".into());
    }
    let directory = tempfile::tempdir()?;
    let database = Database::open(&directory.path().join("probe.db"))?;
    let settings = Arc::new(RwLock::new(Settings {
        locale: Locale::ZhCn,
        ..Settings::default()
    }));
    let service = Service::new(database, settings, Arc::new(|_| {}))?;
    let status = service.status().await?;
    // Native inventory is local-only: do not send the user's installed bundle list to Apple as part of a probe.
    let local = opennavo_desktop_lib::blocking(|| NativeHost.installed()).await?;
    let search = service.search("Keynote".into(), 3).await?;
    let lookup = if let Some(first) = search.first() {
        service.lookup(vec![first.adam_id]).await?
    } else {
        vec![]
    };
    println!(
        "{}",
        json!({"readOnly":true,"status":status,"localStoreCandidates":local.len(),
        "spotlightIds":local.iter().filter(|app|app.adam_id.is_some()).count(),
        "searchResults":search.len(),"lookupResults":lookup.len(),
        "countryConsistent":search.iter().chain(&lookup).all(|app|app.country==status.country)})
    );
    Ok(())
}
