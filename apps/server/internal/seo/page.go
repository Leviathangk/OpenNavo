package seo

import (
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

const IndexableRobots = "index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1"

type PageKind string

const (
	PageTroubleshooting      PageKind = "troubleshooting"
	PageAlternatives         PageKind = "alternatives"
	PageUninstall            PageKind = "uninstall"
	PageOverview             PageKind = "overview"
	PageVersions             PageKind = "versions"
	PageDependencies         PageKind = "dependencies"
	PageDetails              PageKind = "details"
	VersionsPageSize                  = 24
	TroubleshootingMinIssues          = 3
	// A versions page needs notes on this many releases: one note only repeats the overview's latest release.
	VersionsMinNoteEntries = 3
)

// Page reasons other packages act on.
const (
	// ReasonLocaleClosed: the language is closed for indexing (pages.openLocales).
	ReasonLocaleClosed = "locale_closed"
	// ReasonSupportingPage: a dependencies tab with content supports the overview; it keeps its own
	// canonical and followable links but is never indexed, listed in a sitemap or announced.
	ReasonSupportingPage = "supporting_page"
)

// NotesFacts measures the notes one versions page shows in one language: readable characters, and how many
// releases on the page carry them. Notes that only say no details are available count as neither.
type NotesFacts struct {
	Chars   int
	Entries int
}

type PageRef struct {
	Kind     PageKind
	Locale   string
	Page     int
	Filtered bool
}

type PageDecision struct {
	Robots        string   `json:"robots"`
	CanonicalPath string   `json:"canonicalPath"`
	Hreflang      []string `json:"hreflang"`
	InSitemap     bool     `json:"inSitemap"`
	Announce      bool     `json:"announce"`
	Reason        string   `json:"reason"`
}

func OverviewPath(f Facts, locale string) string {
	return i18n.Metadata[i18n.LocaleCode(locale)].Prefix + "/apps/" + url.PathEscape(f.Token)
}

// TranslationReady describes a published snapshot, not its freshness against today's source.
// The write path checks the source hash when publishing; source edits do not invalidate snapshots.
func TranslationReady(f Facts, locale string) bool {
	return locale == f.SourceLocale || slices.Contains(f.ReadyLocales, locale)
}

// DecidePage is the single policy for a concrete URL. Hreflang is filtered using the same page
// number and notes threshold for every language, making the links reciprocal. A language closed for
// indexing is noindex on every page and appears in no page's hreflang; pages noindexed by their shape
// (install details, dependencies, filtered views) keep that reason in every language.
func DecidePage(f Facts, ref PageRef, p Policy) PageDecision {
	d := PageDecision{Robots: "noindex, follow", Hreflang: []string{}}
	if !i18n.Valid(ref.Locale) || ref.Page < 0 {
		d.Reason = "invalid_page"
		return d
	}
	if ref.Kind == PageOverview && f.Token == "opennavo" {
		d.CanonicalPath = i18n.Metadata[i18n.LocaleCode(ref.Locale)].Prefix + "/download"
		d.Reason = "brand_redirect"
		return d
	}
	if f.Kind == "link" && (ref.Kind == PageDetails || ref.Kind == PageDependencies) {
		d.Reason = "unsupported_page"
		return d
	}
	if ref.Kind == PageAlternatives || ref.Kind == PageUninstall {
		return decideIntentPage(f, ref, p)
	}
	if ref.Kind == PageTroubleshooting {
		return decideTroubleshootingPage(f, ref, p)
	}
	if ref.Kind == PageDetails {
		d.CanonicalPath, d.Reason = OverviewPath(f, ref.Locale), "consolidated"
		return d
	}
	if ref.Kind == PageDependencies && !f.HasDependencyContent {
		d.CanonicalPath, d.Reason = OverviewPath(f, ref.Locale), "duplicate_of_overview"
		return d
	}
	if ref.Kind == PageDependencies {
		d.Reason = ReasonSupportingPage
		return d
	}
	if ref.Kind != PageOverview && ref.Kind != PageVersions {
		d.Reason = "unsupported_page"
		return d
	}
	if ref.Filtered {
		d.Reason = "filtered_view"
		return d
	}
	if !p.LocaleOpen(ref.Locale) {
		d.Reason = ReasonLocaleClosed
		return d
	}
	if f.IsFont && !p.FontsSitemap {
		d.Reason = "font_page"
		return d
	}
	if ref.Kind == PageVersions && ref.Locale != f.SourceLocale {
		d.Reason = "versions_source_only"
		return d
	}
	entity := Decide(f, p)
	if f.Hidden || f.Unavailable() || !entity.Indexable {
		d.Reason = entity.Reason
		if d.Reason == "" {
			_, d.Reason = Evaluate(f)
		}
		return d
	}
	page := max(1, ref.Page)
	minimum := p.VersionsMinNotesChars
	if minimum <= 0 {
		minimum = 300
	}
	locales := []string{}
	for _, locale := range entity.Locales {
		if !i18n.ValidApp(locale) && !entity.Eligible {
			continue
		}
		strict := !i18n.ValidApp(locale) || (p.TranslationMode == "enforce" && (len(p.TranslationLocales) == 0 || slices.Contains(p.TranslationLocales, locale)))
		if strict && !TranslationReady(f, locale) {
			continue
		}
		if ref.Kind == PageVersions {
			if locale != f.SourceLocale {
				continue
			}
			notes := f.NotesPages[page][locale]
			if notes.Chars < minimum || notes.Entries < VersionsMinNoteEntries {
				continue
			}
		}
		locales = append(locales, locale)
	}
	if !slices.Contains(locales, ref.Locale) {
		d.Reason = "locale_not_ready"
		if ref.Kind == PageVersions && TranslationReady(f, ref.Locale) {
			d.Reason = "thin_notes_page"
		}
		return d
	}
	d.Robots, d.Hreflang, d.Announce = IndexableRobots, locales, true
	d.InSitemap = page == 1 && p.SitemapAppAllowed(f, ref.Locale) && (ref.Kind != PageVersions || p.VersionsSitemap)
	return d
}

func decideTroubleshootingPage(f Facts, ref PageRef, p Policy) PageDecision {
	d := PageDecision{Robots: "noindex, follow", CanonicalPath: OverviewPath(f, ref.Locale), Hreflang: []string{}, Reason: "insufficient_verified_issues"}
	if ref.Filtered || ref.Page > 1 {
		d.Reason = "filtered_view"
		return d
	}
	overview := DecidePage(f, PageRef{Kind: PageOverview, Locale: ref.Locale, Page: 1}, p)
	if !overview.Announce {
		d.Reason = overview.Reason
		return d
	}
	if f.TroubleshootingIssues[ref.Locale] < TroubleshootingMinIssues {
		return d
	}
	for _, locale := range overview.Hreflang {
		if f.TroubleshootingIssues[locale] >= TroubleshootingMinIssues {
			d.Hreflang = append(d.Hreflang, locale)
		}
	}
	d.Robots, d.CanonicalPath, d.InSitemap, d.Announce, d.Reason = IndexableRobots, "", overview.InSitemap, true, ""
	return d
}

// ReadableChars counts visible text characters; CJK letters count twice. Callers supply
// rendered Markdown text, so formatting and link destinations never meet the threshold.
func ReadableChars(text string) int {
	n := 0
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			n++
			if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
				n++
			}
		}
	}
	return n
}

func decideIntentPage(f Facts, ref PageRef, p Policy) PageDecision {
	d := PageDecision{Robots: "noindex, follow", CanonicalPath: OverviewPath(f, ref.Locale), Hreflang: []string{}, Reason: "intent_not_eligible"}
	if !p.IntentPagesEnabled {
		d.Reason = "intent_disabled"
		return d
	}
	if ref.Filtered || ref.Page > 1 {
		d.Reason = "filtered_view"
		return d
	}
	if ref.Kind == PageUninstall && f.Kind != "cask" {
		d.Reason = "unsupported_page"
		return d
	}
	overview := DecidePage(f, PageRef{Kind: PageOverview, Locale: ref.Locale, Page: 1}, p)
	if !overview.Announce {
		d.Reason = overview.Reason
		return d
	}
	for _, locale := range overview.Hreflang {
		if slices.Contains(f.IntentLocales[ref.Kind], locale) {
			d.Hreflang = append(d.Hreflang, locale)
		}
	}
	if !slices.Contains(d.Hreflang, ref.Locale) {
		d.Hreflang = []string{}
		return d
	}
	d.Robots, d.CanonicalPath, d.InSitemap, d.Announce, d.Reason = IndexableRobots, "", overview.InSitemap, true, ""
	return d
}
