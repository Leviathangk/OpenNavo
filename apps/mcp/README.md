# OpenNavo MCP

Python 3.13 + uv, using FastMCP 4.0.x. The 115 tools and 10 resources adapt only the internal `/agent-api`.

Run `uv sync --locked` in this directory. From the repository root, run `make gen-mcp lint-mcp test-mcp build-mcp` to generate and check artifacts. `make gen` and `make lint test` also include MCP. The authoritative tool manifest is `src/opennavo_mcp/tools/manifest.py`.

Before startup, supply `AGENT_GATEWAY_SECRET` through the process environment, matching the API secret. Optionally set `OPENNAVO_AGENT_API_BASE` (default: `http://127.0.0.1:8080/agent-api`). Run `uv run --locked opennavo-mcp` to listen at `127.0.0.1:8790/mcp`; clients send Bearer tokens. stdio requires `OPENNAVO_MCP_TRANSPORT=stdio` and `OPENNAVO_TOKEN`. No .env files are loaded automatically. Never put real tokens in configuration examples or command-line arguments.

Top-level tool parameters use snake_case; nested fields retain OpenAPI camelCase. Write tools accept `dry_run=true`. All business output is wrapped in `untrusted`. Go performs final permission, deletion-allowance and quota checks; MCP caches introspection for 25 seconds. Go downloads URL uploads; MCP never contacts image sites. For local files, `asset_upload_ticket` returns a single-use `uploadUrl` under the admin origin's `/admin-api`; the client POSTs the file there directly (for example `curl -sS -F file=@icon.png '<uploadUrl>'`), so image bytes never pass through MCP or the model. Go stores only the ticket hash, expires it after 10 minutes and rechecks the token on upload.

The HTTP entry point trusts no forwarded IPs by default. Behind a reverse proxy, set `OPENNAVO_MCP_TRUSTED_PROXIES` to a JSON array of trusted CIDRs; never trust uncontrolled proxies. HTTP and stdio use the same permission middleware. `/healthz` returns liveness only.

Production Compose/Caddy integration is provided. Exercise deployment boundaries from the repository root with `node tooling/scripts/tasks/server.mjs prod-verify --mcp-only`.

Article management covers all 16 exposed article and author operations. Grant `content:article:edit` for drafts, optional review submission, draft revision reversion and author lookup; grant `content:article:publish` separately for publication, withdrawal/restoration, reverting to a published revision, slug and language visibility changes, and author creation/updates. Article mutations accept optional `expected_revision` checks and support `dry_run`. Existing token permissions remain unchanged and must be explicitly updated to grant publication access.


Business writes apply directly after token authorization. Tasks, fencing, evidence, SEO
proposal/action switches, shared entity quotas and breakers are not prerequisites. Previews
(`dry_run`), retry keys (`idempotency_key`) and revision checks (`expected_revision`) are optional.
The same applies to translations of all entities, keyword writes and asset uploads. Explicit
translation edits may replace manual text; provenance and revision history are retained.

For arbitrary apps, search first, then call `link_app_add` with `token`, `name` and at least one
of `homepage`, `download_url`, or `github_repo`. Complete metadata with `package_set_text`,
`package_update_meta`, `package_set_categories`, image upload/attachment and `translations_write`.
Use `link_app_update` for later source changes. Use `tap_package_add` for supported third-party
Homebrew casks and `package_refresh` to queue a sync for existing Homebrew/tap/link entries. `package_versions`
lists versions eligible for `release_write_notes`; writing notes does not create a version.
Listing a link app does not guarantee a one-click installer or a usable update feed.

For an optional concurrency check on `link_app_update` or `link_app_remove`, send the
Link app's `revision` from `link_apps_list` as `expected_revision`. This counter also tracks
admin and candidate publication edits; it is separate from package content revisions.
Task traces expose it under `revisions["link:<package ID>"]`.

`seo_listing_upsert` remains an optional candidate workspace and can publish without evidence,
identity baselines or complete editorial metadata. Apply migration **00098** with the backend:
it adds Link edit revisions, removes candidate download/identity triggers and preserves exact keyword variants as separate
observations. Previously collapsed keyword variants must be reimported from their source.
Deploy the API and MCP generated contract together; source changes do not update a running service.
