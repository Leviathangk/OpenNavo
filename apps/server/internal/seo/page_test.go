package seo

import (
	"fmt"
	"slices"
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
			f.NotesPages[page][locale] = NotesFacts{Chars: 300, Entries: VersionsMinNoteEntries}
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
					if kind == PageVersions && locale != f.SourceLocale {
						require.Equal(t, "versions_source_only", d.Reason)
						require.Equal(t, "noindex, follow", d.Robots)
						require.False(t, d.InSitemap)
						require.False(t, d.Announce)
						require.Empty(t, d.Hreflang)
						return
					}
					if kind == PageDependencies {
						// Dependency content keeps the tab's own URL, but the tab is never indexed.
						require.Equal(t, PageDecision{Robots: "noindex, follow", Hreflang: []string{}, Reason: ReasonSupportingPage}, d)
						return
					}
					require.Equal(t, IndexableRobots, d.Robots)
					require.Empty(t, d.CanonicalPath)
					if kind == PageVersions {
						require.Equal(t, []string{f.SourceLocale}, d.Hreflang)
					} else {
						require.Equal(t, Locales, d.Hreflang)
					}
					require.Equal(t, page == 1, d.InSitemap)
					require.True(t, d.Announce)
					require.Empty(t, d.Reason)
				})
			}
		}
	}
	// A thin page removes that locale from every equivalent page's hreflang set, not just itself.
	f.NotesPages[2]["fr-FR"] = NotesFacts{Chars: 299, Entries: VersionsMinNoteEntries}
	// Long notes on too few releases are thin too: one or two notes repeat the overview's latest release.
	f.NotesPages[3]["ja-JP"] = NotesFacts{Chars: 5000, Entries: VersionsMinNoteEntries - 1}
	for page, thin := range map[int]string{2: "fr-FR", 3: "ja-JP"} {
		for _, locale := range Locales {
			d := DecidePage(f, PageRef{Kind: PageVersions, Locale: locale, Page: page}, p)
			require.NotContains(t, d.Hreflang, thin)
			if locale == thin {
				require.Equal(t, "noindex, follow", d.Robots)
				require.False(t, d.Announce)
				require.Empty(t, d.CanonicalPath)
				require.Empty(t, d.Hreflang)
				require.Equal(t, "versions_source_only", d.Reason)
			}
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
	require.Equal(t, "duplicate_of_overview", d.Reason)
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

func TestClosedLocalesAreNoindexOnEveryPage(t *testing.T) {
	open := []string{"en-US", "zh-CN", "ja-JP", "es-ES", "ru-RU"}
	f := eligible()
	f.Kind, f.ReadyLocales, f.PublishedLocales = "cask", Locales, Locales
	f.TroubleshootingIssues, f.NotesPages = map[string]int{}, map[int]map[string]NotesFacts{1: {}}
	f.IntentLocales = map[PageKind][]string{PageAlternatives: Locales, PageUninstall: Locales}
	for _, locale := range Locales {
		f.TroubleshootingIssues[locale] = TroubleshootingMinIssues
		f.NotesPages[1][locale] = NotesFacts{Chars: 300, Entries: VersionsMinNoteEntries}
	}
	kinds := []PageKind{PageOverview, PageVersions, PageDependencies, PageDetails, PageAlternatives, PageUninstall, PageTroubleshooting}
	for _, mode := range []string{"shadow", "enforce"} {
		for _, content := range []bool{true, false} {
			f.HasDependencyContent = content
			everyLocale := Policy{Mode: mode, Scope: "all", VersionsSitemap: true, IntentPagesEnabled: true}
			closing := everyLocale
			closing.OpenLocales = open
			for _, kind := range kinds {
				for _, locale := range Locales {
					t.Run(fmt.Sprintf("%s/%t/%s/%s", mode, content, kind, locale), func(t *testing.T) {
						ref := PageRef{Kind: kind, Locale: locale, Page: 1}
						before, d := DecidePage(f, ref, everyLocale), DecidePage(f, ref, closing)
						if slices.Contains(open, locale) {
							// An open language decides exactly as before, without the closed languages' alternates.
							want := before
							want.Hreflang = []string{}
							for _, alternate := range before.Hreflang {
								if slices.Contains(open, alternate) {
									want.Hreflang = append(want.Hreflang, alternate)
								}
							}
							require.Equal(t, want, d)
							if kind == PageOverview {
								require.Equal(t, open, d.Hreflang)
							}
							return
						}
						require.Equal(t, "noindex, follow", d.Robots)
						require.Empty(t, d.Hreflang)
						require.False(t, d.InSitemap)
						require.False(t, d.Announce)
						switch {
						case kind == PageDetails:
							require.Equal(t, "consolidated", d.Reason)
						case kind == PageDependencies && content:
							require.Equal(t, ReasonSupportingPage, d.Reason)
							require.Empty(t, d.CanonicalPath)
						case kind == PageDependencies:
							require.Equal(t, "duplicate_of_overview", d.Reason)
						default:
							require.Equal(t, ReasonLocaleClosed, d.Reason)
						}
					})
				}
			}
		}
	}
	// A filtered view keeps its own reason; an empty list closes every language, en-US included.
	require.Equal(t, "filtered_view", DecidePage(f, PageRef{Kind: PageVersions, Locale: "de-DE", Filtered: true}, Policy{OpenLocales: open}).Reason)
	for _, locale := range Locales {
		require.Equal(t, ReasonLocaleClosed, DecidePage(f, PageRef{Kind: PageOverview, Locale: locale}, Policy{OpenLocales: []string{}}).Reason)
	}
}

func TestDecideKeepsOnlyOpenLocales(t *testing.T) {
	open := []string{"en-US", "zh-CN", "ja-JP", "es-ES", "ru-RU"}
	require.True(t, LocaleOpen(nil, "it-IT"), "no list opens every site language")
	require.False(t, LocaleOpen([]string{}, "en-US"))
	require.True(t, Policy{OpenLocales: open}.LocaleOpen("ru-RU"))
	require.False(t, Policy{OpenLocales: open}.LocaleOpen("pt-BR"))

	f := eligible()
	f.PublishedLocales = Locales
	for _, mode := range []string{"off", "shadow", "enforce"} {
		require.Equal(t, Locales, Decide(f, Policy{Mode: mode, Scope: "all"}).Locales, mode)
		d := Decide(f, Policy{Mode: mode, Scope: "all", OpenLocales: open})
		require.Equal(t, []string{"en-US", "zh-CN", "ja-JP", "es-ES", "ru-RU"}, d.Locales, mode)
		require.True(t, d.Indexable, "closing languages does not change the app's own verdict")
	}
	// Enforced languages are the published ones that are also open.
	f.PublishedLocales = []string{"en-US", "fr-FR", "ja-JP"}
	require.Equal(t, []string{"en-US", "ja-JP"}, Decide(f, Policy{Mode: "enforce", Scope: "all", OpenLocales: open}).Locales)
}

func TestBrandOverviewRedirectDecision(t *testing.T) {
	f := eligible()
	f.Token = "opennavo"
	for _, locale := range Locales {
		d := DecidePage(f, PageRef{Kind: PageOverview, Locale: locale}, Policy{})
		require.Equal(t, "brand_redirect", d.Reason)
		require.Contains(t, d.CanonicalPath, "/download")
		require.False(t, d.InSitemap)
		require.False(t, d.Announce)
		require.Empty(t, d.Hreflang)
	}
	f.NotesPages = map[int]map[string]NotesFacts{1: {"en-US": {Chars: 1000, Entries: 3}}}
	require.True(t, DecidePage(f, PageRef{Kind: PageVersions, Locale: "en-US"}, Policy{VersionsSitemap: true}).Announce)
}

func TestSitemapPopularityOnlyChangesInclusion(t *testing.T) {
	cases := []struct {
		name, locale   string
		installs, rank int
		policy         Policy
		override       string
		want           bool
	}{
		{name: "English below default", locale: "en-US", installs: 49, want: false},
		{name: "English at default", locale: "en-US", installs: 50, want: true},
		{name: "English custom", locale: "en-US", installs: 99, policy: Policy{SitemapMinInstalls: 100}, want: false},
		{name: "non-English last rank", locale: "zh-CN", rank: 500, want: true},
		{name: "non-English outside", locale: "ja-JP", installs: 10000, rank: 501, want: false},
		{name: "non-English unranked", locale: "es-ES", installs: 10000, want: false},
		{name: "non-English custom", locale: "ru-RU", rank: 3, policy: Policy{SitemapNonEnglishTopN: 2}, want: false},
		{name: "non-English custom boundary", locale: "ru-RU", rank: 2, policy: Policy{SitemapNonEnglishTopN: 2}, want: true},
		{name: "English override", locale: "en-US", override: OverrideIndex, want: true},
		{name: "non-English override", locale: "zh-CN", rank: 9999, override: OverrideIndex, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := eligible()
			f.ReadyLocales = Locales
			f.Installs30d, f.Rank30d, f.Override = tc.installs, tc.rank, tc.override
			d := DecidePage(f, PageRef{Kind: PageOverview, Locale: tc.locale}, tc.policy)
			require.Equal(t, tc.want, d.InSitemap)
			require.Equal(t, IndexableRobots, d.Robots)
			require.True(t, d.Announce)
			require.Equal(t, Locales, d.Hreflang)
		})
	}
}

func TestFontAndTranslatedVersionsNeverAnnounce(t *testing.T) {
	for _, override := range []string{OverrideAuto, OverrideIndex} {
		f := eligible()
		f.Override = override
		f.ReadyLocales = Locales
		f.NotesPages = map[int]map[string]NotesFacts{1: {}}
		for _, locale := range Locales {
			f.NotesPages[1][locale] = NotesFacts{Chars: 1000, Entries: 3}
		}
		policy := Policy{VersionsSitemap: true}
		for _, locale := range Locales {
			d := DecidePage(f, PageRef{Kind: PageVersions, Locale: locale}, policy)
			if locale == f.SourceLocale {
				require.True(t, d.Announce)
				require.Equal(t, []string{f.SourceLocale}, d.Hreflang)
			} else {
				require.Equal(t, "versions_source_only", d.Reason)
				require.Equal(t, "noindex, follow", d.Robots)
				require.False(t, d.Announce)
				require.False(t, d.InSitemap)
				require.Empty(t, d.Hreflang)
			}
			for _, tokenFont := range []bool{false, true} {
				font := f
				font.IsFont = true
				if tokenFont {
					font.Token = "font-example"
				}
				for _, kind := range []PageKind{PageOverview, PageVersions} {
					d = DecidePage(font, PageRef{Kind: kind, Locale: locale}, policy)
					require.Equal(t, "font_page", d.Reason)
					require.Equal(t, "noindex, follow", d.Robots)
					require.False(t, d.InSitemap)
					require.False(t, d.Announce)
					require.Empty(t, d.Hreflang)
				}
			}
		}
	}
}

func TestPopularityDoesNotNoindexSupportingIntentPages(t *testing.T) {
	f := eligible()
	f.Kind = "cask"
	f.Installs30d = 0
	f.Rank30d = 999
	f.IntentLocales = map[PageKind][]string{PageAlternatives: {"en-US"}, PageUninstall: {"en-US"}}
	f.TroubleshootingIssues = map[string]int{"en-US": 3}
	for _, kind := range []PageKind{PageAlternatives, PageUninstall, PageTroubleshooting} {
		d := DecidePage(f, PageRef{Kind: kind, Locale: "en-US"}, Policy{IntentPagesEnabled: true})
		require.Equal(t, IndexableRobots, d.Robots)
		require.True(t, d.Announce)
		require.False(t, d.InSitemap)
	}
}

func TestFontsAndLinksWithoutPopularity(t *testing.T) {
	for _, font := range []bool{false, true} {
		f := eligible()
		f.Kind = "link"
		if font {
			f.Kind = "cask"
		}
		f.IsFont = font
		f.Installs30d, f.Rank30d = 0, 0
		f.ReadyLocales = Locales
		p := Policy{Mode: "enforce", Scope: "all", FontsSitemap: true}
		for _, locale := range []string{"en-US", "zh-CN"} {
			d := DecidePage(f, PageRef{Kind: PageOverview, Locale: locale}, p)
			require.Equal(t, IndexableRobots, d.Robots)
			require.True(t, d.InSitemap)
		}
		for _, change := range []func(*Facts){
			func(f *Facts) { f.Override = OverrideNoindex },
			func(f *Facts) { f.Hidden = true },
			func(f *Facts) { f.Disabled = true },
			func(f *Facts) { f.Summary = "" },
		} {
			excluded := f
			change(&excluded)
			d := DecidePage(excluded, PageRef{Kind: PageOverview, Locale: "en-US"}, p)
			require.False(t, d.InSitemap)
			require.Equal(t, "noindex, follow", d.Robots)
		}
		p.OpenLocales = []string{"en-US"}
		require.Equal(t, ReasonLocaleClosed, DecidePage(f, PageRef{Kind: PageOverview, Locale: "zh-CN"}, p).Reason)
		p.OpenLocales = nil
		p.TranslationMode = "enforce"
		f.ReadyLocales = nil
		require.False(t, DecidePage(f, PageRef{Kind: PageOverview, Locale: "zh-CN"}, p).InSitemap)
	}
}
