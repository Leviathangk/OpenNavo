## Improvements

- Reduce recurring CPU, filesystem and database work in background security scans, firewall activity and fan monitoring while retaining five-second fan keepalives.
- Retry rejected startup probes on later scans, detect atomic download replacements and keep fallback polling paced when filesystem events disconnect.
- Improve resource-monitor sampling, process readings and chart updates.
- Improve App Store installation handling for package signatures, install paths and administrator prompts.
- Collect anonymous daily usage automatically, including background operation. Reports contain a random installation identifier, time, platform, app version and language; the previous Settings toggle has been removed.

## Known issue

- A SQLite SIGBUS crash observed during earlier validation has not been conclusively reproduced and resolved.
