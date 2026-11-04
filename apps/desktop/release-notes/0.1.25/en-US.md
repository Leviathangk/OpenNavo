## New

- The menu bar monitor can now keep running after you quit OpenNavo. It is on by default, and a one-time notice explains where to turn it off; check its status in Settings → System Monitor. Click a menu bar item to see its readings without opening the app. Fan values are shown read-only.
- Local MCP can now queue updates for apps it already lists and clean regenerable caches, always through a confirmation window and the local activity log. Confirmation windows also show the caller's Team ID.

## Improvements

- Destructive actions use OpenNavo's confirmation windows: the plan is checked again before it runs, Allow arms after a short delay, and closing or leaving the window denies.
- Cleanup never removes more than the review showed. Items whose size could not be fully measured are listed unselected as "at least".
- Update notifications now name only the visible, newly available updates.
- Keyboard-only users can confirm with Command-Return while Allow has focus; Return, Space and Escape still deny.
- Deep links open only public URLs; busy installs and firewall confirmations now fail safely instead of retrying or approving.

## Fixes

- Hardened Local MCP confirmations against navigation, refocus and crash bypasses, and kept denied requests in the audit log.
- Usage reporting continues after a rejected record or a redirect.
- The Bundle ID label and moved-app warnings are correct in every language.
