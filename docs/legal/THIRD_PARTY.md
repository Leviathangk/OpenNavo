# Third-party dependencies and distribution

OpenNavo's own source is GNU GPL version 3 only (`GPL-3.0-only`); versions published at commit `5fa4056` and earlier remain available under Apache-2.0. [LICENSE](../../LICENSE) contains the unmodified GPLv3 text. [NOTICE](../../NOTICE) carries additional terms under GPLv3 sections 7(e) (no trademark grant) and 7(c) (distinguishing modified versions, including the name, icon, Bundle ID, updater public key and update URL). Third-party code, fonts, templates, tools, and external services retain their upstream licenses. The project NOTICE does not replace those licenses. In particular, the original soybean-admin template remains MIT; OpenNavo's modifications and the combined application are distributed under GPL-3.0-only.

## Inventory and evidence

`docs/legal/third-party/inventory.json` records each locked dependency's ecosystem, name, version, declared or text-identified license, scope, provenance, and paths to verbatim upstream license files. `docs/legal/third-party/licenses/` stores those files by SHA-256 without rewriting their contents. The inventory includes cross-platform optional dependencies that are not installed on the host; missing local metadata is not treated as evidence of an unknown license.

Scopes are deliberately conservative:

- `runtime`: Go modules imported by built commands, statically linked Go/Rust standard-library notices, and the retained admin template.
- `runtime-candidate`: npm production dependency graphs, Rust normal dependency graphs across platforms, and Python's locked production export. This includes conditional dependencies and packages used only while bundling. It is not a claim that every listed package executes in every artifact.
- `runtime-asset`: font/icon source packages whose generated assets are shipped even when their generator dependencies are development-only.
- `development`: dependencies outside those graphs, including linters, tests, and generators. Their licenses do not become the application's license merely because the tools are used to build it.
- `external`: separately installed Homebrew, PostgreSQL, Redis, MinIO, and Caddy. Operators and distributors must review those distributions' own notices and obligations. Container base-image packages are also governed by their image distributions and are not exhaustively represented by application lock files.

Metadata sources are exact-version npm/PyPI registries, integrity-checked archives, Go modules verified by checksums, Cargo manifests, and upstream license files at recorded revisions. Where an older archive omits a license and a current upstream license is used as supplementary evidence, provenance records that distinction. Where upstream publishes only a license declaration, the bundle retains its original package metadata/README attribution plus explicitly labeled SPDX reference text; those reference texts are not represented as recovered upstream files. Do not mistake a declared SPDX expression for a complete legal compatibility opinion.

## Reproduce and update

Install the locked Node, Go, Rust (including its `rust-docs` component), and MCP development dependencies, then run:

```sh
apps/mcp/.venv/bin/python -I tooling/scripts/about-inventory.py
apps/mcp/.venv/bin/python -I tooling/scripts/license-evidence.py
apps/mcp/.venv/bin/python -I tooling/scripts/about-inventory.py
```

Rust is pinned to an exact release in `apps/desktop/src-tauri/rust-toolchain.toml`, including the `rust-docs` component that supplies standard-library copyright evidence. The collector rejects a compiler that differs from that pin. To upgrade Rust, update the pin and the installers in `ci-desktop.yml` and `release-desktop.yml`, install the matching toolchain, and regenerate the inventory and bundles above.

Validate the generated artifacts before distribution:

```sh
python3 -I tooling/scripts/test_check_notices.py
python3 -I tooling/scripts/check-notices.py --check-rust-toolchain
```

The gate checks exact bundle membership and complete record equality against the canonical inventory, rejects duplicates, verifies lock fingerprints and notice bytes, and matches Rust notice evidence to the pin and workflow installers. The compiler flag also verifies the actual installed compiler; both Rust CI and desktop releases run it before building. Omit the flag only for offline inventory validation on machines without Rust.

The first collection downloads exact-version metadata when missing; subsequent runs reuse committed evidence. The supplemental step obtains omitted upstream texts and records their origin. The final collection refreshes context-local bundles and only the `modules` field of `apps/server/seeds/about.json`; its `sourceLocale` and localized content are preserved. Review unknown identifiers or missing license texts before distributing. No database or translation service is contacted.

Do not reformat upstream text files. When dependencies change, review the inventory diff, rerun license collection, retain copyright and attribution notices, and check applicable source-offer obligations. Do not remove upstream licenses when translating repository documentation.

## Artifact locations

Source ports and standalone native helpers are registered in `native/manifest.json`
under the `Native` ecosystem. The collector includes their original license texts,
source-lock digests and integration boundaries in About and the desktop notice
bundle. Native manifest entries with `products: ["server"]` (DuoUpdater and
Installomator vendor metadata) instead ship in the server notice bundle.
ClamAV's full release archive includes its Cargo vendor tree; the source
bundle also includes all seven pinned static C dependencies and our build recipe.
Keep this source archive in the same Release as the helper binaries. ClamAV is
never linked into the desktop application. UnRAR is disabled; conservatively
retaining its notice in the source inventory does not mean it is shipped as code.
The M0 evidence workflow remains opt-in. Desktop CI and release packaging build
and bundle the standalone helpers; release packaging also publishes the matching
ClamAV source archive.

| Distribution            | Notice source                                   | Installed location              |
| ----------------------- | ----------------------------------------------- | ------------------------------- |
| Server and ops images   | `apps/server/third-party/`                      | `/usr/share/licenses/opennavo/` |
| MCP image               | `apps/mcp/third-party/`                         | `/usr/share/licenses/opennavo/` |
| MCP wheel               | `apps/mcp/third-party/`                         | `opennavo_mcp/third_party/`     |
| MCP source distribution | `apps/mcp/third-party/`                         | `third-party/`                  |
| Desktop app             | `apps/desktop/src-tauri/resources/third-party/` | Tauri bundled resources         |
| Web runtime image       | `docs/legal/third-party/`                       | `/usr/share/licenses/opennavo/` |

Each bundle contains project `LICENSE`, project `NOTICE`, an inventory, and referenced upstream texts. Server/MCP bundles select their runtime graphs. Desktop notices conservatively include npm/Rust runtime candidates and the admin template; the web image retains the full inventory as a safe superset of bundled dependencies. Include the canonical notice directory alongside separately redistributed static admin files. A generated notice bundle is part of the build input, not an optional post-release attachment.

The separately rebuilt `postgres` and `asynqmon` targets collect upstream Go dependency notices and modified module manifests into `/usr/share/licenses/gosu/` and `/usr/share/licenses/asynqmon/`. The development MinIO image retains its upstream source, modified dependency lock, and collected notices under `/usr/share/doc/minio/`; its locked module graph is separate from the application inventory. Review that source and its AGPL obligations before redistributing the development image.

The upstream soybean-admin MIT license remains in `apps/admin/LICENSE`. Font licenses remain beside their assets. GPL-licensed Go lint tools are development dependencies rather than linked server modules. MinIO and Redis are external services with distinct copyleft/commercial licensing choices; this inventory does not grant an exception to their terms.

## Security review boundaries

Dependency vulnerability reports and license inventories answer different questions. A package appearing in `pnpm audit --prod` can still be a build-time Nuxt module; check actual imports, build traces, and fresh output before describing exposure. Preserve audit reports and unresolved findings instead of suppressing them. [DEPENDENCY_SECURITY.md](../security/DEPENDENCY_SECURITY.md) records the current audit results and verification limits; a clean scanner/audit does not establish absence of all vulnerabilities.
