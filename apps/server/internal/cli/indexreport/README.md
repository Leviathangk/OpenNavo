# URL index inventory

Run the existing `/opennavo` binary with database credentials supplied securely through the environment:

```sh
/opennavo index-report -format text
/opennavo index-report -format json > index-report.json
```

Only `DATABASE_URL` is required. `WEB_BASE_URL` is forwarded to the shared intent eligibility helper. The command does not read `.env` files, initialize Redis, call HTTP services, run migrations, or refresh materialized facts. All reads run in a PostgreSQL `REPEATABLE READ, READ ONLY` transaction. A database role restricted to SELECT is recommended. Errors do not print connection strings.

Rows group counts by page type, locale, indexing decision (or noindex reason), and sitemap membership. Membership reflects the current generator's database inputs, **not** an HTTP-fetched sitemap or search engine index coverage. In particular, versions decisions use current release notes while sitemap membership uses stored page-state notes, matching the two existing readers.

Scope:

- Current cask/link routes, including hidden or disabled records, across all site locales.
- Font overview URLs are counted under `fonts` instead of `overview`; their subpages remain in the matching subpage groups.
- Unfiltered versions pagination is included; other entity and static routes count their base page only.
- Removed/alias URLs, search/filter variants, directory pagination and A–Z bucket routes are excluded.
- `machineOnlyOverview` counts complete non-source published machine translation snapshots in open locales, excluding hidden apps. It describes stored translation status, not whether a person ever reviewed the text.

The report can be expensive on a large catalog: it reads current timeline facts to avoid reporting stale page decisions. It streams database work in batches of 100 packages but retains only aggregate counts. Run it as an explicit maintenance task, not on each HTTP request.
