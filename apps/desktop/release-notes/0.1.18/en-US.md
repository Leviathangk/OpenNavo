## Improvements

- Keep library reads, update preferences and tray refreshes from blocking the window while waiting for disk access or locks.
- Recheck stale native update candidates after restarting or remounting, without skipping scheduled checks and automatic upgrades.
- Prevent older responses from overwriting newer task, update and firewall state; preserve rule and blocklist changes during loading.
- Improve Traditional Chinese text and reduce repeated update-source lookups during application inventory reads.

## Known issue

- A SQLite SIGBUS crash observed during earlier validation has not been conclusively reproduced and resolved.
