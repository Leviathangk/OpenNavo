# Desktop release notes

GitHub Release titles and bodies must be English only. The publishing script uses only the version's `en-US.md` for the GitHub body, including on retries. Chinese text in English notes fails validation before building or making remote changes. Do not concatenate localized notes into GitHub Releases.

Before pushing a `desktop-vX.Y.Z` tag, add six UTF-8 Markdown files for that exact version:

- `apps/desktop/release-notes/X.Y.Z/en-US.md`
- `apps/desktop/release-notes/X.Y.Z/zh-CN.md`
- `apps/desktop/release-notes/X.Y.Z/ja-JP.md`
- `apps/desktop/release-notes/X.Y.Z/es-ES.md`
- `apps/desktop/release-notes/X.Y.Z/pt-BR.md`
- `apps/desktop/release-notes/X.Y.Z/ru-RU.md`

Describe actual user-facing changes in the tagged code. Keep all six translations aligned with the English source. Historical published versions do not need local backfills. Do not copy preview data or list unreleased features. Prereleases use the complete version directory, such as `0.3.0-beta.1`.

Validate locally from the repository root:

```sh
node apps/desktop/scripts/release-notes.mjs --version X.Y.Z
```

Tag CI validates all six files before starting macOS builds and passes them to draft registration with `--notes-dir apps/desktop/release-notes`. Missing, empty or version-only notes fail publication; there is no placeholder fallback. Manual build-only runs do not require notes. Files are read directly, preserving Markdown and avoiding shell interpolation of their contents.

The server stores these notes with the draft release. Publishing that release makes them available to the website and clients. Registration includes all six languages with English as the source language. Existing published notes are not changed by editing these files; update those through the admin release editor.

Draft registration uses `en-US` as the source language. Direct registration also supports an explicit `--notes-en` text argument containing real content; it cannot be combined with `--notes-dir`.
