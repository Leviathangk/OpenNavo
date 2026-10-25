package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNoInformationRecognisesPlaceholdersInEverySourceLanguage(t *testing.T) {
	for _, text := range []string{
		"## Muse Code 1.4.3 Exact-version official change details are unavailable.",
		"Detailed official release notes are unavailable. No changes are inferred from the version number.",
		"The catalog records `3.0.135`. Exact change details for this build were not verified.",
		"### Changes\n\n- Not provided: exact changes for this version are unverified.",
		"### Changes\n\n- Not provided.\n\n[Official release notes](https://example.com/notes)",
		"The official release page contains no release notes or change details for Sky 0.5.2.",
		"The release notice directs readers to the corresponding upstream release notes.",
		"## Exact-build evidence and scope The backend records `7.3.6.10441`. Exact-build changes and publication date are unavailable.",
		"## Version evidence Bilibili 1.19.0 is recorded in the cask history. ## Release details unavailable",
		"官方 macOS 更新源确认版本 1.0，但该条目没有变更描述或发行说明链接。",
		"发布说明正文仅含产品链接，未提供此版本的功能变更或修复详情。",
		"官方未提供此版本的變更說明。",
		"このバージョンの変更内容は提供されていません。",
		"Die Versionshinweise für 2.3.1 sind nicht verfügbar.",
		"Les notes de version de cette version ne sont pas disponibles.",
		"Le note di rilascio di questa versione non sono disponibili.",
		"Las notas de la versión no están disponibles.",
		"As notas de versão não estão disponíveis.",
		"Примечания к выпуску недоступны.",
		"Release notes for 2.3.1 are unavailable.",
		"## Muse 4.0 Official exact-version release details were not found. No changes or release date are inferred.",
		"The official release evidence for Yakit 1.4.9 contains no release notes, so specific changes cannot be described.",
	} {
		require.True(t, noInformation(map[string]any{"body_markdown": text}), text)
	}
	require.True(t, noInformation(map[string]any{"sections": []any{map[string]any{"area": "Changes", "items": []any{"Not provided."}}}}))
}

func TestNoInformationKeepsNotesThatStateAChange(t *testing.T) {
	for _, text := range []string{
		// Real changes that mention unavailable notes or features.
		"修复离线时发行说明不可用导致的崩溃。",
		"修復離線時發行說明無法載入導致的當機。",
		"Fixes a crash when release notes are unavailable offline.",
		"オフライン時に変更内容が表示されない問題を修正しました。",
		"Behebt einen Absturz, wenn die Versionshinweise offline nicht verfügbar sind.",
		"Ein Absturz wurde behoben, wenn die Versionshinweise nicht verfügbar sind.",
		"Corrige un plantage lorsque les notes de version ne sont pas disponibles hors ligne.",
		"Corregge un arresto anomalo quando le note di rilascio non sono disponibili offline.",
		"Corrige un fallo cuando las notas de la versión no están disponibles sin conexión.",
		"Corrige uma falha quando as notas de versão não estão disponíveis offline.",
		"Исправлен сбой, когда примечания к выпуску недоступны офлайн.",
		// A disclaimer beside listed changes.
		"Official notes cover the 2.70 series; separate 2.70.1 changes were not provided.\n\n### Changes\n- Adds vertical tabs.",
		"官方未提供完整变更说明。\n- 新增标签页分组。",
		// Ordinary notes and evidence asides.
		"Adds Save As for screenshots in Quick Access and the annotation editor.",
		"Fixes an issue where the setting was unavailable after waking from sleep.",
		"Removes the unverified developer warning from the installer.",
		"Improves model fallback behavior and unavailable-model guidance in Classic sessions.",
		"Improves startup reliability and log handling. The catalog records both `4.94.0,241994` and `4.94.0`.",
		"Fixed ribbon menus not displaying correctly.\n\n## Version evidence\n\nThe official page lists this build.",
		"## Changes\n- Adds dark mode\n- Fixes a crash when the file is not available offline",
		"修复了未提供权限时应用崩溃的问题。",
		// Upgrades to a stated version, beside an aside about the upstream changelog.
		"将捆绑的 CLIProxyAPI 更新至 8.0.28，包含上游改进和稳定性增强。\n- 精确版本更新日志仅概括了上游改进，未列出更细的修复项目。",
		"Updates the bundled engine to 8.0.28. The upstream changelog does not list further changes.",
		// Changes stated mid-sentence beside a note that separate details are missing.
		"Separate version details were not provided.\n\nThis release adds vertical tabs and fixes a crash when importing large files.",
		"Release notes are not available for other changes. Vienna 3.10.8 fixes a crash affecting macOS 10.13.",
		"未提供此构建的独立版本详情。此版本新增了标签页，并修复了导入大文件时的崩溃。",
		"Die Versionshinweise für 2.3.1 sind nicht verfügbar. Diese Version fügt vertikale Tabs hinzu und behebt einen Absturz.",
		"Les notes de version ne sont pas disponibles. Cette version ajoute des onglets verticaux et corrige un plantage.",
		"Le note di rilascio non sono disponibili. Questa versione aggiunge schede verticali e corregge un arresto anomalo.",
		"Las notas de la versión no están disponibles. Esta versión añade pestañas verticales y corrige un fallo.",
		"As notas de versão não estão disponíveis. Esta versão adiciona abas verticais e corrige uma falha.",
		"Примечания к выпуску недоступны. В этой версии добавлены вертикальные вкладки и исправлен сбой.",
		"このバージョンの変更内容は提供されていません。このリリースでは縦タブを追加し、クラッシュを修正しました。",
		// A change named by a link label.
		"Official release notes are unavailable.\n\n[Fixes a crash on launch](https://example.com/pull/123)",
		// Series-level items listed under a patch that had no notes of its own.
		"Official 0.553-series notes; separate changes for patch 0.553.1 were not provided.\n\n### Changes\n- Drag notes, tasks, bookmarks or contacts into a note to insert note-links.\n- Drag a selection onto the sidebar to create a linked page.",
	} {
		require.False(t, noInformation(map[string]any{"body_markdown": text}), text)
	}
	// A section heading does not join its item: "Changes" over a fix that mentions unavailable video.
	require.False(t, noInformation(map[string]any{"sections": []any{map[string]any{"area": "Changes", "items": []any{
		"Try adaptive YouTube formats when HLS video is unavailable.", "Stop verifying Pro registration by confirmation email."}}}}))
	// A section item that states a change outweighs an aside elsewhere in the note.
	require.False(t, noInformation(map[string]any{
		"summary":  "Changes were not provided for every component.",
		"sections": []any{map[string]any{"area": "Improvements", "items": []any{"Adds keyboard shortcuts for tabs."}}},
	}))
	// Remarks about sources are not changes, even as list items.
	require.True(t, noInformation(map[string]any{"body_markdown": "### Changes\n\n- Not provided: exact changes for this desktop version could not be verified.\n- The official public release history does not list changes for this version."}))
	require.True(t, noInformation(map[string]any{"body_markdown": "## 主要变化\n- 官方页面未提供本次构建的独立变更说明。\n- 所查来源没有更细的修复项目。"}))
	// Source links and download files listed under a placeholder are not changes.
	require.True(t, noInformation(map[string]any{"body_markdown": "Official release details for 4.0 were not found.\n\n## Sources\n- https://formulae.brew.sh/api/cask/muse.json\n- [homepage](https://muse.ai/)\n- url: Muse-4.0-arm64.dmg\n- url: Muse-4.0-x64.dmg"}))
	require.True(t, noInformation(map[string]any{"body_markdown": "Exact-version release details unavailable. New features, fixes and performance changes are not itemized for this build."}))
	// Build and download remarks or bare names are not changes either.
	require.True(t, noInformation(map[string]any{"body_markdown": "### Changes\n\n- Not provided: exact desktop 3.0.2 build changes could not be verified.\n- The desktop updater lists the build and download for this version.\n- The CE download page links older notes only."}))
	require.True(t, noInformation(map[string]any{"body_markdown": "The official feed was checked. No changes can be reported for 1.98.56.\n\n- Brave Origin Beta\n- GitHub v1.98.56"}))
	// Links name sources whatever their label, and nouns or negated verbs state no change.
	require.True(t, noInformation(map[string]any{"body_markdown": "Official release notes are unavailable.\n\n- [View the release history](https://example.com/history)\n- [Read the version details](https://example.com/details)"}))
	require.True(t, noInformation(map[string]any{"body_markdown": "Official release notes are unavailable; the listed bug fixes are not specified."}))
	require.True(t, noInformation(map[string]any{"body_markdown": "Release notes for 2.3.1 are unavailable. No release fixes are documented."}))
	require.True(t, noInformation(map[string]any{"body_markdown": "Примечания к выпуску недоступны. Новые функции не добавлены в описание."}))
	require.True(t, noInformation(map[string]any{"body_markdown": "官方未提供具体变更清单，因此不推断新增功能、改进或修复。\n\n提供 0.41.0 安装包。"}))
	// A bare heading is no change: "## Fixes" over "Not provided" stays a placeholder.
	require.True(t, noInformation(map[string]any{"body_markdown": "## Fixes\n\n- Not provided: exact changes are unverified."}))
}

func TestNoInformationChangeRegressions(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		empty      bool
	}{
		{"past tense", "Separate version details were not provided. This release added vertical tabs and fixed a crash when importing large files.", false},
		{"negated object", "Official release notes are unavailable. This release adds no new features.", true},
		{"negated leading verb", "Official release notes are unavailable. Adds no new features.", true},
		{"negative past object", "Official release notes are unavailable. This release introduced no new features.", true},
		{"real change after negative", "Official release notes are unavailable. This release adds no new features. This release fixed a crash on launch.", false},
		{"negative bug description", "Official release notes are unavailable. This release fixed missing tabs when no windows are open.", false},
		{"reference links", "Official release notes are unavailable.\n- [View the release history][history]\n- [Read the version details][details]\n\n[history]: https://example.com/history\n[details]: https://example.com/details", true},
		{"collapsed and shortcut links", "Official release notes are unavailable.\n- [View the release history][]\n- [Read the version details]\n\n[View the release history]: https://example.com/history\n[Read the version details]: https://example.com/details", true},
		{"reference change label", "Official release notes are unavailable.\n[Fixes a crash on launch][fix]\n\n[fix]: https://example.com/fix", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.empty, noInformation(map[string]any{"body_markdown": tc.body}))
		})
	}
}

func TestNoInformationAllowsAndResolvedFailures(t *testing.T) {
	for _, tc := range []struct {
		statement string
		empty     bool
	}{
		{"The application now allows users to pin tabs and reopen closed workspaces.", false},
		{"This release allowed users to pin tabs.", false},
		{"The application now allows no new actions.", true},
		{"Now no longer crashes when release notes are unavailable offline.", false},
		{`Fixes "No release notes available" error on startup.`, false},
		{`This release fixes "No release notes available" error on startup.`, false},
		{"Fixes ‘No release notes available’ error on startup.", false},
		{"Now no new features are documented.", true},
		{"Now no longer available: official release notes.", true},
		{"Fixes no known issues.", true},
		{"This release fixes no known issues.", true},
	} {
		t.Run(tc.statement, func(t *testing.T) {
			require.Equal(t, tc.empty, noInformation(map[string]any{"body_markdown": "Official release notes are unavailable. " + tc.statement}))
		})
	}
}

// A long changelog is read at once: a placeholder line in it never makes it a note without information, and
// reading it must not rescan the whole note once per list item (cmux's notes took over a minute in every language).
func TestNoInformationReadsLongNotesQuickly(t *testing.T) {
	var body strings.Builder
	body.WriteString("Official release notes are unavailable for some components.\n\n## Changes\n")
	for i := 0; i < 1500; i++ {
		body.WriteString("- Fixed a crash when reopening a workspace with many panes and restored the scrollback buffer (#1234).\n")
	}
	start := time.Now()
	require.False(t, noInformation(map[string]any{"body_markdown": body.String()}))
	require.Less(t, time.Since(start), time.Second)
	// Just under the bound, the full reading still runs, and the listed changes still count.
	short := "Official release notes are unavailable.\n\n" + strings.Repeat("- Fixed a crash when importing large files.\n", 120)
	require.Less(t, len(short), noInformationMaxBytes)
	require.False(t, noInformation(map[string]any{"body_markdown": short}))
}
