//! Scheduled CI artifact inspection. Download/extraction is confined to a fresh
//! temporary directory. No installed app, database, LaunchServices or installer.
use opennavo_desktop_lib::updater::{
    UpdateSource, archive, feed,
    gate::{Inspector, NativeInspector},
    release::{Artifact, Release},
    service,
};
use serde::Deserialize;
use std::{fs, io::Write, time::Duration};
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Sample {
    bundle_id: String,
    team_id: String,
    release: Release,
}
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    if args.len() != 3 || args[0] != "--real-read-only" || args[1] != "--manifest" {
        return Err("usage: update_sample_probe --real-read-only --manifest <resolved-sample.json>; temp downloads only".into());
    }
    let samples: Vec<Sample> = serde_json::from_slice(&fs::read(&args[2])?)?;
    if samples.len() > 20 {
        return Err("sample limit".into());
    }
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(900))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if attempt.previous().len() >= 5 || feed::https(attempt.url().as_str()).is_err() {
                attempt.error("unsafe redirect")
            } else {
                attempt.follow()
            }
        }))
        .build()?;
    let (os, arch) = service::system()?;
    for sample in samples {
        sample.release.validate(UpdateSource::Vendor)?;
        let Some(file) = sample.release.select(&arch, &os).cloned() else {
            println!("{}: no artifact for this runner", sample.bundle_id);
            continue;
        };
        let artifact = Artifact {
            file,
            version: sample.release.version,
            version_key: sample.release.version_key,
        };
        let root = tempfile::tempdir()?;
        let file = root.path().join("archive");
        let mut output = fs::File::create(&file)?;
        let mut response = client
            .get(feed::https(&artifact.file.url)?)
            .send()
            .await?
            .error_for_status()?;
        let mut length = 0u64;
        while let Some(chunk) = response.chunk().await? {
            length += chunk.len() as u64;
            if length > 1024 * 1024 * 1024 {
                return Err("artifact size limit".into());
            };
            output.write_all(&chunk)?
        }
        output.sync_all()?;
        let bytes = fs::read(&file)?;
        artifact.verify(&bytes, UpdateSource::Vendor)?;
        let app = if bytes.starts_with(b"PK\x03\x04") {
            archive::extract_zip(&file, &root.path().join("extracted"))?
        } else if bytes.len() >= 512 && &bytes[bytes.len() - 512..bytes.len() - 508] == b"koly" {
            archive::extract_dmg(&file, &root.path().join("extracted"))?
        } else {
            return Err("sample installer requires separate manual payload verification".into());
        };
        let identity = NativeInspector.inspect(&app)?;
        if !identity.valid
            || identity.team_id != sample.team_id
            || identity.identifier != sample.bundle_id
            || !identity.notarized
            || !identity.architectures.contains(&arch)
        {
            return Err(format!(
                "{}: signature/identity/notarization/architecture mismatch",
                sample.bundle_id
            )
            .into());
        }
        println!(
            "{}: Team ID, bundle ID, notarization, architecture and declared digest passed",
            sample.bundle_id
        );
    }
    Ok(())
}
