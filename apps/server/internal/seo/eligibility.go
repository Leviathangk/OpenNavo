// Package seo decides which app pages search engines should index (WP11). SSR robots, sitemap,
// hreflang and the admin impact report all read the same Decision, so they cannot disagree.
package seo

import (
	"slices"
	"strings"
	"time"
)

// Reasons a page is not recommended for indexing.
const (
	ReasonHidden         = "hidden"          // hidden from browsing in admin
	ReasonForcedNoindex  = "forced_noindex"  // manual override
	ReasonUnavailable    = "unavailable"     // disabled upstream or by the stale rule
	ReasonMissingSummary = "missing_summary" // no usable summary in the source language
	ReasonThin           = "thin"            // no version, system requirement or install count to show
)

// Overrides set per app in admin.
const (
	OverrideAuto    = "auto"
	OverrideIndex   = "index"
	OverrideNoindex = "noindex"
)

// Locales are the six published interface languages, in hreflang order.
var Locales = []string{"en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU"}

// Facts are what the rule needs about one app.
type Facts struct {
	Hidden           bool
	Disabled         bool // upstream disabled or stale-disabled
	Name, Token      string
	Summary          string // source-language summary
	Version          string
	HasRequirements  bool
	Installs30d      int
	Override         string
	FirstSeenAt      time.Time
	SourceLocale     string
	PublishedLocales []string // locales whose summary is published (source, machine or manual)
}

// Policy mirrors the admin settings pages.indexPolicy.
type Policy struct {
	Mode  string // off | shadow | enforce
	Scope string // new | all
	Since time.Time
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
	if f.Disabled {
		return false, ReasonUnavailable
	}
	summary := strings.TrimSpace(f.Summary)
	if summary == "" || strings.EqualFold(summary, strings.TrimSpace(f.Name)) || strings.EqualFold(summary, f.Token) {
		return false, ReasonMissingSummary
	}
	version := strings.TrimSpace(strings.SplitN(f.Version, ",", 2)[0])
	if (version == "" || strings.EqualFold(version, "latest")) && !f.HasRequirements && f.Installs30d <= 0 {
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
// all six languages); enforce applies the rule to apps in scope. "new" covers apps first seen after
// Since, so existing pages are not noindexed overnight. A manual override is an explicit decision,
// so under enforce it applies to any app, in scope or not.
func Decide(f Facts, p Policy) Decision {
	eligible, reason := Evaluate(f)
	d := Decision{Indexable: true, Locales: Locales, Eligible: eligible, EligibleReason: reason}
	inScope := p.Scope == "all" || (p.Scope == "new" && !p.Since.IsZero() && f.FirstSeenAt.After(p.Since))
	overridden := f.Override == OverrideIndex || f.Override == OverrideNoindex
	if p.Mode != "enforce" || !inScope && !overridden {
		return d
	}
	d.Enforced = true
	d.Indexable, d.Reason = eligible, reason
	if eligible {
		d.Locales = IndexedLocales(f)
	} else {
		d.Locales = []string{}
	}
	return d
}
