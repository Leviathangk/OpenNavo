// Package seo decides which app pages search engines should index (WP11). SSR robots, sitemap,
// hreflang and the admin impact report all read the same Decision, so they cannot disagree.
package seo

import (
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/i18n"
)

// Reasons a page is not recommended for indexing.
const (
	ReasonHidden         = "hidden"          // hidden from browsing in admin
	ReasonForcedNoindex  = "forced_noindex"  // manual override
	ReasonUnavailable    = "unavailable"     // Homebrew no longer offers it (Facts.Unavailable)
	ReasonMissingSummary = "missing_summary" // no usable summary in the source language
	ReasonThin           = "thin"            // no version, system requirement or install count to show
)

// Overrides set per app in admin.
const (
	OverrideAuto    = "auto"
	OverrideIndex   = "index"
	OverrideNoindex = "noindex"
)

// Locales are the generated website/content languages, in hreflang order.
var Locales = func() []string {
	out := make([]string, 0, len(i18n.SiteLocales))
	for _, locale := range i18n.SiteLocales {
		out = append(out, string(locale))
	}
	return out
}()

// Facts are what the rule needs about one app.
type Facts struct {
	TroubleshootingIssues map[string]int
	IntentLocales         map[PageKind][]string
	Kind                  string
	Description           string
	HasSubstantiveContent bool
	ReadyLocales          []string
	NotesPages            map[int]map[string]NotesFacts
	HasDependencyContent  bool
	Hidden                bool
	Disabled              bool // disabled by Homebrew
	Deprecated            bool // deprecated by Homebrew
	Stale                 bool // paused by OpenNavo's 90-day rule; Homebrew may still offer it
	Name, Token           string
	Summary               string // source-language summary
	Version               string
	HasRequirements       bool
	Installs30d           int
	Rank30d               int // stable installs_30d rank; zero means unranked
	IsFont                bool
	Override              string
	FirstSeenAt           time.Time
	SourceLocale          string
	PublishedLocales      []string // locales whose summary is published (source, machine or manual)
}

// Policy mirrors the admin pages settings, including pages.indexPolicy.
type Policy struct {
	SitemapMinInstalls    int // <= 0 uses DefaultSitemapMinInstalls
	SitemapNonEnglishTopN int // <= 0 uses DefaultSitemapNonEnglishTopN
	IntentPagesEnabled    bool
	TranslationLocales    []string
	TranslationMode       string
	VersionsMinNotesChars int
	VersionsSitemap       bool
	FontsSitemap          bool
	Mode                  string // off | shadow | enforce
	Scope                 string // new | all
	Since                 time.Time
	// OpenLocales are the languages open for indexing (pages.openLocales); nil opens every site language.
	OpenLocales []string
}

// LocaleOpen reports whether an openLocales list lets pages in a language be indexed; a nil list opens
// every site language. Pages in a closed language still render, but stay noindex and out of every
// hreflang set, sitemap and IndexNow announcement.
func LocaleOpen(open []string, locale string) bool {
	return open == nil || slices.Contains(open, locale)
}

// LocaleOpen applies the policy's openLocales list.
func (p Policy) LocaleOpen(locale string) bool {
	return LocaleOpen(p.OpenLocales, locale)
}

// openOnly keeps the languages open for indexing, in their given order.
func (p Policy) openOnly(locales []string) []string {
	if p.OpenLocales == nil {
		return locales
	}
	out := []string{}
	for _, locale := range locales {
		if p.LocaleOpen(locale) {
			out = append(out, locale)
		}
	}
	return out
}

// Decision is what pages and the sitemap act on.
type Decision struct {
	Indexable bool
	Reason    string
	// Locales to index: self-canonical pages listed in hreflang and the sitemap.
	Locales []string
	// Eligible and EligibleReason are the rule's verdict regardless of mode, for the shadow report.
	Eligible       bool
	EligibleReason string
	Enforced       bool
}

// Unavailable reports whether Homebrew no longer offers the app: disabled, or deprecated and no
// longer updated. An app paused only by the 90-day rule still installs with Homebrew, so its pages
// stay in search (and the sitemap) like any other.
func (f Facts) Unavailable() bool {
	return f.Disabled || f.Deprecated && f.Stale
}

// Evaluate applies the computable rule and the manual override.
func Evaluate(f Facts) (bool, string) {
	if f.Hidden {
		return false, ReasonHidden
	}
	switch f.Override {
	case OverrideNoindex:
		return false, ReasonForcedNoindex
	case OverrideIndex:
		return true, ""
	}
	if f.Unavailable() {
		return false, ReasonUnavailable
	}
	summary := strings.TrimSpace(f.Summary)
	if summary == "" || strings.EqualFold(summary, strings.TrimSpace(f.Name)) || strings.EqualFold(summary, f.Token) {
		return false, ReasonMissingSummary
	}
	version := strings.TrimSpace(strings.SplitN(f.Version, ",", 2)[0])
	if (version == "" || strings.EqualFold(version, "latest")) && !f.HasRequirements && f.Installs30d <= 0 && !f.HasSubstantiveContent && strings.TrimSpace(f.Description) == "" {
		return false, ReasonThin
	}
	return true, ""
}

// IndexedLocales keeps the published languages in hreflang order; the source language always counts
// once the app is eligible, because its page shows the original text.
func IndexedLocales(f Facts) []string {
	out := []string{}
	for _, locale := range Locales {
		if locale == f.SourceLocale || slices.Contains(f.PublishedLocales, locale) {
			out = append(out, locale)
		}
	}
	return out
}

// Decide applies the policy. off and shadow keep today's behaviour (every listed page indexable in
// every language open for indexing); enforce applies the rule to apps in scope. "new" covers apps first
// seen after Since, so existing pages are not noindexed overnight. A manual override is an explicit
// decision, so under enforce it applies to any app, in scope or not. Languages closed for indexing are
// never among the Locales, whatever the mode or override.
func Decide(f Facts, p Policy) Decision {
	eligible, reason := Evaluate(f)
	d := Decision{Indexable: true, Locales: p.openOnly(Locales), Eligible: eligible, EligibleReason: reason}
	inScope := p.Scope == "all" || (p.Scope == "new" && !p.Since.IsZero() && f.FirstSeenAt.After(p.Since))
	overridden := f.Override == OverrideIndex || f.Override == OverrideNoindex
	if p.Mode != "enforce" || !inScope && !overridden {
		return d
	}
	d.Enforced = true
	d.Indexable, d.Reason = eligible, reason
	if eligible {
		d.Locales = p.openOnly(IndexedLocales(f))
	} else {
		d.Locales = []string{}
	}
	return d
}

const (
	DefaultSitemapMinInstalls    = 50
	DefaultSitemapNonEnglishTopN = 500
)

// SitemapAppAllowed only limits sitemap inclusion, never robots, hreflang or announcements.
// Links and fonts have no install analytics. Page-level exclusions still apply.
func (p Policy) SitemapAppAllowed(f Facts, locale string) bool {
	if f.Override == OverrideIndex {
		return true
	}
	if f.Kind == "link" || f.IsFont {
		return f.Override != OverrideNoindex
	}
	minimum, top := p.SitemapMinInstalls, p.SitemapNonEnglishTopN
	if minimum <= 0 {
		minimum = DefaultSitemapMinInstalls
	}
	if top <= 0 {
		top = DefaultSitemapNonEnglishTopN
	}
	if locale == "en-US" {
		return f.Installs30d >= minimum
	}
	return f.Rank30d > 0 && f.Rank30d <= top
}
