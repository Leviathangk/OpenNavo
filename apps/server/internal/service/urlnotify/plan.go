// Package urlnotify is the URL change feed (WP4): catalog changes become refreshed pages and
// search engine notifications, in the order the plan requires — Go API caches, Nitro page caches,
// Cloudflare (only while HTML edge caching is on), then IndexNow.
package urlnotify

import (
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

var packageTabs = []string{"", "/versions", "/dependencies", "/details"}

// Plan lists what one run refreshes and announces.
type Plan struct {
	// Paths are page-cache prefixes, one per language and token; each covers that app's tabs and payloads.
	Paths []string
	// PurgeURLs are absolute page URLs (all tabs) for Cloudflare.
	PurgeURLs []string
	// IndexNowURLs are absolute overview URLs whose state changed.
	IndexNowURLs []string
}

func localePrefix(locale string) string {
	return i18n.Metadata[i18n.LocaleCode(locale)].Prefix
}

func appendUnique(list []string, seen map[string]bool, value string) []string {
	if seen[value] {
		return list
	}
	seen[value] = true
	return append(list, value)
}

// BuildPlan turns pending changes into paths and URLs. current holds each package's current token
// and removal state; index holds index facts for live packages.
func BuildPlan(changes []store.URLChange, current map[int64]store.URLChange, index map[int64]store.IndexRow, policy seo.Policy, siteURL string) Plan {
	siteURL = strings.TrimRight(siteURL, "/")
	contentGroups := map[time.Time]int{}
	for _, change := range changes {
		if change.Reason == "content" {
			contentGroups[change.CreatedAt]++
		}
	}
	tokens := map[int64][]string{}
	announce := map[int64]bool{}
	order := []int64{}
	for _, change := range changes {
		if _, ok := tokens[change.PackageID]; !ok {
			order = append(order, change.PackageID)
		}
		for _, token := range []string{change.Token, ptr(change.PreviousToken)} {
			if token != "" && !slices.Contains(tokens[change.PackageID], token) {
				tokens[change.PackageID] = append(tokens[change.PackageID], token)
			}
		}
		if change.Reason != "content" || contentGroups[change.CreatedAt] <= bulkContentRows {
			announce[change.PackageID] = true
		}
	}
	var plan Plan
	seenPath, seenPurge, seenAnnounce := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range order {
		now, known := current[id]
		list := tokens[id]
		if known && !slices.Contains(list, now.Token) {
			list = append([]string{now.Token}, list...)
		}
		for _, token := range list {
			for _, locale := range seo.Locales {
				path := localePrefix(locale) + "/apps/" + token
				plan.Paths = appendUnique(plan.Paths, seenPath, path)
				for _, tab := range packageTabs {
					plan.PurgeURLs = appendUnique(plan.PurgeURLs, seenPurge, siteURL+path+tab)
				}
			}
		}
		if !announce[id] {
			continue
		}
		row, live := index[id]
		for _, token := range list {
			locales := seo.Locales
			// The current token of a live app is announced only where it is indexable; former
			// tokens (now redirects) and removed apps (now 410) are announced as changed.
			if live && known && token == now.Token {
				decision := seo.Decide(row.Facts(), policy)
				if row.Hidden || !decision.Indexable {
					continue
				}
				locales = decision.Locales
			}
			for _, locale := range locales {
				plan.IndexNowURLs = appendUnique(plan.IndexNowURLs, seenAnnounce, siteURL+localePrefix(locale)+"/apps/"+token)
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

// ContentPlan covers changed pages outside the catalog (articles, authors) in all six languages.
func ContentPlan(changes []store.ContentURLChange, siteURL string) Plan {
	siteURL = strings.TrimRight(siteURL, "/")
	var plan Plan
	seenPath, seenPurge, seenAnnounce := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, change := range changes {
		for _, path := range change.Paths {
			for _, locale := range seo.Locales {
				full := localePrefix(locale) + path
				plan.Paths = appendUnique(plan.Paths, seenPath, full)
				plan.PurgeURLs = appendUnique(plan.PurgeURLs, seenPurge, siteURL+full)
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
	return Plan{Paths: merge(p.Paths, other.Paths), PurgeURLs: merge(p.PurgeURLs, other.PurgeURLs), IndexNowURLs: merge(p.IndexNowURLs, other.IndexNowURLs)}
}
