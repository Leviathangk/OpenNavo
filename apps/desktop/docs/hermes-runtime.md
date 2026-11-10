# Hermes runtime integration

OpenNavo uses a pinned Hermes Agent runtime as an optional sidecar. The runtime
is not bundled in the app; the assistant asks before installing it.

## Build

The runtime is generated, not committed:

```bash
pnpm --filter @opennavo/desktop hermes:prepare
pnpm --filter @opennavo/desktop hermes:package
```

`hermes:prepare` builds a standalone CPython 3.14 + `hermes-agent==0.23.0` +
`CuaDriver.app` under `apps/desktop/src-tauri/resources/hermes`.
`hermes:package` creates the release tarball and SHA-256 under
`apps/desktop/hermes-dist/`; upload that artifact to the GitHub Release and set the app's
`HERMES_RUNTIME_URL` (or update `DEFAULT_RUNTIME_URL` in `install.rs`).

Development can install from the prepared directory with
`HERMES_RUNTIME_SOURCE=/path/to/resources/hermes` instead of downloading.
`HERMES_RUNTIME_SHA256` optionally verifies a downloaded package.

## Data root

All Hermes runtime/state lives under:

```text
~/Library/Application Support/com.opennavo.desktop/OpenNavoData/
```

- `runtime/hermes/` — optional runtime
- `hermes/` — HERMES_HOME, sessions, memory, logs, audit

API keys are stored in the macOS Keychain (`com.opennavo.desktop.hermes`). The
gateway receives the resolved key at process start through an inherited file
descriptor and keeps it only in an in-memory `os.environ` overlay; it is never
written to `hermes/`, `.env`, `config.yaml`, or the process exec environment.
WebKit's own WebsiteData remains under `~/Library/WebKit/com.opennavo.desktop/`.

## Runtime contract

- `hermes_runtime_status` reports whether the runtime is installed, its
  version, the single `OpenNavoData` path, and whether the computer-use driver
  is present.
- `hermes_runtime_install` downloads/copies the runtime into
  `OpenNavoData/runtime/hermes`, emits `hermes-install-progress`, and verifies
  the optional SHA-256. `hermes_runtime_uninstall` removes `OpenNavoData` and
  deletes the provider Keychain items; CuaDriver and macOS TCC grants are left
  alone.
- `hermes_chat` starts a local `hermes serve` gateway and streams
  `message.delta` frames from `/api/ws` as `hermes-chunk` Tauri events. The
  final `message.complete` frame becomes the persisted assistant message.
- The desktop uses the gateway only. CLI fallback is intentionally disabled so
  steering, approvals and tool events always share one transport contract.
- Full control enables the session-scoped gateway YOLO flag; guarded and
  safe modes do not, and approvals are denied unless the user grants
  them. The gateway toolsets are selected with `HERMES_TUI_TOOLSETS`.
- `hermes_stop` interrupts the active child and stops the gateway.
- Provider keys live in the macOS Keychain (`com.opennavo.desktop.hermes`);
  the commands are `hermes_secret_set`, `hermes_secret_status`, and
  `hermes_secret_delete`. Before each turn the app reads the key, removes any
  app-managed row older builds wrote to `HERMES_HOME/auth.json` (and any managed
  line in `.env`), then starts the runtime with the key on an inherited file
  descriptor. A bootstrap installs an `os.environ` overlay before Hermes imports:
  lookups by name see the key, while iteration, `copy()`, and child process
  environments do not. The key is also absent from the exec environment, so
  `ps -E` cannot show it. Hermes' own OAuth tokens and the short-lived Copilot
  exchange JWT still live in `auth.json`; a `terminal` tool (or screen control)
  running as the user can read those, so the Mac control settings warn when
  Terminal or screen control is enabled. Hermes' file tool denies `auth.json` as
  defence in depth, not as a boundary.
- Each conversation stores the gateway's `stored_session_id` and resumes it
  with `session.resume`, so follow-up turns keep agent context.

## Mac control and TCC

`terminal` and `file` work through Hermes. `computer_use` additionally needs
the optional runtime's `CuaDriver.app`, which must be granted:

- Accessibility
- Screen Recording
- Full Disk Access only for tools that read those locations

TCC cannot be granted programmatically. The settings UI labels these permissions
as first-use/on-demand and opens System Settings; validate on a signed build and
a clean Mac. The runtime package contains `CuaDriver.app` (signed by TryCua); TCC grants
name CuaDriver. The optional runtime is installed outside the signed app, so
the release pipeline must checksum the package; the app verifies its SHA-256
when `HERMES_RUNTIME_SHA256` is configured.

## Model ids

The settings UI shows product-facing model labels and sends canonical provider
ids from `apps/desktop/src/hermes/providers.ts`. The model field accepts a raw
provider id as well, so a provider-specific model that is not in the table can
still be used.

## Known gaps

- The gateway streams token deltas and tool/reasoning events.
- Skills/tools/cron are viewable and have basic management; MCP server config editing is not exposed.
- The model catalog is official metadata when available, with a clearly-marked static fallback.
- Computer-use TCC validation and notarization are release-environment work.
- Hermes's own session store is used for continuity; OpenNavo keeps the visible
  transcript for history and search.
