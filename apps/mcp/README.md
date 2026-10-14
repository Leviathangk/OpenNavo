# OpenNavo MCP

Python 3.13 + uv, using FastMCP 4.0.x. The 82 tools and 8 resources adapt only the internal `/agent-api`.

Run `uv sync --locked` in this directory. From the repository root, run `make gen-mcp lint-mcp test-mcp build-mcp` to generate and check artifacts. `make gen` and `make lint test` also include MCP. The authoritative tool manifest is `src/opennavo_mcp/tools/manifest.py`.

Before startup, supply `AGENT_GATEWAY_SECRET` through the process environment, matching the API secret. Optionally set `OPENNAVO_AGENT_API_BASE` (default: `http://127.0.0.1:8080/agent-api`). Run `uv run --locked opennavo-mcp` to listen at `127.0.0.1:8790/mcp`; clients send Bearer tokens. stdio requires `OPENNAVO_MCP_TRANSPORT=stdio` and `OPENNAVO_TOKEN`. No .env files are loaded automatically. Never put real tokens in configuration examples or command-line arguments.

Top-level tool parameters use snake_case; nested fields retain OpenAPI camelCase. Write tools accept `dry_run=true`. All business output is wrapped in `untrusted`. Go performs final permission, deletion-allowance and quota checks; MCP caches introspection for 25 seconds. Go downloads URL uploads; MCP never contacts image sites. For local files, `asset_upload_ticket` returns a single-use `uploadUrl` under the admin origin's `/admin-api`; the client POSTs the file there directly (for example `curl -sS -F file=@icon.png '<uploadUrl>'`), so image bytes never pass through MCP or the model. Go stores only the ticket hash, expires it after 10 minutes and rechecks the token on upload.

The HTTP entry point trusts no forwarded IPs by default. Behind a reverse proxy, set `OPENNAVO_MCP_TRUSTED_PROXIES` to a JSON array of trusted CIDRs; never trust uncontrolled proxies. HTTP and stdio use the same permission middleware. `/healthz` returns liveness only.

Production Compose/Caddy integration is provided. Exercise deployment boundaries from the repository root with `node tooling/scripts/tasks/server.mjs prod-verify --mcp-only`.

Article management covers all 15 admin article and author operations. Grant `content:article:edit` for drafts, review submission, revision reversion and author lookup; grant `content:article:publish` separately for publication, withdrawal/restoration, slug and language visibility changes, and author creation/updates. Article mutations retain `expected_revision` checks and support `dry_run`. Apply migration 00026 when upgrading the backend; existing token permissions remain unchanged and must be explicitly updated to grant publication access.
