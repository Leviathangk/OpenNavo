# GEOInflow integration

The web client supports an optional, self-hosted GEOInflow tracker alongside GA4.
Set these in the deployment's private environment file (not in Git):

```dotenv
NUXT_PUBLIC_GEOINFLOW_SCRIPT_URL=https://stats.example.com/script.js
NUXT_PUBLIC_GEOINFLOW_WEBSITE_ID=<website-uuid-from-installation-page>
GEOINFLOW_ORIGIN=https://stats.example.com
```

Use the website UUID, not its numeric dashboard handle. The UUID is a public
collection identifier; never put an administrator password or API token in these
variables. `GEOINFLOW_ORIGIN` must be the exact HTTPS origin without a path and
allows the script and collection requests through the web-only Caddy CSP.
Empty settings disable this integration.

For the existing BaoTa deployment, updating `ops/docker-compose.prod.yml` alone
does not update the host-owned configuration. Pass both public variables through
`deploy/docker-compose.baota.yml` and the active `deploy/rolling.compose.json`
web service. The release updater clones that active service for future rollouts.
Add the analytics HTTPS origin to both `script-src` and `connect-src` in the
host's `deploy/caddy/Caddyfile.baota` web-only policy, validate and reload Caddy.
Recreate the active web service using the already validated release image.
Invalidate Nitro HTML route caches and then Cloudflare's cached HTML: changing
runtime variables alone does not replace HTML cached under the same build ID.
Never flush unrelated Redis data. Verify the rendered runtime values, an actual
browser `/api/send` response, and received events; a healthy container alone
does not prove analytics is enabled.

GEOInflow also requires trusted browser identity for its visitor reports. With
`CLIENT_IP_HEADER=x-liekan-client-ip`, the reverse proxy must explicitly overwrite
`X-Liekan-Client-IP` with the validated client address. For Cloudflare, trust only
its published TCP peer ranges when interpreting `CF-Connecting-IP`, then pass
Nginx's `$remote_addr` upstream. Keep the origin peer restriction in place.
Do not trust an arbitrary incoming `X-Liekan-Client-IP` or forwarded header.
Ensure panel-generated proxy changes retain this location-level header.

An HTTP 200 from `/api/send` can still store `browser_unresolved` events that
do not count as visitors. Verify a new event has browser identity available and
appears in `liekan_effective_browser_event` and the visitor dashboard. Historical
events missing trusted identity must not be relabeled as verified visitors.

When auditing reports, reuse the authenticated session and follow asynchronous
job polling rather than issuing repeated forced refreshes. GEOInflow v1.2 limits
an actor to two concurrent analysis tasks. HTTP 429 with
`analysis-concurrency-limited` requires waiting and retrying, as its frontend
does; it does not mean ingestion failed. Dashboard snapshots can temporarily
show `dashboard-refresh-failed` while waiting for capacity. HTTP heartbeat
monitoring does not validate identity qualification or report completion, so
check those explicitly after proxy changes and releases.

GA4 and GEOInflow start in every region without an automatic consent prompt.
The footer settings retain an explicit opt-out, and saved refusals remain effective.
The tracker starts after mounting and idle scheduling, sends one page view per
pathname navigation, and forwards `get_app`, `copy_install_command`, `open_in_app`
and `download_client`. URLs and referrers exclude query strings and fragments.
GEOInflow respects Do Not Track. No user identification is sent.

Release through the normal GitHub Actions gates. Both the web runtime environment
and Caddy configuration must roll out. After release, accept analytics in a normal
browser, navigate between pages and use an installation action; check the
GEOInflow installation status and browser network requests. Declining analytics
must prevent subsequent tracking. Do not use synthetic crawler requests as
evidence of real AI visibility.

## Origin logs and catalog

The production collector is a separate host systemd service; the GEOInflow app,
database and workers remain in one Docker container. Signed collector artifacts
are persisted outside the container. The collector reads a dedicated Combined
log for public web domains only, excluding queries and referrers. It has read-only
log access, encrypted buffering, resource limits and log rotation. Raw log IPs
are still personal data; keep retention short and access restricted.

The collector sees requests reaching Nginx, including bots that do not execute
JavaScript. Cloudflare cache hits and requests blocked at the edge are absent.
User-agent classifications do not authenticate a crawler's identity, and these
counts are separate from browser analytics consent.

GEOInflow v1.2 rejects encoded URL paths longer than 500 characters. Such requests
produce a format warning even when the Combined syntax is valid; other records
continue processing. Do not truncate paths to suppress this warning, since that
would merge distinct pages. Treat affected requests as a collection gap.
The dedicated production log excludes `/_og/` image-render requests, whose
encoded image parameters routinely exceed this limit. OG image delivery remains
unchanged, but those asset requests are intentionally absent from GEOInflow.

Import actual sitemap URL sets into the content catalog; expand sitemap indexes
first. Import in bounded batches, preserve real last-modified dates, and do not
invent titles, word counts or structured-data scores. Catalog imports upsert
entries; they do not remove URLs that disappear from the sitemap.

Operational scripts, pairing codes, credentials, collector configuration and
backups belong outside this repository. To disable browser analytics, clear both
public tracker variables and roll out the web service. Disable the collector
service and its scoped Nginx logging separately if origin-log collection is no
longer wanted.

## GEOInflow v1.2.0 large catalog hotfix

The AI pages report includes the full `tables.blindList`; its UI paginates this
list in the browser. A catalog of about 33,000 URLs exceeds the upstream 2 MiB
analysis envelope limit even for a one-day query. Shorter date ranges cannot
fix this case.

The production derivative image `geoinflow:production-1.2.0-report-limit` raises
`encodeAnalysisEnvelope`'s limit to 8 MiB only when `value.resource` is `ai/pages`.
Other reports retain the 2 MiB limit. The two database constraints on
`liekan_analysis_query.result_bytes` and `liekan_dashboard_snapshot.result_bytes`
must also allow 8 MiB only for `target='feature:page-report'` with
`parameters->>'resource'='ai/pages'`; otherwise successful computation still
fails when saving the result. No catalog or raw-log records are removed.

The host operations directory contains the original source, schema backups,
transactional SQL patches, worker bundle, derivative Dockerfile and original
Compose file. Build the worker with the source tree **and tsconfig.json** so
esbuild resolves the `@/` alias. Verify 33,000 entries survive serialization,
other reports still reject results above 2 MiB, and AI pages rejects above 8 MiB.
Verify today, seven-day and thirty-day reports through the authenticated API.

This is a bounded compatibility fix, not server-side pagination. Preserve it
when upgrading until upstream supports large catalogs. A future catalog can
still exceed 8 MiB. Roll back the image with the saved Compose file; restoring
the original database constraints additionally requires handling oversized
cached query/snapshot rows first, without deleting raw analytics or catalog data.

## Edge and origin protections

GEOInflow dynamic pages/APIs bypass Cloudflare cache; static Next.js assets,
collector downloads and `script.js` retain their existing cache behavior.
Cloudflare Free Managed Rules are enabled. Two broad browser-version/protocol
challenge rules were disabled because they rejected ordinary clients.

Nginx's per-site extension rejects non-Cloudflare TCP peers using
`$realip_remote_addr`; loopback remains permitted for local checks. The trusted
CIDRs come from Cloudflare's official IP list and require maintenance when that
list changes. Forwarded headers alone do not grant access. This restricts HTTP
requests at Nginx, not SSH or all host ports.

See [HTTP availability probes](prometheus/http-probe.md) for the host service and
existing Prometheus alerts that distinguish origin failures from cached edge
responses. No email or webhook notifications are configured.
