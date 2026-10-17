# Contributing to OpenNavo

Read the [README](../README.md) for toolchain versions and local setup. Report security issues privately using [SECURITY.md](SECURITY.md), not a public issue.

## Contribution license

By submitting a contribution for inclusion in OpenNavo, you license your own
contribution under GNU GPL version 3 only (`GPL-3.0-only`), the same license as
the project (inbound = outbound). See [LICENSE](../LICENSE) and the additional
terms under GPLv3 section 7 in [NOTICE](../NOTICE). Retain the original notices
and licenses of any third-party material you contribute.

## Development workflow

1. Describe the problem and intended behavior in an issue or pull request. Keep unrelated changes separate.
2. Run `make setup`, then `make infra migrate seed` for backend work. Use the fake Homebrew executable for desktop write operations; never test real installs, upgrades, removals, cleanup, or updates on a contributor's machine.
3. Add a focused regression test for changed behavior. Run the relevant package checks while iterating and `make lint test` before submitting. Browser tests require separate setup; browser interaction in agent-assisted work must use browser-use CLI.
4. Use Conventional Commits, such as `fix(server): honor local configuration overrides`. Explain the problem, resulting behavior, test evidence, and remaining limitations in the pull request.

The local configuration precedence is explicit process environment, then `apps/server/.env.local`, then nonempty `apps/server/.env.example` defaults. Keep secrets in ignored local configuration. Never copy production credentials or data into fixtures.

Rustup uses the exact version in `apps/desktop/src-tauri/rust-toolchain.toml` for local builds. CI and desktop releases use the same version. Cargo's `rust-version` declares a minimum supported compiler; it does not select the release compiler. A toolchain upgrade must update the pin, both workflow installers, and standard-library notice evidence together; follow [THIRD_PARTY.md](../docs/legal/THIRD_PARTY.md).

## Repository layout and tools

Applications and services live in `apps/`, shared libraries in `packages/`, deployment files in `ops/`, and development scripts/configuration in `tooling/`. Documentation, brand source, and canonical third-party evidence live in `docs/`.

Run `pnpm format:check` or `pnpm format` from the repository root. Package `lint` scripts load the shared configuration explicitly; admin retains its own configuration. Editor integrations must use `tooling/config/` and the repository root as the working directory, or invoke these package scripts instead of relying on root configuration discovery.

Run Cargo commands from `apps/desktop/src-tauri/` so Rustup selects the pinned toolchain. The unified `make` and package generation commands already do this. If you moved an existing MCP virtual environment from the old directory, run `uv sync --locked --reinstall` in `apps/mcp/` to refresh its executable paths.

## Source and generated files

- Change `apps/server/api/*.openapi.yaml` before implementing contract changes. Run `make gen` and include generated outputs; do not hand-edit bindings.
- Add migrations instead of rewriting applied migrations. Keep application and migration database roles separate.
- Use `packages/tokens/tokens.json` for visual tokens and regenerate derived artifacts.
- English is the default for documentation, source comments, errors, and developer tooling. Preserve explicit localized product content and multilingual fixtures. New product content defaults to English; existing Chinese-authored content retains explicit `zh-CN` metadata.
- Keep user-facing copy in locale catalogs, with all six languages. `tooling/scripts/gen-locales.mjs` generates shared locale metadata from `packages/shared/src/locales.json`. Translation locks record explicit source content; do not reset them merely to change the authoring default.
- `make i18n-sync` may call a configured translation provider. Do not invoke it implicitly during offline checks or use external AI services to translate repository comments.
- Retain upstream copyright notices and license texts. Dependency changes require regenerating the inventory and notices described in [THIRD_PARTY.md](../docs/legal/THIRD_PARTY.md).

## Security checks

```sh
node --test tooling/scripts/tasks/shared.test.mjs tooling/scripts/check-secrets.test.mjs
node tooling/scripts/check-secrets.mjs --worktree
node tooling/scripts/check-secrets.mjs --staged
node tooling/scripts/check-secrets.mjs --history
```

The scanner pins Gitleaks and needs Go plus module-download access on first use. Staged mode scans index contents; worktree mode scans tracked and unignored files; history mode scans all locally reachable Git history. Findings print paths, line numbers, and rule IDs without secret values. Never add broad allowlists to silence findings. Generated translation hashes and OpenAPI data have narrow path-and-line exceptions. A clean scan is not proof that no secret exists.

GitHub Actions results must actually run before being described as passing. Record billing, runner, network, platform, or unavailable-service limitations explicitly. Do not use a local test result as a substitute for an unstarted CI job.
