# OpenNavo MCP

Python 3.13 + uv, using FastMCP 4.0.x. The 103 tools and 9 resources adapt only the internal `/agent-api`.

Run `uv sync --locked` in this directory. From the repository root, run `make gen-mcp lint-mcp test-mcp build-mcp` to generate and check artifacts. `make gen` and `make lint test` also include MCP. The authoritative tool manifest is `src/opennavo_mcp/tools/manifest.py`.

Before startup, supply `AGENT_GATEWAY_SECRET` through the process environment, matching the API secret. Optionally set `OPENNAVO_AGENT_API_BASE` (default: `http://127.0.0.1:8080/agent-api`). Run `uv run --locked opennavo-mcp` to listen at `127.0.0.1:8790/mcp`; clients send Bearer tokens. stdio requires `OPENNAVO_MCP_TRANSPORT=stdio` and `OPENNAVO_TOKEN`. No .env files are loaded automatically. Never put real tokens in configuration examples or command-line arguments.

Top-level tool parameters use snake_case; nested fields retain OpenAPI camelCase. Write tools accept `dry_run=true`. All business output is wrapped in `untrusted`. Go performs final permission, deletion-allowance and quota checks; MCP caches introspection for 25 seconds. Go downloads URL uploads; MCP never contacts image sites. For local files, `asset_upload_ticket` returns a single-use `uploadUrl` under the admin origin's `/admin-api`; the client POSTs the file there directly (for example `curl -sS -F file=@icon.png '<uploadUrl>'`), so image bytes never pass through MCP or the model. Go stores only the ticket hash, expires it after 10 minutes and rechecks the token on upload.

The HTTP entry point trusts no forwarded IPs by default. Behind a reverse proxy, set `OPENNAVO_MCP_TRUSTED_PROXIES` to a JSON array of trusted CIDRs; never trust uncontrolled proxies. HTTP and stdio use the same permission middleware. `/healthz` returns liveness only.

Production Compose/Caddy integration is provided. Exercise deployment boundaries from the repository root with `node tooling/scripts/tasks/server.mjs prod-verify --mcp-only`.

Article management covers all 16 exposed article and author operations. Grant `content:article:edit` for drafts, optional review submission, draft revision reversion and author lookup; grant `content:article:publish` separately for publication, withdrawal/restoration, reverting to a published revision, slug and language visibility changes, and author creation/updates. Article mutations accept optional `expected_revision` checks and support `dry_run`. Existing token permissions remain unchanged and must be explicitly updated to grant publication access.


Business writes apply directly after token authorization. Previews (`dry_run`) are optional.
Agents should send a retry key (`idempotency_key`, forwarded as `Idempotency-Key`; reuse it only
for the identical request) and the revision they read (`expected_revision`, forwarded as
`X-Expected-Revision`) with each write; no other execution metadata exists. Batch translations
carry the same checks per language in the body. Explicit translation edits may replace manual
text; provenance and revision history are retained.

Retry keys are scoped to the calling client/token and must identify one unchanged request.
MCP sends write-safety values in headers, without duplicating the legacy article body fields.
Optional query/header nulls mean omission; body nulls retain their clearing semantics.
Partial category, collection and feature updates send only the caller's patch. The server
replays identical retry keys before reading state, then merges new writes under its write lock.
Optional `expected_state` and `expected_revision` checks run in that same transaction; reuse
the original arguments and retry key after a lost response, including any explicit guards.
Package content revisions include screenshot edits and deletions; use the revision from
`package_get` for screenshot writes too.

`package_set_enabled(package="pearcleaner", enabled=true, dry_run=true)` previews enabling
catalog visibility; omit `dry_run` (or set it to false) to apply it. It requires
`catalog:package:edit`; token lookup also requires `catalog:package:view` (a numeric admin ID
needs only edit permission). This is the admin UI's visibility switch: `enabled=true` writes
`hidden=false`, and `enabled=false` writes `hidden=true`. Homebrew/stale-version disabling and
removed state still apply; inspect `hidden`, `disabled` and `removed` with `package_get` before
expecting an app to appear publicly. The tool sends only the visibility patch through
`updatePackageMeta`, retaining audit logs, restorable revisions, optional write-safety headers,
cache invalidation and the catalog change feed used for sitemap/URL updates. Dry runs return
the existing metadata preview's before/after state without persisting business changes.

`listing_gaps` defaults to visible, enabled, current non-font apps (`hidden=false`,
`disabled=false`, `removed=false`, `is_font=false`). Override those filters to inspect another
scope. Its total counts matching packages; worklists overlap and are not a unified task queue.
`releases_list(view="versions")` covers recorded cask and link builds; `source`, `hidden` and
translation filters belong to `view="releases"`. Reading by ID takes no list filters.
Tools reject arguments their selected mode does not consume.

Failed calls return an MCP error result whose `structuredContent.error` holds `code`, `message`,
`reason` and `detail`, plus `currentRevision` for revision conflicts. Known codes forward only
rule-shaped reasons (`rejected: <reason>[:<detail>]`, such as `revision_conflict:<n>`,
`idempotency_key_reused`, `batch_too_large:<n>` or `tap_*`; a detail may join several parts with
colons, as in `derived_locale_read_only:zh-Hant:zh-CN`) and drop other backend text.
Unrecognized four-digit codes keep their code with a cleaned one-line message of at most 300
characters. MCP's own permission checks answer 1004 with the server's reasons:
`permission_required:<permission>`, `delete_not_allowed` or `operation_not_exposed`.

`package_get` takes `locale` (language of summaries and related names such as categories),
`fields` (top-level keys; `id`, `token` and `revision` always come back when the detail is read)
and `locales` (languages kept in `i18n`, localized maps and screenshot captions). Requesting only
`facts`, `translations` or `screenshots` skips the detail read. A reply over 32 KB leaves whole
values out, largest first, and lists each in `omitted` with its path, character count, sha256 (of
the UTF-8 text, or of a non-string value's compact JSON) and the `read` call that returns it whole:
`package_text_get` for an i18n text or `raw`, or `field=json` with the supplied `path` for
other values. It never carries half a value. `package_text_get` reads in parts (`offset`,
`limit` up to 16000 characters and about 30 KB per part). Add each returned `length` to
`offset` until reaching `total`, join the values and verify the whole text's sha256.
For `field=json`, parse the joined JSON to recover the value. Even a single language of
hundreds of screenshot captions can be read this way. If the digest changes between parts,
restart the read; parts from different versions must not be joined.

SEO work runs outside the server: OpenSEO finds problems and measures results, an agent plans,
and these tools change site content (`opennavo://guide/seo-routine`). Template and technical
problems go to people, not through MCP.

For arbitrary apps, search first, then call `link_app_add` with `token`, `name` and at least one
of `homepage`, `download_url`, or `github_repo`. Complete metadata with `package_set_text`,
`package_update_meta`, `package_set_categories`, image upload/attachment and `translations_write`.
Use `link_app_update` for later source changes. Use `tap_package_add` for supported third-party
Homebrew casks and `package_refresh` to queue a sync for existing Homebrew/tap/link entries. `package_versions`
lists versions eligible for `release_write_notes`; a listed build also permits shared notes
on its base version (for example, `2.0,42` permits `2.0`). Writing notes does not create a version.
Listing a link app does not guarantee a one-click installer or a usable update feed.

GitHub release selection is optional on both `link_app_add` and `link_app_update`. The admin
POST `/links/packages` and PATCH `/links/packages/{id}` expose the same fields in camelCase:

| MCP argument | Admin field | Meaning |
| --- | --- | --- |
| `github_tag_prefix` | `githubTagPrefix` | Case-sensitive literal tag prefix, at most 256 UTF-8 bytes. |
| `github_tag_regex` | `githubTagRegex` | Go RE2 expression on the raw tag, at most 1024 UTF-8 bytes. Use `^` and `$` for a whole-tag match. |
| `github_asset_pattern` | `githubAssetPattern` | Case-sensitive Go `path.Match` glob on asset names, at most 512 UTF-8 bytes; for example `*.dmg`. Supports `*`, `?`, character classes and backslash escaping, without globstar. |
| `include_prereleases` | `includePrereleases` | Defaults to `false`. Set `true` to consider releases marked prerelease by GitHub. Drafts are always excluded. |

Prefix and regex are mutually exclusive; the asset pattern is ANDed with either. Filters
require `github_repo`. Empty/blank strings, invalid glob syntax and invalid or unsupported
RE2 syntax (including lookaround/backreferences) are rejected by Go. Expressions are never
compiled or executed by Python. Send `null` to clear a string filter, and `false` to disable
prereleases. Omitted PATCH fields retain their values. When using a prefix, the prefix and
then a leading `v` are removed from the displayed version; regex selection removes only a
leading `v`. The original tag is always retained in release metadata.

Without filters and with prereleases disabled, sync keeps using GitHub's `/releases/latest`.
With any filter or prereleases enabled, sync reads up to ten pages of 100 releases and
chooses the match with the newest `published_at` (lexically greatest tag breaks ties),
regardless of list order or semantic version. An asset pattern requires at least one
matching asset and keeps only matching assets. Architecture `asset_rules` still classify
those assets; they do not filter releases. Every page is fetched on later syncs, so cached
metadata cannot bypass changed filters or hide an older page's edits.

No match reports `github_release_no_match` in `lastError` and increments the sync failure
count, including when all matching app releases are beta and prereleases are disabled.
There is no fallback to an engine release, `/releases/latest`, or a configured Sparkle feed.
More than ten pages reports `github_release_search_limit` without selecting a partial result.
HTTP/rate-limit/metadata errors also fail the sync. As with other upstream failures, the
last recorded package version/history are preserved; a filter edit clears `latest` and the
conditional cache before the next sync. If GitHub matches successfully, the existing
newest-publication comparison with a separately configured Sparkle feed still applies.

`dry_run: true` validates and previews the effective config on create/update without writing
the app, audit records or sync jobs. It does not query GitHub or predict a selected release.
Actual creation/edits persist these fields and queue sync; subsequent scheduled/manual
version syncs apply the same config. Apply migration **00121** and deploy the API and MCP
generated models together before using the new arguments.

Read-only public GitHub API inspection on 2026-10-29 found these distinct desktop streams:

- [TensorFold desktop `v1.0.0-beta.10`](https://github.com/ashhart/TensorFold/releases/tag/v1.0.0-beta.10)
  contains `TensorFold-1.0.0-beta.10-arm64.dmg` and is a prerelease (as is beta.9).
  [Engine `v1.1.8`](https://github.com/ashhart/TensorFold/releases/tag/v1.1.8) contains
  `tensorfold-1.1.8-macos-arm64.tar.gz`. A DMG glob with prereleases enabled follows the app
  through a future stable release; adding a beta regex would intentionally pin it to beta tags.
- [Rapid-MLX desktop `rapid-mac-v0.16.10`](https://github.com/raullenchai/Rapid-MLX/releases/tag/rapid-mac-v0.16.10)
  contains `rapid-mlx-desktop.dmg` and is stable. [Engine `v0.16.10`](https://github.com/raullenchai/Rapid-MLX/releases/tag/v0.16.10)
  contains `install.sh` and checksum files. Use the literal prefix `rapid-mac-v` and a DMG glob.

Example MCP tool arguments (run `packages_search` first, as for any link addition):

```json
{
  "token": "tensorfold",
  "name": "TensorFold",
  "pricing": "open_source",
  "github_repo": "ashhart/TensorFold",
  "github_asset_pattern": "*.dmg",
  "include_prereleases": true,
  "dry_run": true
}
```

```json
{
  "package": "rapid-mlx",
  "github_tag_prefix": "rapid-mac-v",
  "github_tag_regex": null,
  "github_asset_pattern": "*.dmg",
  "include_prereleases": false,
  "dry_run": true
}
```

For an optional concurrency check on `link_app_update` or `link_app_remove`, send the
Link app's `revision` from `link_apps_list` as `expected_revision`. This counter also tracks
admin edits; it is separate from package content revisions.

Apply database migrations and deploy the API and MCP generated contract together; source changes do not update a running service.

## OAuth connections

The API now provides an OAuth authorization server alongside existing manually issued
Bearer tokens. Apply migration **00108** before starting the updated API. Deploy the
API, admin UI, MCP service and Caddy configuration together after the normal CI gates.
`ADMIN_ORIGIN` is the canonical authorization-server origin; production Compose sets
`OPENNAVO_OAUTH_ISSUER=https://${ADMIN_DOMAIN}` on MCP so its 401 challenge points to
the resource metadata. Keep these origins identical. Direct/local MCP instances can
omit `OPENNAVO_OAUTH_ISSUER` to retain manual-token-only discovery. OAuth production
endpoints require HTTPS; local admin development with an absolute backend URL uses
the Vite `/oauth/` proxy. A relative backend URL such as `/admin-api` retains same-origin
routing (including `dev:prod`); it does not create a proxy with an invented origin.

Clients discover `/.well-known/oauth-protected-resource/mcp` (also available at
`/.well-known/oauth-protected-resource`) and `/.well-known/oauth-authorization-server`.
They register a public client at `POST /oauth/register`, then use authorization code
flow with PKCE S256 at `/oauth/authorize` and `/oauth/token`. Registration accepts
`client_name`, `redirect_uris`, optional `scope`, and `token_endpoint_auth_method: none`.
Only authorization-code/refresh-token grants and the `code` response type are supported.
A previously registered client ID can be reused. Client ID Metadata Documents and
confidential-client authentication are not advertised or implemented in this version.

Redirect URIs must match a registered value exactly, including loopback ports. HTTPS
callbacks and HTTP loopback callbacks are supported; wildcard, fragment and embedded
credential callbacks are rejected. The authorization and token requests must include
`resource` equal to the canonical `ADMIN_ORIGIN` plus `/mcp`. Token and revocation
requests use form encoding, not JSON. State is returned unchanged and the authorization
response includes `iss`. Duplicate parameters are rejected.

Users log into the existing admin UI and explicitly select permissions on
`/oauth-consent`. Only read permissions start selected; deletion is separately opt-in.
The user's current permissions, requested scopes, grant scopes and existing Agent role
permissions all constrain access. OAuth clients and Agent identities have separate IDs; each consent creates a
separate Agent identity for attribution, quotas and administrative disabling.

Authorization codes expire after 5 minutes and are single-use. Access tokens expire
after at most 15 minutes. Refresh tokens rotate on every exchange, with an absolute
30-day grant lifetime. Reusing a consumed code (with its correct PKCE binding) or refresh
token revokes the whole grant, including issued access tokens. Refresh with narrower
scopes narrows the grant permanently. Only credential hashes are stored. Password/session
version changes, owner disabling, permission removal, the global Agent switch and Agent
client disabling are rechecked; OAuth does not bypass existing audit or quotas. OAuth usage is keyed by the stable
OAuth grant ID (in a namespace separate from manual tokens), so rotating access tokens
does not reset call, write or LLM quotas. Manual tokens retain their existing counters.
Deleting a human owner unlinks the owner from retained grant history; access-token
checks and refresh exchange reject grants without a live owner.

Users can revoke grants in **Profile → Connected applications**. The list uses ID
cursor pagination: `GET /oauth/grants` returns `{items, nextCursor}` with up to 100
records; pass the returned cursor as `before` to reach older grants. The UI's **Load
more** action preserves previously loaded records, including after revocation.
Clients can submit
`client_id` and `token` to `POST /oauth/revoke`. Revoking an OAuth access token through
existing Agent administration also revokes the grant and all its refresh tokens. MCP
may cache discovery/introspection for up to 25 seconds; backend business calls always
recheck authorization. Manual Agent tokens continue to work as before.

At the edge, allow machine access to `/mcp`, the two well-known metadata routes and
`/oauth/register`, `/oauth/token`, `/oauth/revoke`; do not serve SPA HTML, interactive
bot challenges, redirects to an edge login, or empty error bodies there. Keep edge
rate limits in place. The application additionally rate-limits registration by source
IP and limits request sizes. Do not log authorization query strings, form bodies or
Authorization headers. This repository change does not alter Cloudflare account rules.

Targeted verification:

```sh
# apps/server (isolated PostgreSQL and Redis containers)
go test -race -tags=integration ./internal/http -run '^TestMCPOAuthLifecycle$' -count=1
# apps/mcp
uv run --locked pytest tests/test_transport.py
# repository root
pnpm --filter @opennavo/admin typecheck
pnpm --filter @opennavo/admin exec vitest run tests/oauth.test.ts
```
