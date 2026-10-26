// Package urlnotify is the URL change feed (WP4): catalog changes become refreshed pages and
// search engine notifications, in the order the plan requires — Go API caches, Nitro page caches,
// Cloudflare (only while HTML edge caching is on), then IndexNow.
package urlnotify

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
)

// bulkContentRows: more content rows than this in one transaction (a category rename touches every
// app in it) refresh pages but are not announced to IndexNow, since the pages barely changed.
const bulkContentRows = 50

// longCachedSections hold the pages the web caches at the edge for hours (app pages with their
// versions, details and dependencies tabs, articles and authors); they must match the long-TTL page
// types in apps/web/server/utils/edgeCache.ts.
// Home and list pages expire within minutes and need no purge. The whole host is never purged: that
// would also evict the 30-day share images (/_og/) and the hashed build assets (/_nuxt/).
var longCachedSections = []string{"/apps", "/articles", "/authors", "/collections"}

// Plan lists what one run refreshes and announces.
type Plan struct {
	Decisions map[string]seo.PageDecision
	// Paths are page-cache prefixes, one per language and token; each covers that app's tabs and payloads.
	Paths []string
	// PurgePrefixes are the same pages as Cloudflare purge prefixes (host and path, no scheme), each
	// covering the page, its query variants, tabs and payloads.
	PurgePrefixes []string
	// IndexNowURLs are absolute overview URLs whose state changed.
	IndexNowURLs []string
}

func localePrefix(locale string) string {
	return i18n.Metadata[i18n.LocaleCode(locale)].Prefix
}

// purgeHost is the site host that Cloudflare purge prefixes start with, in place of a scheme.
func purgeHost(siteURL string) string {
	site, err := url.Parse(siteURL)
	if err != nil {
		return ""
	}
	return site.Host
}

// sectionPrefixes lists the long-cached sections in every language as purge prefixes, for purges
// wider than single pages: a web release, or more changed pages than one run purges one by one.
func sectionPrefixes(siteURL string) []string {
	host := purgeHost(siteURL)
	prefixes := make([]string, 0, len(seo.Locales)*len(longCachedSections))
	for _, locale := range seo.Locales {
		for _, section := range longCachedSections {
			prefixes = append(prefixes, host+localePrefix(locale)+section)
		}
	}
	return prefixes
}

func appendUnique(list []string, seen map[string]bool, value string) []string {
	if seen[value] {
		return list
	}
	seen[value] = true
	return append(list, value)
}

// announcementPackages shares the bulk-edit rule with the expensive fact loader.
func announcementPackages(changes []store.URLChange) map[int64]bool {
	contentGroups := map[time.Time]int{}
	for _, change := range changes {
		if change.Reason == "content" {
			contentGroups[change.CreatedAt]++
		}
	}
	announce := map[int64]bool{}
	for _, change := range changes {
		if change.Reason != "content" || contentGroups[change.CreatedAt] <= bulkContentRows {
			announce[change.PackageID] = true
		}
	}
	return announce
}

// BuildPlan turns pending changes into paths and URLs. current holds each package's current token
// and removal state; index holds index facts for live packages.
func BuildPlan(changes []store.URLChange, current map[int64]store.URLChange, index map[int64]store.IndexRow, policy seo.Policy, siteURL string) Plan {
	siteURL, host := strings.TrimRight(siteURL, "/"), purgeHost(siteURL)
	tokens := map[int64][]string{}
	locales := map[int64][]string{}
	announce := announcementPackages(changes)
	order := []int64{}
	for _, change := range changes {
		if _, ok := tokens[change.PackageID]; !ok {
			order = append(order, change.PackageID)
		}
		if previous, seen := locales[change.PackageID]; !seen {
			locales[change.PackageID] = change.Locales
		} else if previous == nil || change.Locales == nil {
			locales[change.PackageID] = nil
		} else {
			locales[change.PackageID] = append(append([]string{}, previous...), change.Locales...)
		}
		for _, token := range []string{change.Token, ptr(change.PreviousToken)} {
			if token != "" && !slices.Contains(tokens[change.PackageID], token) {
				tokens[change.PackageID] = append(tokens[change.PackageID], token)
			}
		}
	}
	plan := Plan{Decisions: map[string]seo.PageDecision{}}
	seenPath, seenPurge, seenAnnounce := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range order {
		now, known := current[id]
		list := tokens[id]
		if known && !slices.Contains(list, now.Token) {
			list = append([]string{now.Token}, list...)
		}
		for _, token := range list {
			for _, locale := range scopedLocales(locales[id]) {
				path := localePrefix(locale) + "/apps/" + token
				plan.Paths = appendUnique(plan.Paths, seenPath, path)
				plan.PurgePrefixes = appendUnique(plan.PurgePrefixes, seenPurge, host+path)
				if token == "opennavo" {
					download := localePrefix(locale) + "/download"
					plan.Paths = appendUnique(plan.Paths, seenPath, download)
					plan.PurgePrefixes = appendUnique(plan.PurgePrefixes, seenPurge, host+download)
				}
			}
		}
		if !announce[id] {
			continue
		}
		row, live := index[id]
		for _, token := range list {
			for _, locale := range scopedLocales(locales[id]) {
				if live && known && token == now.Token {
					for _, kind := range []seo.PageKind{seo.PageOverview, seo.PageVersions, seo.PageDependencies, seo.PageDetails, seo.PageAlternatives, seo.PageUninstall, seo.PageTroubleshooting} {
						d := seo.DecidePage(row.Facts(), seo.PageRef{Kind: kind, Locale: locale, Page: 1}, policy)
						if !d.Announce {
							continue
						}
						path := localePrefix(locale) + "/apps/" + token
						if kind != seo.PageOverview {
							path += "/" + string(kind)
						}
						plan.IndexNowURLs = appendUnique(plan.IndexNowURLs, seenAnnounce, siteURL+path)
						plan.Decisions[siteURL+path] = d
					}
				} else if token != "opennavo" && (locales[id] == nil || seo.LocaleOpen(policy.OpenLocales, locale)) {
					// The permanent brand redirect is not a catalog URL, even after removal.
					// Redirects and removals must still be announced, so engines can retire old URLs.
					plan.IndexNowURLs = appendUnique(plan.IndexNowURLs, seenAnnounce, siteURL+localePrefix(locale)+"/apps/"+token)
				}
			}
		}
	}
	return plan
}

func ptr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// ContentPlan covers changed pages outside the catalog in all site languages.
func ContentPlan(changes []store.ContentURLChange, siteURL string) Plan {
	siteURL, host := strings.TrimRight(siteURL, "/"), purgeHost(siteURL)
	var plan Plan
	seenPath, seenPurge, seenAnnounce := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, change := range changes {
		for _, path := range change.Paths {
			for _, locale := range scopedLocales(change.Locales) {
				full := localePrefix(locale) + path
				plan.Paths = appendUnique(plan.Paths, seenPath, full)
				// A home path would purge a whole language, or the whole host with its share images and
				// build assets; section roots would evict every detail page. These short-TTL
				// pages expire within minutes instead.
				if strings.Trim(path, "/") != "" && (change.PurgeSections || !slices.Contains(longCachedSections, "/"+strings.Trim(path, "/"))) {
					plan.PurgePrefixes = appendUnique(plan.PurgePrefixes, seenPurge, host+full)
				}
				if change.Announce {
					plan.IndexNowURLs = appendUnique(plan.IndexNowURLs, seenAnnounce, siteURL+full)
				}
			}
		}
	}
	return plan
}

// Merge adds another plan's entries that this one lacks.
func (p Plan) Merge(other Plan) Plan {
	merge := func(list, more []string) []string {
		seen := map[string]bool{}
		for _, value := range list {
			seen[value] = true
		}
		for _, value := range more {
			list = appendUnique(list, seen, value)
		}
		return list
	}
	decisions := map[string]seo.PageDecision{}
	for url, d := range p.Decisions {
		decisions[url] = d
	}
	for url, d := range other.Decisions {
		decisions[url] = d
	}
	return Plan{Paths: merge(p.Paths, other.Paths), PurgePrefixes: merge(p.PurgePrefixes, other.PurgePrefixes), IndexNowURLs: merge(p.IndexNowURLs, other.IndexNowURLs), Decisions: decisions}
}

// Canonical order and deduplication keep plan output stable regardless of write order.
func scopedLocales(scope []string) []string {
	if scope == nil {
		return seo.Locales
	}
	out := []string{}
	for _, locale := range seo.Locales {
		if slices.Contains(scope, locale) {
			out = append(out, locale)
		}
	}
	return out
}
