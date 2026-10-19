## New features

- Add an optional outbound application firewall with per-app and destination rules, connection prompts, activity history, and blocklists. Enable it in OpenNavo and approve the macOS system extension and network filter when prompted.
- Provide firewall controls and explanations in all six supported languages. If the extension cannot connect, the app reports its status and any required restart.

## Improvements

- Improve menu-bar panels so they open without bringing the main window forward and dismiss consistently.
- Improve App Store account-status messages and report a missing download helper explicitly.
- Keep startup-item switches responsive during scans and prevent stale scan results from reverting changes.
- Protect live SQLite files and their aliases from security scans to preserve database locks.
- Expand the website's security pages and keep eligible apps paused only by the catalog freshness rule available for search indexing.

## Firewall limits

- The firewall controls outbound TCP and UDP connections. Unknown connections are allowed when the app is unavailable or a prompt times out; this is not a fail-closed network boundary.
- Domain rules need a hostname supplied by macOS. Connections without one can match IP, port, and app rules instead.

## Known issue

- A SQLite SIGBUS crash was observed during earlier validation. This version adds database-lock protections and regression tests, but the original crash has not been conclusively reproduced and resolved.
