# Probe pacing and hot page caches

This policy uses the existing Nitro cache storage, content change feed and
Cloudflare response headers. It grants no exemption from load shedding. The
following defaults are proposed for owner review in this PR; deploying them is
a separate operation.

| Request | Origin retention / freshness | Browser and CDN behavior | Invalidation |
| --- | --- | --- | --- |
| `/_og/` | Existing image pool, six-hour storage ceiling | Explicit 30-day `max-age` and `s-maxage`, `immutable` | Signed URL includes localized image inputs, app version and Nuxt build ID; content changes purge the containing HTML |
| Every locale's `/recent` | 60 seconds fresh, 300 seconds SWR | Browser revalidates; Cloudflare 60 seconds fresh, 300 seconds SWR and stale-if-error | Catalog changes purge localized page prefixes, including pagination and payloads, at the origin and edge |
| `/apps/*/versions` in every locale | Six hours fresh, up to 18 hours stale | Existing two-day Cloudflare TTL, seven-day SWR and stale-if-error | Existing app-prefix purge covers versions, filters, pagination and payloads |

The origin image retention remains shorter than the CDN TTL deliberately: the
separate Redis/LRU image pool can evict images without forcing a new render at
the CDN. Its existing maximum is 32 entries / 16 MiB in process; pages retain
their 128-entry / 32 MiB ceiling. Entries over 512 KiB are still rejected.
No new response cache or unbounded per-URL map is added. The build namespace
keeps replacement releases separate. Old signed image URLs remain immutable;
updated HTML points to new URLs instead of purging the entire image namespace.

Locale comes from the path and image props, never a visitor's language cookie.
Nitro keys include the normalized URL, including contract query parameters.
Cloudflare must keep the existing path-based key and the query allowlist in
`ops/cloudflare/html-cache-key.json`. OG keeps its module's `Vary: host,
accept-encoding`; no `Vary: Accept-Language` is needed for explicit locale URLs.
Only successful renders are stored. All error responses, including signature
failures and 429/503, send `no-store`; a failed revalidation preserves a prior
successful page instead of replacing it with an error.

## Probe budgets

- IndexNow public probes remain globally serialized. `INDEXNOW_PROBE_INTERVAL`
  defaults to two seconds (previously one); zero selects the default, with
  positive overrides up to five seconds. A page gets at most three HTTP
  attempts within ten seconds, including pacing. 429 and every 503 honor
  `Retry-After` seconds or HTTP dates up to `INDEXNOW_PROBE_MAX_RETRY_AFTER`
  (default `15m`; zero selects the default; values above `24h` clamp with a
  warning); absent, invalid or negative headers
  use exponential backoff with jitter. The capped delay is persisted when it
  exceeds the run budget. Exhaustion and transport failures defer the existing
  outbox row
  without spending delivery attempts or declaring the page removed. The
  existing durable exponential cooldown survives worker restarts. Reads and
  writes persistently cap old probe and purge deadlines, so repeated reads do
  not extend recovery. Legacy outbox deadlines beyond 24 hours are shortened
  to the configured cap when claiming work; normal content waits and next-day
  announcement budgets retain their schedules.
- The internal `/cache/probe` endpoint shares one active batch per web process
  by default (`NUXT_PROBE_CONCURRENCY`, clamped to 1–4), with no waiting queue.
  Each batch handles at most ten paths sequentially, three attempts per path
  and ten seconds across the whole batch. It returns `temporaryFailures` with
  retry delays alongside HTTP statuses. A single process-wide cooldown also
  prevents later batches from ignoring a long `Retry-After`, without retaining
  per-URL state. `NUXT_PROBE_MAX_RETRY_AFTER_SECONDS` caps server-provided
  cooldowns at 900 seconds by default. Capacity refusals are uncached 503s.
  Timed-out local requests retain their concurrency slot until the underlying
  request settles or a hard 30-second slot deadline expires. Expiry releases
  capacity and logs a warning; late settlement cannot release it a second time.
  Retry cap configuration above 24 hours clamps to 24 hours with a warning.
- The optional Prometheus HTTP exporter changes from four workers to one.
  Its local JSON accepts `"probe": {"concurrency": 1, "maxAttempts": 3,
  "budgetSeconds": 12, "maxRetryAfterSeconds": 900}`. Concurrency is bounded to
  1–4, attempts to 1–3 and the entire scrape to 1–12 seconds, below its documented
  15-second timeout. Server-provided cooldowns default to a 15-minute cap;
  configured caps above 24 hours clamp to 24 hours with a warning.
  Per-target cooldown slots are bounded by the eight configured targets and
  retained across scrapes. Long waits skip HTTP requests on subsequent scrapes.
  `temporary_failure` and `retry_after_seconds` metrics distinguish deferred
  checks from definitive page statuses; `success` remains zero until a new
  successful check, preserving existing availability alerts. A slow target can
  use the scrape budget and defer later targets; owners can raise concurrency
  within the bound after measuring the origin load.

## Owner decisions

Confirm the recent-page freshness/stale windows, the 30-day immutable image
headers, and retaining the existing long CDN lifetime for versions pages.
Confirm the reduced probe rate and retry budgets, and whether the optional
monitor should use concurrency one or two to balance origin pressure against
coverage of slow targets. Catalog edits now add one recent-page purge prefix
per affected locale, which shares the existing Cloudflare purge quota and
durable purge queue. No Cloudflare rule, server configuration, monitoring host
or production data is changed by this PR.
