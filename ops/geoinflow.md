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

The tracker shares the existing analytics region/consent decision and withdrawal
controls. It starts after mounting and idle scheduling, sends one page view per
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

Import actual sitemap URL sets into the content catalog; expand sitemap indexes
first. Import in bounded batches, preserve real last-modified dates, and do not
invent titles, word counts or structured-data scores. Catalog imports upsert
entries; they do not remove URLs that disappear from the sitemap.

Operational scripts, pairing codes, credentials, collector configuration and
backups belong outside this repository. To disable browser analytics, clear both
public tracker variables and roll out the web service. Disable the collector
service and its scoped Nginx logging separately if origin-log collection is no
longer wanted.
