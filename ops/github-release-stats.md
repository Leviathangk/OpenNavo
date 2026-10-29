# GitHub release download statistics

The `github-release:snapshot` worker runs hourly at minute 10 and once more at
23:59 Beijing time to capture the end of the calendar day. The worker loads
`TZ_SCHEDULE` (default and deployment setting: `Asia/Shanghai`) and passes it as
asynq's scheduler location. The additional `CRON_TZ=Asia/Shanghai 59 23 * * *`
schedule explicitly fixes that run to Beijing time (15:59 UTC), even if the
scheduler location changes. Both schedules share the same job type and
deduplication, with a 20-minute uniqueness lock matching the run timeout.

It reads the encrypted token from `github_settings` through `Store.GitHubToken`,
then fetches every release page and the asset pages of published `desktop-v*`
releases from `opennavo/OpenNavo`. Releases with `draft: true` are skipped before
fetching assets and never enter snapshots or totals. Every published asset is
retained for auditing, with one complete observation per asset per Beijing
natural day. Migration `00123_github_release_snapshots.sql` creates the snapshot
table. No backfill is performed.

The dashboard aggregates all desktop versions together. Future beta/pre-release
versions are counted together with stable releases:

- **Downloads**: only `*_universal.dmg`. This includes the download page and
  Homebrew cask installs **and upgrades**.
- **Updates**: only `*.app.tar.gz`, summed across Apple Silicon and Intel.
  These are download counts, not people or unique installations.
- `.sig`, `SHA256SUMS`, ClamAV source archives and all other files are excluded
  from both categories. The updater manifest on `cdn.opennavo.com` is not measured.

Cumulative totals sum the latest complete snapshot. Daily values sum each asset's
increase since the preceding snapshot day, clamping negative differences to zero.
A new asset on a later day contributes its full count. The first snapshot day
has cumulative totals only, including after further hourly runs that day.
`dailyStartDate` is the second distinct snapshot day. A missed day has null daily
values; its increase belongs to the next successful snapshot day.

All asset pages must succeed before any snapshot rows change. Missing credentials,
invalid responses and API failures fail the job and are logged by the job runner.
The dashboard keeps the last successful totals and `lastSnapshotAt`. A newer
completed observation cannot be overwritten by a slower, older run. Deleted
upstream assets leave the latest complete snapshot without erasing earlier days.

The overview API exposes `githubReleases` with downloads/updates today, yesterday
and total, the last 30 Beijing calendar days, `lastSnapshotAt` and `dailyStartDate`.
Null means unavailable; zero means a measured zero. The card shows the capture
date and time in Asia/Shanghai and supports pointer and keyboard chart inspection
and a daily data table.

## Local verification

Unit and integration cases cover classification, initial/new assets, resets,
missed days, Beijing midnight, atomic concurrent upserts, saved-token rotation,
failure retention, migration up/down and the dashboard OpenAPI response.

The opt-in `TestLiveGitHubReleaseSnapshot` (`integration,live` build tags) always
creates disposable local PostgreSQL/Redis containers. Supply the credential in
`OPENNAVO_LIVE_GITHUB_TOKEN` through the process environment. It is used only in
memory and never saved to the test database. The test calls only GitHub GET APIs,
checks totals against the 2026-10-29 baseline (953 downloads, 582 updates), and
prints totals and capture time. Never put the credential in a command committed
to the repository or in output files.
