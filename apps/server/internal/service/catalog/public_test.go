package catalog

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestPackageCompatibilityAndLifecycleMapping(t *testing.T) {
	for _, input := range []struct {
		current, size, maximum int
		valid                  bool
	}{{1, 24, 100, true}, {0, 24, 100, false}, {1, 101, 100, false}, {math.MaxInt, 100, 100, false}, {1, 5000, 5000, true}} {
		require.Equal(t, input.valid, ValidPage(input.current, input.size, input.maximum))
	}
	date, reason, replacement := "2026-10-04", "unsupported", "cask:new-token"
	info := lifecycle(true, &date, &reason, &replacement)
	require.Equal(t, "2026-10-04", info.Date.String())
	require.Equal(t, "new-token", info.Replacement.Token)
	require.Nil(t, lifecycle(false, nil, nil, nil))
	display, summaryText := "中文名", "中文简介"
	sum := summary(store.PublicPackage{Name: "English", I18n: store.PublicI18n{DisplayName: &display, Summary: &summaryText}})
	require.Equal(t, display, sum.DisplayName)
	require.Equal(t, summaryText, *sum.Summary)
	conflict := conflicts(json.RawMessage(`{"cask":["conflict"],"formula":["formula-conflict"]}`))
	require.Equal(t, []string{"conflict"}, conflict.Casks)
	require.Equal(t, []string{"formula-conflict"}, conflict.Formulae)
	formula, _ := dependencyData(store.PublicPackage{Kind: "formula", Dependencies: json.RawMessage(`{"runtime":["lib"],"usesFromMacos":[{"zlib":"build"},"curl"]}`)})
	require.Equal(t, []string{"zlib", "curl"}, formula.UsesFromMacos)
	require.Empty(t, formula.Test)
	_, cask := dependencyData(store.PublicPackage{Kind: "cask", SupportsArm64: true, Dependencies: json.RawMessage(`{"dependsOn":{"macos":{">=":["13"]},"cask":["app"]}}`)})
	require.Equal(t, ">= 13", *cask.Macos)
	require.Equal(t, []string{"app"}, cask.Casks)
	require.Len(t, cask.Arch, 1)
}

func TestStaleDisableNoticeAndUpstreamPrecedence(t *testing.T) {
	p := store.PublicPackage{Disabled: true, StaleDisabled: true}
	for locale, reason := range map[string]string{
		"en-US": "No version update for more than 90 days",
		"zh-CN": "超过 90 天没有版本更新",
		"ja-JP": "90 日以上バージョン更新がありません",
		"es-ES": "Sin actualizaciones de versión durante más de 90 días",
		"pt-BR": "Sem atualização de versão há mais de 90 dias",
		"ru-RU": "Версия не обновлялась более 90 дней",
	} {
		notice := packageDisable(p, locale)
		require.NotNil(t, notice)
		require.Equal(t, reason, *notice.Reason)
		require.Nil(t, notice.Replacement)
	}
	date, reason, replacement := "2026-01-01", "unsafe", "cask:replacement"
	p.UpstreamDisabled, p.DisableDate, p.DisableReason, p.DisableReplacement = true, &date, &reason, &replacement
	notice := packageDisable(p, "zh-CN")
	require.Equal(t, reason, *notice.Reason)
	require.Equal(t, date, notice.Date.String())
	require.Equal(t, "replacement", notice.Replacement.Token)
	require.Nil(t, packageDisable(store.PublicPackage{}, "en-US"))
}

func TestCaskPlatformPublicMappingPreservesCompleteMetadata(t *testing.T) {
	item, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"mapping","version":"2","supported_platforms":["arm64_sonoma","sonoma"],"caveats_rosetta":true,"depends_on":{"formula":[{"openssl@3":">=3"}],"maximum_macos":{"<=":["15"]}},"artifacts":[{"uninstall":[{"pkgutil":"test.id"}]},{"zap":[{"trash":"~/.test"}]}],"variations":{"sonoma":{"version":"1","depends_on":{}}}}`))
	require.NoError(t, err)
	p := store.PublicPackage{Kind: "cask", SupportsArm64: item.SupportsArm64, SupportsX8664: item.SupportsX8664, Dependencies: item.Dependencies}
	metadata, platforms, err := platformData(p)
	require.NoError(t, err)
	require.Equal(t, "known", metadata.SupportStatus)
	require.True(t, metadata.RequiresRosetta)
	require.Len(t, platforms, 2)
	require.Equal(t, "2", platforms[0].Version)
	require.Equal(t, "1", platforms[1].Version)
	require.Empty(t, platforms[1].DependsOn.Formulae)
	require.Equal(t, "<= 15", *platforms[0].DependsOn.Macos)
	require.Len(t, *platforms[0].Artifacts.Entries, 2)
	require.Equal(t, "zap", (*platforms[0].Artifacts.Entries)[1].Type)
	_, dependsOn := dependencyData(p)
	require.Equal(t, "<= 15", *dependsOn.Macos)
	require.Equal(t, []string{"openssl@3"}, dependsOn.Formulae)
	require.Contains(t, *dependsOn.Requirements, "maximum_macos")
	_, legacy, err := platformData(store.PublicPackage{Dependencies: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.NotNil(t, legacy)
}

func TestDisambiguatedName(t *testing.T) {
	developer := "Example Studio"
	require.Equal(t, "Buzz (Example Studio)", disambiguatedName("Buzz", "buzz", &developer))
	require.Equal(t, "Buzz (buzz-other)", disambiguatedName("Buzz", "buzz-other", nil))
	require.Equal(t, "Buzz (Example Studio)", disambiguatedName("Buzz (Example Studio)", "buzz", &developer))
}
