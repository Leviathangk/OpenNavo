package seo

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPageDecisionMatrix(t *testing.T) {
	f := eligible()
	f.Kind, f.ReadyLocales, f.PublishedLocales, f.HasDependencyContent = "cask", Locales, Locales, true
	f.TroubleshootingIssues = map[string]int{}
	for _, locale := range Locales {
		f.TroubleshootingIssues[locale] = TroubleshootingMinIssues
	}
	f.NotesPages = map[int]map[string]NotesFacts{}
	for page := 1; page <= 3; page++ {
		f.NotesPages[page] = map[string]NotesFacts{}
		for _, locale := range Locales {
			f.NotesPages[page][locale] = NotesFacts{Chars: 300, HasSource: true}
		}
	}
	p := Policy{Mode: "enforce", Scope: "all", TranslationMode: "enforce", VersionsSitemap: true}
	for _, kind := range []PageKind{PageOverview, PageVersions, PageDependencies, PageDetails, PageTroubleshooting} {
		for _, locale := range Locales {
			for page := 1; page <= 3; page++ {
				t.Run(fmt.Sprintf("%s/%s/%d", kind, locale, page), func(t *testing.T) {
					d := DecidePage(f, PageRef{Kind: kind, Locale: locale, Page: page}, p)
					if kind == PageDetails || kind == PageTroubleshooting && page > 1 {
						require.Equal(t, "noindex, follow", d.Robots)
						require.Equal(t, OverviewPath(f, locale), d.CanonicalPath)
						require.Empty(t, d.Hreflang)
						require.False(t, d.Announce)
						require.False(t, d.InSitemap)
						return
					}
					require.Equal(t, "index, follow", d.Robots)
					require.Empty(t, d.CanonicalPath)
					require.Equal(t, Locales, d.Hreflang)
					require.Equal(t, page == 1, d.InSitemap)
					require.True(t, d.Announce)
					require.Empty(t, d.Reason)
				})
			}
		}
	}
	// A thin page removes that locale from every equivalent page's hreflang set, not just itself.
	f.NotesPages[2]["fr-FR"] = NotesFacts{Chars: 299, HasSource: true}
	for _, locale := range Locales {
		d := DecidePage(f, PageRef{Kind: PageVersions, Locale: locale, Page: 2}, p)
		require.NotContains(t, d.Hreflang, "fr-FR")
		if locale == "fr-FR" {
			require.False(t, d.Announce)
			require.Empty(t, d.CanonicalPath)
			require.Empty(t, d.Hreflang)
			require.Equal(t, "thin_notes_page", d.Reason)
		}
	}
}

func TestTroubleshootingRequiresLocalVerifiedIssuesAndOverview(t *testing.T) {
	for _, kind := range []string{"cask", "link"} {
		for _, locale := range Locales {
			for _, count := range []int{0, 2, 3, 4} {
				t.Run(fmt.Sprintf("%s/%s/%d", kind, locale, count), func(t *testing.T) {
					f := eligible()
					f.Kind, f.ReadyLocales = kind, Locales
					f.TroubleshootingIssues = map[string]int{locale: count}
					policy := Policy{Mode: "shadow"} // Independent of intent rollout.
					ref := PageRef{Kind: PageTroubleshooting, Locale: locale}
					d := DecidePage(f, ref, policy)
					require.Equal(t, count >= TroubleshootingMinIssues, d.InSitemap)
					require.Equal(t, d.InSitemap, d.Announce)
					if d.InSitemap {
						require.Equal(t, []string{locale}, d.Hreflang)
						require.Empty(t, d.CanonicalPath)
					} else {
						require.Equal(t, "noindex, follow", d.Robots)
						require.Empty(t, d.Hreflang)
						require.Equal(t, OverviewPath(f, locale), d.CanonicalPath)
					}
					f.Hidden = true
					require.False(t, DecidePage(f, ref, policy).Announce)
					f.Hidden = false
					ref.Filtered = true
					require.False(t, DecidePage(f, ref, policy).InSitemap)
				})
			}
		}
	}
}

func TestPageDecisionQualityAndShadow(t *testing.T) {
	f := eligible()
	f.Kind, f.ReadyLocales = "cask", []string{"fr-FR"}
	shadow := Policy{Mode: "shadow", TranslationMode: "shadow"}
	for _, locale := range []string{"en-US", "zh-CN", "fr-FR"} {
		require.True(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: locale}, shadow).Announce)
	}
	require.False(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "de-DE"}, shadow).Announce)
	shadow.TranslationMode = "enforce"
	require.False(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "zh-CN"}, shadow).Announce)
	f.HasDependencyContent = false
	d := DecidePage(f, PageRef{Kind: PageDependencies, Locale: "fr-FR"}, shadow)
	require.Equal(t, "/fr/apps/visual-studio-code", d.CanonicalPath)
	f.Kind = "link"
	d = DecidePage(f, PageRef{Kind: PageDetails, Locale: "fr-FR"}, shadow)
	require.Equal(t, "unsupported_page", d.Reason)
	require.Empty(t, d.CanonicalPath)
	f.Hidden = true
	require.False(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "en-US"}, shadow).Announce)
	require.Equal(t, 11, ReadableChars("abc中文かな"))
}

func TestTranslationEnforcementCanRollOutByLocale(t *testing.T) {
	f := eligible()
	f.Kind = "cask"
	f.ReadyLocales = []string{}
	policy := Policy{Mode: "shadow", TranslationMode: "enforce", TranslationLocales: []string{"zh-CN"}}
	require.False(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "zh-CN"}, policy).InSitemap)
	require.True(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "ja-JP"}, policy).InSitemap)
	require.False(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "fr-FR"}, policy).InSitemap)
}

func TestIntentPagesRequireQualifiedTargetPerLocale(t *testing.T) {
	f := eligible()
	f.Kind = "cask"
	f.ReadyLocales = Locales
	f.IntentLocales = map[PageKind][]string{PageAlternatives: {"en-US", "fr-FR"}, PageUninstall: {"en-US"}}
	p := Policy{Mode: "shadow", IntentPagesEnabled: true}
	for _, locale := range Locales {
		d := DecidePage(f, PageRef{Kind: PageAlternatives, Locale: locale}, p)
		if locale == "en-US" || locale == "fr-FR" {
			require.True(t, d.InSitemap)
			require.Equal(t, []string{"en-US", "fr-FR"}, d.Hreflang)
		} else {
			require.False(t, d.Announce)
			require.Empty(t, d.Hreflang)
			require.Equal(t, OverviewPath(f, locale), d.CanonicalPath)
		}
	}
	p.IntentPagesEnabled = false
	require.False(t, DecidePage(f, PageRef{Kind: PageAlternatives, Locale: "en-US"}, p).InSitemap)
	p.IntentPagesEnabled = true
	f.Kind = "link"
	require.Equal(t, "unsupported_page", DecidePage(f, PageRef{Kind: PageUninstall, Locale: "en-US"}, p).Reason)
}
