# Self-hosting OpenNavo

Run these commands from the repository root on your deployment host. This guide uses Docker Engine with Compose v2 (including profiles and `depends_on.required`), Node.js 24, pnpm 10, and Python 3.13. The desktop client requires a separate macOS build. Point your web, API, admin, and CDN DNS records at their intended endpoints, and allow inbound HTTPS/HTTP to Caddy. Keep PostgreSQL, Redis, and monitoring ports private.

## Configuration outside the checkout

```sh
install -d -m 700 "$HOME/.config/opennavo"
install -m 600 ops/.env.example "$HOME/.config/opennavo/production.env"
export COMPOSE_ENV_FILES="$HOME/.config/opennavo/production.env"
# Edit this file with your preferred editor before proceeding.
${EDITOR:-vi} "$COMPOSE_ENV_FILES"
```

`COMPOSE_ENV_FILES` tells every Compose invocation, including `migrate.sh`, `backup.sh`, and `rollout.py`, to load the external file. Export it again in every new shell or service environment. Existing exported variables override file values. Do not `source` an untrusted environment file, print expanded Compose configuration into public logs, or commit configuration copies.

Builds now provide hardened `postgres`, `asynqmon`, `redis`, and `caddy` targets alongside the application images. The PostgreSQL image retains the upstream Debian base and PostgreSQL 18 data layout; it rebuilds gosu and installs available OS fixes. Back up existing databases and check collation-version warnings before upgrading. The backup image contains PostgreSQL 18 client tools only.

Set all domains and public URLs to your own HTTPS origins. `WEB_BASE_URL`, `NUXT_PUBLIC_SITE_URL`, and `NUXT_PUBLIC_I18N_BASE_URL` must agree. Set `ADMIN_DIST` to an absolute path containing the built admin bundle. Caddy manages HTTPS for the configured domains. Reserve the configured Caddy address outside its dynamic IP pool and avoid subnet overlap with your host/VPN.

Generate independent secrets for JWT, API/MCP gateway authentication, release registration, OG signing, encryption, database users, and bootstrap/Grafana passwords. For each secret, generate a value with `openssl rand -hex 32` and paste it into the external file. Never reuse the public development or verification credentials. Keep the OG secret stable across replicas/restarts. Keep `GITHUB_SETTINGS_ENCRYPTION_KEY` stable if you use encrypted GitHub settings, and `SEO_SETTINGS_ENCRYPTION_KEY` stable if you store an IndexNow key or Cloudflare purge token in SEO settings. Set `WEB_INTERNAL_SECRET` so the worker can refresh cached pages after catalog changes; without it pages expire by their cache TTL. Leave LLM features disabled unless intentionally configured.

For the bundled PostgreSQL profile, use these connection shapes, substituting the independently generated hex passwords:

```dotenv
DATABASE_URL=postgres://opennavo_app:APP_PASSWORD@postgres:5432/opennavo?sslmode=disable
MIGRATION_DATABASE_URL=postgres://opennavo_migrate:MIGRATION_PASSWORD@postgres:5432/opennavo?sslmode=disable
APP_DATABASE_PASSWORD=APP_PASSWORD
MIGRATION_DATABASE_PASSWORD=MIGRATION_PASSWORD
```

For managed PostgreSQL, use the provider's host and required TLS parameters instead. Create the database and a migration role with schema/role-management privileges first; keep the application role separate. URL-encode passwords if they contain URI-reserved characters. The application role is provisioned by `db-permissions` after migrations.

## Request logs and IP addresses

The API's ordinary request logger records request IDs, route templates, status and duration without an IP address field. IP-based rate limits and administrator audit records still use source addresses. The bundled Caddy configuration does not enable access logging. If another reverse proxy sits in front of Caddy, disable its ordinary access log (for example, `access_log off;` in the relevant Nginx server block) or use a format that excludes client addresses and forwarded headers; check included configurations for overrides. Validate the configuration before a graceful reload.

This does not erase historical logs or disable proxy error diagnostics, which may contain client addresses. Review log rotation and infrastructure-provider retention separately, and describe actual behavior in the About configuration rather than promising a zero-log service.

## Build tagged images and admin assets

Use one reviewed source checkout. These local tags are examples; set the four image variables in the external environment file to exactly these tags, or replace them with your own immutable release tags/digests:

```dotenv
SERVER_IMAGE=opennavo-server:local
OPS_IMAGE=opennavo-ops:local
WEB_IMAGE=opennavo-web:local
MCP_IMAGE=opennavo-mcp:local
POSTGRES_IMAGE=opennavo-postgres:local
ASYNQMON_IMAGE=opennavo-asynqmon:local
REDIS_IMAGE=opennavo-redis:local
CADDY_IMAGE=opennavo-caddy:local
```

```sh
pnpm install --frozen-lockfile
pnpm --filter @opennavo/admin build
mkdir -p apps/admin/dist/third-party
cp -R docs/legal/third-party/. apps/admin/dist/third-party/
install -d /srv/opennavo/admin
cp -R apps/admin/dist/. /srv/opennavo/admin/
docker build --pull --no-cache --target server -t opennavo-server:local apps/server
docker build --pull --no-cache --target ops -t opennavo-ops:local apps/server
docker build --pull --no-cache --target postgres -t opennavo-postgres:local apps/server
docker build --pull --no-cache --target asynqmon -t opennavo-asynqmon:local apps/server
docker build --pull --no-cache --target redis -t opennavo-redis:local apps/server
docker build --pull --no-cache --target caddy -t opennavo-caddy:local apps/server
docker build --pull --no-cache -f apps/web/Dockerfile -t opennavo-web:local .
docker build --pull --no-cache -f apps/mcp/Dockerfile -t opennavo-mcp:local apps/mcp
```

Set `ADMIN_DIST=/srv/opennavo/admin`. Use a directory writable by your deployment user or arrange installation privileges separately. Published images can replace local builds: server, ops, postgres, asynqmon, redis, and caddy use `server-v*` tags, web uses `web-v*`, and MCP uses `mcp-v*`. Do not assume identically numbered tags exist across all products. Retain the license/notice bundle described in [third-party licensing](../docs/legal/THIRD_PARTY.md) with redistributed artifacts.

## Prepare S3-compatible storage

Production Compose does not start MinIO. Provision S3-compatible storage separately before initialization. Set `S3_ENDPOINT` to `host:port` without a URL scheme, `S3_USE_SSL=true` for TLS, the correct region/bucket, and a dedicated access key/secret. `CDN_BASE_URL` must be the public HTTPS base for this bucket's public objects, including a bucket path if your provider uses path-style URLs.

The initialization command below creates the bucket if absent and configures anonymous reads only for public prefixes. Its credential therefore needs bucket creation/policy permissions during initialization as well as object read/write/list permissions. If your provider prohibits bucket policy updates, have the storage administrator pre-provision the equivalent public-prefix policy and keep backup objects private. Inspect `apps/server/internal/storage` before adapting this policy; do not make the entire bucket public. Public app assets must be readable through `CDN_BASE_URL`, while database backups must remain inaccessible anonymously. Back up storage separately and configure retention/lifecycle policies deliberately.

## Initialize in order

Choose one database mode and keep the same setting in later shells:

```sh
# Bundled PostgreSQL:
export COMPOSE_PROFILES=local-db
# For managed PostgreSQL instead, use: unset COMPOSE_PROFILES

docker compose -f ops/docker-compose.prod.yml config --quiet
docker compose -f ops/docker-compose.prod.yml up -d --wait redis
# Only for bundled PostgreSQL:
docker compose -f ops/docker-compose.prod.yml up -d --wait postgres

sh ops/migrate.sh
docker compose -f ops/docker-compose.prod.yml --profile ops run --rm storage-init
docker compose -f ops/docker-compose.prod.yml --profile ops run --rm seed

docker compose -f ops/docker-compose.prod.yml up -d --wait api worker web mcp caddy
docker compose -f ops/docker-compose.prod.yml ps
```

`migrate.sh` runs migrations with the migration role, then grants the application role permissions. `seed` is idempotent; it does not sync the full external catalog. Log into the admin origin with your bootstrap credentials and change the password immediately. Confirm English `/` and Chinese `/zh` pages, login, API health, and MCP health. Inspect logs locally without copying credentials into reports. Trigger catalog operations deliberately through the supported admin/Agent workflows.

Profiles are additive: `local-db` starts PostgreSQL, `ops` exposes one-shot migration/seed/storage commands, `backup` enables the backup helper, and `monitoring` enables Prometheus/Grafana/asynqmon. Avoid a blanket `up` with `ops` enabled, which would start one-shot services indiscriminately.

## Page cache isolation upgrade

Web now uses a disposable `web-cache` Redis instance (768 MB, `allkeys-lru`, no persistence, 1 GB container limit). API rate limits, sessions and queues retain the existing `redis` instance. Public API query caches use `CACHE_REDIS_URL=redis://web-cache:6379/1`; invalidation messages still travel over business Redis and are applied to both stores. These must be separate Redis processes: selecting DB 1 instead of DB 0 does not isolate memory. Start `web-cache` before replacing web replicas. Custom/versioned deployment configurations must also set `NUXT_REDIS_URL=redis://web-cache:6379/0` and attach web/cache to the same network; rebuilding images alone does not change runtime environment variables.

After every old web replica has stopped using the business Redis, inspect DB 1 and remove only keys matching `opennavo:web:cache:*` there using incremental SCAN/UNLINK. Do not flush a database or change the business Redis to an eviction policy. Existing page caches otherwise remain until their old TTL expires (up to 24 hours), and an already-full instance can continue rejecting API requests during that period. Do not automatically purge previous build namespaces at application startup: an older replica may still serve traffic during rollout or rollback.

Verify repeated collection list/detail requests, Redis memory headroom and OOM counters after migration. Collection HTML and payload routes now use `no-store`; other routes keep their configured SWR policy on the dedicated cache. Public rate-limit storage failures return 503 with `Retry-After: 5` and a request-correlated server log; requests are never allowed through without a quota check. A rollback to the previous web image should retain the dedicated cache URL.

New cache writes retain page entries for at most one hour, image entries for six hours, and skip serialized entries over 512 KiB (including replacing an old value that has grown oversized). These are storage limits; route revalidation intervals are unchanged. Error renders captured by Nuxt async data return 503 and no-store instead of being cached as successful pages. Asynq dequeue failures use capped exponential backoff with jitter (250–500 ms initially, up to 30 s) and cancel on worker shutdown; empty queues and NOSCRIPT fallback are not treated as infrastructure failures. Lease renewal and task acknowledgement commands are unaffected.

For retired-build cleanup, first identify all build IDs still serving traffic. Set `CACHE_CLEANUP_REDIS_URL` to the database to inspect (the old business DB 1 for migration, dedicated cache DB 0 thereafter). From `apps/web`, run `node scripts/cache-cleanup.mjs --build RETIRED_ID --keep-build ACTIVE_ID`. Repeat `--keep-build` for every active build. This defaults to a read-only count/size preview; add `--apply` only after the retired replicas have stopped. The tool removes only the exact build prefix with bounded SCAN/UNLINK batches and rejects active build IDs. It does not discover replicas automatically, and must not be used while that retired build is still writing. Never run FLUSHDB/FLUSHALL.

API metrics now expose `opennavo_redis_*` with `role=business|cache`: memory, memory limit, OOM errors, evictions, cache hits/misses and probe availability. Prometheus warns at 70% business memory and escalates at 85%, detects OOM increases and unavailable probes. Cache eviction alone is expected and does not fire the business-memory alert. The existing API error-rate and queue-backlog alerts remain enabled. Monitor cache hit rate and API/DB load when tuning the 768 MiB budget; the initial 256 MiB filled up and evicted pages faster than their revalidation interval.

## Load controls

The reference host has two vCPUs, so routine background work must not compete with request handling:

- Cache invalidation messages are coalesced by each API subscriber: the first message after a quiet period applies at once, later ones within 10 seconds are merged into one pass, and `c:*` supersedes narrower patterns. Bulk admin or agent writes therefore cost one SCAN pass per interval instead of one per write. Changelog fetches invalidate only the affected package's release timeline, and only when they record new versions; catalog syncs that change nothing leave caches warm.
- Health checks run every 30 seconds with `start_period: 60s` and `start_interval: 2s`, so deployments still detect healthy candidates within seconds. Each probe is a `docker exec`; the previous 2–5 second intervals cost a measurable share of a core. Existing infrastructure containers keep their old interval until they are recreated during planned maintenance.
- The worker is limited to one CPU. Go adapts `GOMAXPROCS` to the limit.
- The API has a 1 GB memory limit with `GOMEMLIMIT=900MiB`, and decodes at most two uploaded images at a time (a 32-megapixel image needs about 128 MB). Bursts of uploads previously exceeded the old 512 MB limit and crash-looped the API through repeated OOM kills.
- PostgreSQL runtime settings for a small host are persisted in the data volume with `ALTER SYSTEM` and applied by `SELECT pg_reload_conf()` without a restart: `jit = off`, `max_parallel_workers_per_gather = 0`, `work_mem = '16MB'`, `log_min_duration_statement = '500ms'`, `log_temp_files = '10MB'` and `log_parameter_max_length = 0` (slow-query logs show statements but never bound values). Inspect them with `SELECT name, setting FROM pg_settings WHERE source = 'configuration file' AND sourcefile LIKE '%postgresql.auto.conf'`, and revert one with `ALTER SYSTEM RESET <name>` followed by a reload. Apply them again after restoring into a new data volume.

Crawler controls in `robots.txt` (generated by `@nuxtjs/robots`; see `apps/web/nuxt.config.ts`) leave search engines and AI search/user agents (for example Claude-SearchBot, Claude-User, OAI-SearchBot) on the default rules. SEO-tool crawlers (MJ12bot, SerpstatBot) are disallowed entirely. The ClaudeBot training crawler gets `Crawl-delay: 10` and may not fetch `_payload.json` responses or dependency tabs. Do not enable the module's `blockAiBots`: it also blocks the AI search agents. Dependency tabs without dependencies, conflicts or dependents render `noindex, follow`. The production Cloudflare zone additionally blocks the two SEO-tool crawlers with a WAF custom rule (`Block SEO tool crawlers (MJ12bot, SerpstatBot)`); robots.txt alone takes hours to propagate through crawler caches. This dashboard rule is not stored in the repository, so recreate it when moving the zone.

## Backups and restore drills

The ops image includes PostgreSQL tools. The helper reads `MIGRATION_DATABASE_URL` through Compose and uploads backups to private object storage:

```sh
# One backup now; save the returned object key in your private operations record.
sh ops/backup.sh
# Start scheduled backups (also creates one at startup).
docker compose -f ops/docker-compose.prod.yml --profile backup up -d backup
# Optional monitoring; requires GRAFANA_ADMIN_PASSWORD.
docker compose -f ops/docker-compose.prod.yml --profile monitoring up -d prometheus grafana asynqmon
```

Monitoring binds to loopback; use an SSH tunnel or another authenticated private access path. Database backups do not replace backups of S3 objects, environment secrets, Caddy volumes, or release artifacts.

Test restore against a separate empty database first. Use a separate external environment file whose `MIGRATION_DATABASE_URL` points to that disposable target and whose S3 credentials can read the saved backup. The restore operation replaces database contents; never point a drill at the live database.

```sh
export COMPOSE_ENV_FILES="$HOME/.config/opennavo/restore-drill.env"
export BACKUP_OBJECT_KEY='replace-with-the-key-returned-by-backup-create'
docker compose -f ops/docker-compose.prod.yml --profile backup run --rm --no-deps backup restore "$BACKUP_OBJECT_KEY"
```

Validate row counts and application reads on the restored database. Return `COMPOSE_ENV_FILES` to the deployment file before later operations.

## Upgrade and rollback

Back up first and retain the old image digests, static admin bundle, external environment, and schema version. Review migration compatibility before changing tags. Pull or build new images, update the external file, then inspect the backend rollout plan:

```sh
export COMPOSE_ENV_FILES="$HOME/.config/opennavo/production.env"
sh ops/backup.sh
python3 ops/rollout.py
python3 ops/rollout.py --apply
docker compose -f ops/docker-compose.prod.yml up -d --wait --no-deps web mcp caddy
```

The rollout requires two healthy API replicas, applies migrations/permissions, replaces APIs one at a time using a temporary third replica, then updates the worker. Install the matching admin bundle separately. A failed migration or incompatible schema may require a planned database restore, not merely an old image. Do not automatically run down migrations on production. For image-only rollback with a compatible schema, restore the previous tags and admin bundle, then repeat the rollout and frontend/MCP update. Keep the old backup until post-upgrade checks pass.

## Verification limits

`python3 ops/lint.py` and the deployment unit tests validate configuration without deploying. `ops/verify-production.py` creates a fixed isolated verification stack; it is not a command for an existing production deployment. Browser verification must use browser-use CLI. Run restore and full-stack checks on a disposable host before relying on this configuration in production.

## GitHub production and desktop releases

`Deploy production` is a manual workflow. Select a commit on `main`; backend,
frontend, and security CI must have passed for that exact commit. The workflow
builds Linux amd64 server/web/MCP images in GHCR, builds the admin bundle, and
sends immutable image digests and the checked admin archive to the production
SSH receiver. This workflow targets the existing versioned Compose/Caddy
installation; it does not initialize a new server or replace databases/storage.

Create a `production` GitHub Environment with `DEPLOY_HOST`, `DEPLOY_USER`,
`DEPLOY_SSH_KEY`, and `DEPLOY_KNOWN_HOSTS` secrets. Use a dedicated SSH key whose
host-side authorized_keys entry uses `restrict,command="/usr/bin/python3 /path/to/github-deploy.py"`. The key can invoke only the deployment
receiver, not an interactive shell or port forwarding. Pin the host key from a
trusted existing connection. Do not store the server password in GitHub.

Install `ops/github-deploy.py` as a root-owned file. Its private host configuration
is `/etc/opennavo/github-deploy.json`, with `root`, `project`, `registry`,
`compose_file`, `caddy_file`, `caddy_container`, `postgres_container`,
`postgres_user`, `postgres_database`, `local_origin`, and a `checks` list of
`{host,path}` entries. The existing installation must have `.env`,
`current-release.env`, and `deploy/rolling.compose.json`. Initialize private
`deploy/github-state.json` with `services` mapping api/web/mcp/admin to their
currently routed Compose service names. Never commit these host files: rendered
Compose data and release snapshots contain credentials.

The receiver validates image digests and source revision labels, checks archive
paths/checksum, preserves old hashed assets, takes and validates a PostgreSQL
backup, applies migrations and grants, starts healthy candidates, and switches
Caddy routes. It then updates the worker and verifies routes again. A failed
switch restores old routes and the worker; it never runs down migrations.
Schema changes must remain compatible with the previous application during the
rollout. After both route checks and the worker update succeed, the receiver stops
all retired application containers, retains at most one stopped previous release
for operator-controlled rollback, and removes older application containers. It
also bounds the rolling Compose overlay and state history to the current and
immediately previous releases. Keep versioned application service names out of
`depends_on` in the host's base Compose file: retired releases are pruned from
the overlay, and the receiver's `compose config` check then aborts the deployment
before the backup, migrations or route switch. Infrastructure containers and data volumes are
not removed. The receiver then bounds disk use. It keeps the newest five `gh-*` release snapshots
(optional `release_retention` in the host configuration, minimum two); each snapshot holds that
deployment's pre-migration database dump. The live and previous snapshots are always kept, as is
any snapshot a container still mounts. Older `gh-*` snapshots are deleted. The receiver also
removes OpenNavo server, web and MCP images that neither a container (running or stopped) nor
the current release’s rollback Compose configuration references. Rollback references are resolved
to image IDs first; a failed lookup skips image cleanup. It
removes them by image ID with `docker image rm`, without force. Manual snapshots and other
files under `releases/` are left to the operator. These pre-migration dumps are not a
backup schedule: keep scheduled, off-host backups as well. Retention problems are
reported but never fail a verified deployment; the next deployment retries them. Start the previous application
services and verify their health before restoring their routes during a manual
rollback. Cleanup failures do not roll back a verified release; retrying the same
revision verifies the live routes and resumes cleanup. First inspect a failed run on the server; logs
intentionally omit expanded Docker/Compose errors that could reveal secrets.
The registry token is job-scoped and stored only in a temporary Docker config.
The receiver pulls the three images in parallel, writes the pre-migration dump with zstd
compression (falling back to pg_dump's default where the server lacks zstd), and prints each
phase's duration to the workflow log. Deployments never update the receiver itself: reinstall
the root-owned host copy after every change to `ops/github-deploy.py`.

### Production image and build-cache cleanup

Keep all images referenced by existing containers, including stopped containers,
and the exact image digests or local tags needed by the immediately previous
release. Include its API, web, MCP, admin, and worker image requirements. Resolve
references to image IDs before selecting deletions: multiple tags can refer to
the same image, and a local hotfix may differ from the release's source revision.
The current release's `before-rolling.json` and `before-release.env` snapshots
record the previous deployment's image references. Keep its static assets,
configuration snapshots, and database backup available separately.

Remove only older, unreferenced OpenNavo application images with
`docker image rm <explicit-tag-or-digest>`, without force. Preserve infrastructure
images. Do not use broad `docker image prune -a` when rollback images have no
retained containers: it can remove those images too. The deployment receiver applies
this rule automatically after each verified deployment (see above). Manual cleanup is
only needed for local hotfix tags, images from failed deployments before the next
successful one, or a stuck retention step.

Unused build cache can be reclaimed with `docker builder prune -af` when no build
is in progress. This may make future builds slower, but does not remove runtime
data volumes. Never add `--volumes` or run volume pruning as part of image cleanup.
Compare `docker system df` before and after, verify retained rollback images with
`docker image inspect`, check current container health, and verify the public
website and admin routes after cleanup. Image sizes share layers, so summing
individual image sizes does not measure reclaimed disk space.

For desktop builds, configure the Apple certificate/P12 password/signing identity,
App Store Connect issuer/key ID/P8, and matching Tauri updater private key as
repository Secrets required by `release-desktop.yml`. Configure `ADMIN_API_BASE`,
`DESKTOP_API_BASE`, and `WEB_BASE_URL` as Variables and `CI_RELEASE_TOKEN` as a
Secret matching the backend. A password-protected updater key additionally needs
`TAURI_SIGNING_PRIVATE_KEY_PASSWORD`. Never replace an installed client's updater
key without a deliberate key migration.

After CI passes, push a `desktop-vX.Y.Z` tag to create signed/notarized arm64 and
Intel updater archives plus a universal DMG. A prerelease suffix selects beta.
The build publishes GitHub assets and registers a backend draft. Then run
`Publish desktop update` with that version: it requires the successful signed
release workflow and activates the manifest using the production-local
`desktop-publish` command. Deploy the server image containing this command first.
This keeps database/S3 credentials off GitHub and leaves the existing CI token
restricted to draft registration. Publishing the same tag again is not a version
upgrade: use a new version for changed artifacts.

Existing separate `server-v*`, `web-v*`, and `mcp-v*` workflows remain available
for image distribution. Their tag publications and desktop tag publications now
require successful CI for the exact source commit. Manual build-only runs do not
publish packages. Repository changes are not deployed merely by pushing `main`.
