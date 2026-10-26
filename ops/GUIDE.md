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

Crawler controls in `robots.txt` (generated per request from the site settings' crawler rules by `apps/web/server/routes/robots.txt.ts` with `packages/shared/src/robots.ts`) leave search engines and AI search/user agents (for example Claude-SearchBot, Claude-User, OAI-SearchBot, ChatGPT-User, Amzn-SearchBot, Amzn-User) on the default rules; these are locked and cannot be listed in the settings. SEO-tool crawlers (MJ12bot, SerpstatBot) are disallowed entirely. The limited training crawlers (by default ClaudeBot, GPTBot, Amazonbot, Bytespider, CCBot, Meta-ExternalAgent, anthropic-ai and MistralAI-Training) get `Crawl-delay: 10` and may not fetch `_payload.json` responses, the details and dependencies tabs, share images (`/_og/`), feeds (`/*/feed.xml`), URLs with a query (`/*?`) or the prefixes of languages not open for indexing (outside `pages.openLocales`). Do not enable the `@nuxtjs/robots` module's `blockAiBots`: it also blocks the AI search agents. Dependency tabs without dependencies, conflicts or dependents render `noindex, follow`. The production Cloudflare zone additionally blocks the two SEO-tool crawlers with a WAF custom rule (`Block SEO tool crawlers (MJ12bot, SerpstatBot)`); robots.txt alone takes hours to propagate through crawler caches. This dashboard rule is not stored in the repository, so recreate it when moving the zone.

### Origin capacity (2026-10-26)

The origin is a small host, so crawlers are answered from the Cloudflare edge where possible and held to a budget
where not:

- Edge lifetimes (`Cloudflare-CDN-Cache-Control`, set by `apps/web/server/utils/edgeCache.ts`): app pages with their
  versions, details and dependencies tabs, articles and authors keep `max-age=172800, stale-while-revalidate=604800,
  stale-if-error=604800` (two days; the URL change feed purges them when they change and after every web release);
  the home page, lists and the other cached pages keep `max-age=600, stale-while-revalidate=3600,
  stale-if-error=86400`. Agents read the two lifetimes from the site index settings (`edgeCache.longTtlSeconds`,
  `shortTtlSeconds`).
- AI training crawlers (GPTBot, ClaudeBot, Amazonbot, Bytespider, CCBot, Meta-ExternalAgent, MistralAI-Training) each
  have a token bucket at the web origin whatever the load: 6 requests a minute with a burst of 12, matching
  `Crawl-delay: 10`; beyond it the web answers `429` with a `Retry-After` of one to three minutes. Requests the edge
  answers cost nothing. Buckets live in each web process: production runs one web container, and during a rolling
  deploy, briefly two, a crawler may get up to twice its budget.
- Runtime configuration (web container): `NUXT_LOAD_SHED_CRAWLER_PER_MINUTE` and `NUXT_LOAD_SHED_CRAWLER_BURST` set the
  budget, `NUXT_LOAD_SHED_ENABLED=false` turns off load shedding and the budget together, and the other `NUXT_LOAD_SHED_*` values
  (`apps/web/nuxt.config.ts`, `loadShed`) set the in-flight and event-loop-lag limits of the load levels.
- Effect: on the web's internal port, `curl -s http://web:9091/metrics | grep 'opennavo_web_load_shed_total'` lists
  refusals by `agent`, `tier` and `level`; `level="budget"` counts the 429s of spent crawler budgets, the other levels
  the 503s of load shedding. A steady `budget` count for one agent with few 503s means the budget, not the load,
  holds that crawler back.

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

### Isolating share-image load and keeping release metrics current

The web service uses `NUXT_REDIS_URL` for HTML and
`NUXT_IMAGE_REDIS_URL` for the `cache:nuxt-og-image` submount. Production
Compose provides a separate `image-cache` Redis process (384 MiB eviction
budget, 512 MiB container limit, no persistence or published port). Redis
database numbers alone do not isolate memory. Empty `NUXT_IMAGE_REDIS_URL`
retains the previous shared-cache behavior for existing local installations.
The rolling receiver also provisions this disposable service before starting
new web candidates and passes its URL into the candidate environment. Existing
image entries in the page pool expire naturally within six hours; no broad
cache flush is needed. Plan for up to 512 MiB additional container memory.

`NUXT_LOAD_SHED_IMAGE_IN_FLIGHT` defaults to 2 per web process. It bounds
admitted image requests, including cache reads; excess requests receive an
uncached 503 with Retry-After and the `image_capacity` metric label. The bound
is not a CPU quota or a separate renderer process. Already-admitted image
requests pass a server-only capability to their internal island rendering
calls, which do not reserve a second request slot or release their parent's
slot. Direct external island requests remain subject to admission controls.
Image request concurrency is exported as `opennavo_web_image_in_flight`.

Install the updated `ops/github-deploy.py` receiver on the deployment host as
part of rollout; updating application images alone does not update the host
receiver. It discovers the running Prometheus container by Compose labels,
updates simple static API/web scrape jobs to the selected release, preserves
other jobs and global/rule configuration, validates with promtool, hot reloads,
and waits for both targets to be UP. Failure restores the prior config, and
route rollback also points monitoring back to the previous services. Same-SHA
retries repair monitoring before retiring old containers. Unfamiliar API/web
scrape job layouts are rejected for operator review instead of silently losing
custom authentication, relabeling, or discovery settings. Prometheus must share
the default network with the application services; its config must be a host
bind mount at `/etc/prometheus/prometheus.yml`. Each release stores a private
`before-prometheus.yml` snapshot; the bind-mounted file is updated in place.
A host without a running Prometheus service is explicitly reported and skipped.

### Usage countries on existing hosts

The API counts a usage report under its `CF-IPCountry` country only when the
connection comes from an address in `USAGE_COUNTRY_TRUSTED_PROXIES`; any other
report counts as unknown. Production Compose sets Caddy's fixed proxy address.
The receiver builds each API candidate from the previous release's service in
the rolling overlay, so a host provisioned before this setting would go on
counting every country as unknown. When the copied service lacks the variable,
the receiver adds the base Compose value `${CADDY_PROXY_IP:-172.30.80.2}`. A
value already set on the host, including an empty one, is kept. Install the
updated receiver first: the setting arrives with the next deployment of a new
revision, not with a retry of the deployed one. After that deployment, check
that the admin dashboard lists real countries rather than only unknown.

Trusting Caddy is only safe while nothing but Cloudflare can set the header.
The bundled Caddyfile removes `CF-IPCountry` from connections whose peer is
outside Cloudflare's ranges. Deployments keep the host's live Caddyfile and
change only service names and the release header, so apply Caddyfile changes
on the host by hand. The filter matches the connection peer. If another reverse
proxy such as Nginx sits in front of Caddy, that proxy is the peer of every
request, and the unchanged filter would remove every country. In that layout,
remove the header at the outer proxy for peers outside Cloudflare, or admit
only Cloudflare at the origin firewall.

## Derived Traditional Chinese

`zh-Hant` remains a readable site/desktop locale, but all external content writes
(including MCP, batch imports, source-language selection and admin corrections)
must use another locale. Valid `zh-CN` text is the only input to Traditional
Chinese. English or other source content must acquire a fresh Simplified Chinese
translation first. Saving or correcting Simplified Chinese regenerates the
Traditional row in the same transaction, with normal cache/page invalidation.
Removing or invalidating Simplified Chinese withdraws its derived row.

The repository generator uses the same embedded OpenCC 1.1.9 dictionaries and
conversion code as the server, without an LLM, network call or native dependency:

```sh
node tooling/scripts/derive-traditional.mjs
node tooling/scripts/derive-traditional.mjs --check
```

Interface synchronization and the root frontend build/generation commands run
the generator. The frontend lint gate checks the generated files. Desktop release
notes derive `zh-Hant.md` from `zh-CN.md` when loaded, and CI still registers ten
languages. CI's Traditional notes are checked against the converter then discarded;
the server creates the stored row. MCP has no such assertion exception.

Migration 00102 retires unfinished independent Traditional translation tasks,
fences outstanding leases, and creates `derived_traditional` receipts. It does
not drop historical completed tasks or language rows. After the normal CI gates
and a database backup, the new worker backfills in pages of 128 candidates, one
object per transaction; retries are idempotent. Invalid derived content (including
length expansion beyond field limits) is skipped and counted as `<entity>:failed`;
later rows continue. Correct the source and rerun the backfill to retry these rows.
Infrastructure failures retry after one minute. Ordinary translation backfill runs
independently, so these retries cannot block it.
The first prior row (including manual text), Simplified content hash, converter
version and latest generated row are retained. Missing/stale Simplified text is
not translated from English by the converter. Unpublished article revisions are
handled when published, not exposed by the backfill.

The server image also provides an explicit preview/apply command (credentials
come only from the deployment environment):

```sh
/traditional-backfill             # rolls back each preview transaction
/traditional-backfill --apply     # resumable backfill
```

For rollback, stop the new worker and quiesce content writes first. Preview with
`/traditional-backfill --rollback`, then use `--rollback --apply` to restore only
rows still matching their last generated snapshot; conflicts preserve later
edits. Deleted parents are counted as conflicts, without stopping later restores.
The rollback receipt is retained. Switch back to the prior verified image
before restarting workers; running the new worker again intentionally regenerates
Traditional Chinese. Do not automatically reopen retired translation tasks.

Dictionary upgrades require reviewing `internal/traditional/data/upstream.json`,
updating its checksums and the converter version, regenerating language packs,
and running converter, migration/rollback, interface replay and write-guard tests.
The data license and distribution notices are included in the normal inventory.

Proof-backed background-item descriptions and collection setup criteria also derive
from Simplified Chinese on save and during backfill. Only their prose is converted;
paths, item types and evidence (including validity) are copied unchanged. Their
receipts participate in the same preview and conditional rollback commands.
Restoring an archived article or reverting to an older published revision schedules
its translations and derives Traditional Chinese after the revision becomes live.

After a completed rollback, the next derivation starts a new backup cycle using
the then-current Traditional row, including any intervening manual changes.
Repeated derivations within an active cycle preserve that cycle's first backup.

A rollback receipt whose original and generated states are both absent completes
normally when the row is still absent. A newly created row remains a conflict.
Code literals are protected using a CommonMark AST (including quote/list containers
and indented blocks); compatibility tests compare code tokens with the UI's
markdown-it parser.

Markdown conversion edits only AST prose segments, preserving the remaining source
bytes. This includes complete fences, inline code delimiters, link destinations
and reference definitions. Implicit reference labels remain unchanged because they
are lookup keys; ordinary prose and explicit link labels are converted.

## SEO demand and delivery recovery

Migration 00103 adds nullable, localized `seo_title` and `seo_description` to
categories and collections. Empty metadata uses the web's existing defaults;
existing translation fingerprints remain valid until metadata is authored.
Admin, public API, MCP models and web rendering must ship with the matching schema.

Migration 00103 also requeues the latest dead URL receipt for the specific legacy
purge-429 or dependencies robots-mismatch failures. It preserves newer receipts
and active work. Cached API dependency decisions are invalidated before recovered
jobs run, and the web release purge refreshes HTML. Empty dependencies pages use
the server's noindex/canonical decision and their obsolete announcements finish
without contacting search engines. Dependencies pages remain outside the sitemap.

Purge requests share the persisted 12-second interval across workers and release
purges. Capacity waits and upstream 429 responses defer delivery without consuming
attempts; `Retry-After` extends the shared deadline (one minute if absent/invalid).
Other failures retain exponential retries. Recovered delivery receipts are not
reverted by the down migration, which only removes the new metadata columns.

Article translation callers should use `article_get.published.id` as the article
translation reference, not the article ID or the revision number. Public request failure
logs include request ID, route, fixed operation stage, error types and classified
causes/SQLSTATE, without copying raw database or upstream error payloads.

## SEO automation removal (migrations 00109–00111)

The server no longer runs SEO operations itself. OpenSEO finds problems and measures
results, an agent plans the work, and the OpenNavo MCP reads and edits site content.
The server keeps technical SEO only: page decisions (`seo.DecidePage`), page states and
sitemaps (`seo:pages`), robots, structured data, and the URL change feed (cache refresh,
Cloudflare purge, IndexNow).

- Migration 00109 adds per-app editorial switches for alternatives and uninstall pages
  (`package_intent_pages`), backfilled from the pages the keyword ledger had opened, and
  optional per-language overview titles and descriptions (`package_seo_overrides`).
  `pages.intentPagesEnabled` stays the global switch; the content thresholds still apply.
- Migration 00110 renames the write-safety tables (`agent_write_controls`,
  `agent_write_events`, `entity_revisions`, `evidence_snapshots`), moves the retired
  engine tables into the `seo_archive` schema, drops the stored Search Console, Bing and
  audit credentials, and replaces the `seo:*` agent permissions with `site:index:view`.
  The retired job types (`seo:gsc`, `seo:inspect`, `seo:bing`, `seo:audit`,
  `seo:opportunities`, `seo:evaluate`, `seo:experiments`) are acknowledged and discarded.
- Migration 00111 gives probe receipts a fresh retry budget when they failed only
  because of the two problems fixed in this release (below). Dead receipts restart from
  `pending`; other errors, active leases and URLs with newer receipts are left alone.

Migration 00110 stays compatible with the previous release for one version, because
rollouts migrate first and replace the API replicas and the worker afterwards. The renamed
and archived tables keep same-named views in `public`, the `seo_settings.automation`
columns stay, `seo_provider_settings` is archived with its credentials cleared and disabled,
token permissions accept both the old and the new codes, OAuth client registrations keep
their old scopes (mapped when read), and the new per-app intent page and SEO tables have no
foreign keys yet (orphans are removed by the daily cleanup). The previous image therefore
keeps working on the new schema, and redeploying it is a complete rollback; `migrate down`
is not needed. Only the new code ignores these leftovers. The next release drops the views,
the two columns and `seo_provider_settings`, tightens the permission check and adds the
foreign keys.

00110 sets `lock_timeout` to five seconds and drops the archived tables' foreign keys to
`packages` and `admin_users` last, so the exclusive lock on those tables lasts only until
the commit. If a long query holds them, the migration fails without changes and the rollout
stops with the old release serving; retry the deploy.

Deploy the API and worker before the MCP: the new MCP tools call the new endpoints.
Pause agent SEO jobs during the deploy and switch them to the new tools afterwards.
Nothing reads `seo_archive` after the next release; once its data is no longer wanted,
drop it with `DROP SCHEMA seo_archive CASCADE`.

### Public probe before IndexNow

Before announcing a URL, the worker fetches the public page and compares it with the
page decision. Robots are compared by meaning: the meta tags (`robots`, `googlebot`,
`bingbot`) and `X-Robots-Tag` headers must allow exactly the decision's indexing and
link following; preview controls such as `max-image-preview` are ignored. Canonicals
are compared as URLs. A mismatch records both the expected and the rendered values.

The web sheds low-priority probes under load with `503`, `Retry-After` and an
`X-OpenNavo-Load-Shed: <level>` header. Such responses and any `429` defer the receipt
until the shared probe cooldown ends without spending a retry. Other `5xx` responses and
network errors remain failures with exponential retries. The settings page and the
agent's site settings show the change-feed backlog: undelivered announcements, the
oldest one, unresolved dead receipts from the last 30 days, the probe cooldown and
consecutive shed probes. An oldest announcement older than a day or any dead receipt
needs attention.

### Traditional Chinese wording (converter v5)

Converter `opencc-data-1.1.9-s2twp-v5` applies OpenNavo's Taiwan wording
(`internal/traditional/data/opennavo-terms.json`: 權限, 登入項目, 選單列, 垃圾桶, 帳號 …)
after OpenCC, to prose only, for server-side content as well as the repository's interface
files. The new version makes the worker derive stored content again when it starts, with
limits: objects whose receipt names another converter at
`TRADITIONAL_BACKFILL_OBJECTS_PER_SECOND` (default 20), and rows actually rewritten at
`TRADITIONAL_BACKFILL_WRITES_PER_MINUTE` (default 30). Text that comes out the same only has
its `derived_traditional` receipt renewed: no row write, revision, page refresh, cache
invalidation or URL notification. A rewritten row refreshes its pages and announces its URLs
like an edit; rewritten background-item descriptions and collection criteria share one full
cache clear per minute. The catalog lock is released between objects, and the receipts record
progress, so a restarted worker continues where the run stopped.

The worker logs `Traditional Chinese backfill progress` at the start and after every 500
objects derived again (`processed`, `stamped`, `written`, `failed`, `pending`, `remaining`,
`eta`), then `Traditional Chinese backfill complete` with the totals, `elapsed` and the counts
per entity (`<entity>` written, `<entity>:stamped`, `<entity>:failed`). Budget about N objects
÷ 20 per second, plus W rewritten rows ÷ 30 per minute. N is the number of zh-CN rows in the
content tables: on 2026-10-26 production had about 27,100 (release_i18n 13,880, package_i18n
6,967, screenshot_i18n 6,122, all others under 100 each) and 26,331 receipts, all from v4, so
one pass takes about 22 minutes and each rewritten row adds up to two seconds. The `eta` in
the progress lines updates from the share of rows rewritten so far. To change the limits, set
them in the deployment environment and restart the worker; `/traditional-backfill` and its
`--apply` use the same limits.
