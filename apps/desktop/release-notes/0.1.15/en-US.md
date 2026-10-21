## Improvements

- Add optional anonymous usage statistics for running Mac installations, including background operation. Collection is off by default and can be enabled in Settings; reports contain only a random identifier, time, platform, app version, and language. Turning it off clears the local identifier and pending reports.
- Check for OpenNavo updates hourly while automatic checks are enabled, instead of stopping after the first successful check of the day.
- Keep startup Homebrew refreshes from suppressing scheduled checks across all selected update sources.

## Known issue

- A SQLite SIGBUS crash observed during earlier validation has not been conclusively reproduced and resolved.
