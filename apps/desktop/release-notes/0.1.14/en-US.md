## Improvements

- Speed up full security scans with two isolated workers and a clean-file cache. Show live progress and let users force a fresh scan.
- Add process grouping by app and a CPU usage breakdown, including completed child processes and OpenNavo itself.
- Explain signature issues in a dedicated view and offer the existing uninstall review flow.
- Improve catalog synchronization, update failure details, and selection of Mac downloads by chip.
- Let users clear finished task history while preserving records that can still be undone or rolled back.
- Explain why administrator access is requested and localize macOS permission text in all six supported languages.

## Known issue

- A SQLite SIGBUS crash observed during earlier validation has not been conclusively reproduced and resolved.
