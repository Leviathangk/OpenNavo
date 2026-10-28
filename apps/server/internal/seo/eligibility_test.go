package seo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func eligible() Facts {
	return Facts{
		Name: "Visual Studio Code", Token: "visual-studio-code", Summary: "Open-source code editor",
		Version: "1.95.3,abc", Installs30d: 120, Rank30d: 1, Override: OverrideAuto, SourceLocale: "en-US",
		PublishedLocales: []string{"en-US", "zh-CN", "ja-JP"}, FirstSeenAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestEvaluateRuleAndOverrides(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Facts)
		ok     bool
		reason string
	}{
		{"eligible", func(*Facts) {}, true, ""},
		{"hidden wins over a forced index", func(f *Facts) { f.Hidden, f.Override = true, OverrideIndex }, false, ReasonHidden},
		{"forced noindex", func(f *Facts) { f.Override = OverrideNoindex }, false, ReasonForcedNoindex},
		{"forced index rescues a thin page", func(f *Facts) { f.Summary, f.Override = "", OverrideIndex }, true, ""},
		{"disabled", func(f *Facts) { f.Disabled = true }, false, ReasonUnavailable},
		{"stale but still offered by Homebrew", func(f *Facts) { f.Stale = true }, true, ""},
		{"deprecated and stale", func(f *Facts) { f.Deprecated, f.Stale = true, true }, false, ReasonUnavailable},
		{"deprecated but still updated", func(f *Facts) { f.Deprecated = true }, true, ""},
		{"no summary", func(f *Facts) { f.Summary = "  " }, false, ReasonMissingSummary},
		{"summary repeats the name", func(f *Facts) { f.Summary = "visual studio code" }, false, ReasonMissingSummary},
		{"summary repeats the token", func(f *Facts) { f.Summary = "visual-studio-code" }, false, ReasonMissingSummary},
		{"latest version, no requirements, no installs", func(f *Facts) { f.Version, f.Installs30d = "latest", 0 }, false, ReasonThin},
		{"requirements alone are a fact", func(f *Facts) { f.Version, f.Installs30d, f.HasRequirements = "latest", 0, true }, true, ""},
		{"installs alone are a fact", func(f *Facts) { f.Version = "" }, true, ""},
	}
	for _, c := range cases {
		f := eligible()
		c.change(&f)
		ok, reason := Evaluate(f)
		require.Equal(t, c.ok, ok, c.name)
		require.Equal(t, c.reason, reason, c.name)
	}
}

func TestDecideAppliesModeAndScope(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	thin := eligible()
	thin.Summary = ""
	for _, mode := range []string{"off", "shadow"} {
		d := Decide(thin, Policy{Mode: mode, Scope: "all"})
		require.True(t, d.Indexable, mode)
		require.Equal(t, Locales, d.Locales, mode)
		require.False(t, d.Eligible)
		require.Equal(t, ReasonMissingSummary, d.EligibleReason)
		require.False(t, d.Enforced)
	}
	d := Decide(thin, Policy{Mode: "enforce", Scope: "all"})
	require.False(t, d.Indexable)
	require.Equal(t, ReasonMissingSummary, d.Reason)
	require.Empty(t, d.Locales)
	require.True(t, d.Enforced)

	// "new" leaves apps first seen before Since alone, and does nothing until Since is set.
	require.True(t, Decide(thin, Policy{Mode: "enforce", Scope: "new", Since: since}).Indexable)
	require.True(t, Decide(thin, Policy{Mode: "enforce", Scope: "new"}).Indexable)
	thin.FirstSeenAt = since.Add(time.Hour)
	require.False(t, Decide(thin, Policy{Mode: "enforce", Scope: "new", Since: since}).Indexable)
	// Overrides apply under enforce even outside the scope, and never in shadow mode.
	old := eligible()
	old.Override = OverrideNoindex
	require.False(t, Decide(old, Policy{Mode: "enforce", Scope: "new", Since: since}).Indexable)
	require.True(t, Decide(old, Policy{Mode: "shadow", Scope: "all"}).Indexable)

	ok := Decide(eligible(), Policy{Mode: "enforce", Scope: "all"})
	require.True(t, ok.Indexable)
	require.Equal(t, []string{"en-US", "zh-CN", "ja-JP"}, ok.Locales)
	zhSource := eligible()
	zhSource.SourceLocale, zhSource.PublishedLocales = "zh-CN", []string{"ru-RU"}
	require.Equal(t, []string{"zh-CN", "ru-RU"}, IndexedLocales(zhSource))
}
