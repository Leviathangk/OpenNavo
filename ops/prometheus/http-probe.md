# HTTP availability probes

The optional host service `opennavo-http-probe` exposes configurable lightweight probes to
the existing Prometheus instance. It requires Python 3 and curl, not another
container. Install `http-probe.py` under `/opt/opennavo-monitoring/` and install
`http-probe.service` as `/etc/systemd/system/opennavo-http-probe.service`.

Create `/opt/opennavo-monitoring/probes.json` outside the repository:

```json
{
  "listen": "<monitoring Docker bridge gateway>",
  "port": 19192,
  "probe": {"concurrency": 1, "maxAttempts": 3, "budgetSeconds": 12, "maxRetryAfterSeconds": 900},
  "targets": [
    {"service": "web", "layer": "origin", "url": "https://example.org/", "resolve": "example.org:443:127.0.0.1"},
    {"service": "web", "layer": "edge", "url": "https://example.org/"}
  ]
}
```

Production checks the web homepage, API `/healthz` and admin homepage over local
TLS with correct SNI. An additional probe checks the public web homepage.
Origin probes bypass CDN caching.
Only HTTP 200 counts as healthy; redirects, TLS failures and timeouts fail.
These are availability checks, not full functional/database checks.

The default concurrency is one. Retries for 429/503 and transport failures share
a twelve-second scrape budget and never exceed three attempts per target.
`Retry-After` seconds/dates take precedence over exponential backoff with jitter,
up to `maxRetryAfterSeconds` (default 900 seconds, or 15 minutes). Configured
values above 86400 seconds clamp to 24 hours and log a warning. Invalid or
negative headers use the default backoff. Capped cooldowns persist across
scrapes in at most eight target slots. Deferred
checks export `temporary_failure=1` and `retry_after_seconds` while `success=0`
keeps the existing availability alert active. See [the cache and probe policy](../hot-path-cache.md)
for limits and the slow-target coverage tradeoff.

Bind to the private Docker bridge gateway only. Add an `http-probe` scrape job
with that gateway's port 19192, interval 30s and timeout 15s. Merge the group from
`http-probe-rules.yml` into the mounted rules file. Keep existing groups/jobs,
back up both mounted files and preserve their inodes when writing updates.
If the host firewall drops container-to-host traffic, allow TCP port 19192 only
from the monitoring bridge subnet to its gateway; do not open it publicly.
Validate with `promtool check config` before sending Prometheus SIGHUP. The
deployment updater preserves unrelated scrape jobs.

View `opennavo_http_probe_success`, `opennavo_http_probe_status_code` and
`opennavo_http_probe_duration_seconds` in the existing Prometheus query screen.
Its Alerts screen shows `HTTPProbeFailed` and `HTTPProbeExporterDown`; failures
must persist two minutes. No external notification receiver is configured.

Run `python3 -m unittest discover -s ops/prometheus -p 'test_http_probe.py'`
and, from this directory, `promtool test rules http-probe-rules.test.yml`.
The alert test covers an unavailable origin hidden behind a healthy CDN cache.

This monitor runs on the same server and cannot independently notify about a
complete server outage. Changing the Docker network requires updating both the
listen address and scrape target. To roll back, restore the backed-up Prometheus
files, reload Prometheus, then disable the systemd service.
Remove the corresponding private firewall allowance when retiring the service.
