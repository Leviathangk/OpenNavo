package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/seo"
	"gorm.io/gorm"
)

// IndexReport is a database snapshot, not a claim about search engine coverage or edge caches.
type IndexReport struct {
	GeneratedAt         time.Time          `json:"generatedAt"`
	Scope               string             `json:"scope"`
	Rows                []IndexReportCount `json:"rows"`
	MachineOnlyOverview map[string]int     `json:"machineOnlyOverview"`
}

type IndexReportCount struct {
	PageType  string `json:"pageType"`
	Locale    string `json:"locale"`
	Decision  string `json:"decision"`
	InSitemap bool   `json:"inSitemap"`
	Count     int    `json:"count"`
}

type reportKey struct {
	kind, locale, decision string
	sitemap                bool
}
type reportCounts map[reportKey]int

func (counts reportCounts) add(kind, locale, reason string, indexable, sitemap bool) {
	if indexable {
		reason = "indexable"
	} else if reason == "" {
		reason = "noindex_unspecified"
	}
	counts[reportKey{kind, locale, reason, sitemap}]++
}
func (counts reportCounts) rows() []IndexReportCount {
	rows := make([]IndexReportCount, 0, len(counts))
	for k, n := range counts {
		rows = append(rows, IndexReportCount{k.kind, k.locale, k.decision, k.sitemap, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.PageType != b.PageType {
			return a.PageType < b.PageType
		}
		if a.Locale != b.Locale {
			return a.Locale < b.Locale
		}
		if a.Decision != b.Decision {
			return a.Decision < b.Decision
		}
		return !a.InSitemap && b.InSitemap
	})
	return rows
}

// BuildIndexReport enforces read-only at the database transaction boundary, including every helper.
// No Redis, HTTP, cache fills, page-state refreshes or migrations are involved.
// sitemapFonts defaults to false, matching Web; callers must pass its deployed setting.
func (s *Store) BuildIndexReport(ctx context.Context, now time.Time, siteURL string, sitemapFonts ...bool) (IndexReport, error) {
	var report IndexReport
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		report, err = (&Store{DB: tx}).buildIndexReport(domain.WithLinks(ctx, true), now, siteURL, sitemapFonts...)
		return err
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	return report, err
}
func (s *Store) buildIndexReport(ctx context.Context, now time.Time, siteURL string, sitemapFonts ...bool) (IndexReport, error) {
	out := IndexReport{GeneratedAt: now.UTC(), Scope: "Current cask/link routes (including hidden/disabled), all site locales, unfiltered versions pagination; font overview URLs are counted as fonts, not overview. Other entities and static routes count their base page only. Excludes removed/alias URLs, search/filter variants, directory pagination and A-Z bucket routes. Sitemap membership is the current generation policy, not a fetched/cached sitemap.", MachineOnlyOverview: map[string]int{}}
	counts := reportCounts{}
	policy, err := s.IndexPolicy(ctx)
	if err != nil {
		return out, err
	}
	for _, locale := range seo.Locales {
		if policy.LocaleOpen(locale) {
			out.MachineOnlyOverview[locale] = 0
		}
	}
	// Bounded batches keep release and intent facts off the full-catalog heap.
	for after := int64(0); ; {
		rows, err := s.indexRows(ctx, "p.id>?", "ORDER BY p.id LIMIT 100", after)
		if err != nil {
			return out, err
		}
		if len(rows) == 0 {
			break
		}
		after = rows[len(rows)-1].ID
		if err = s.PopulateIntentEligibility(ctx, rows, policy, siteURL); err != nil {
			return out, err
		}
		ids := make([]int64, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		states, err := s.PageStates(ctx, ids)
		if err != nil {
			return out, err
		}
		stored := map[int64]map[string]seo.NotesFacts{}
		for _, state := range states {
			if state.PageKind == "versions" && state.PageNumber == 1 && state.NotesChars != nil && state.NotesEntries != nil {
				if stored[state.PackageID] == nil {
					stored[state.PackageID] = map[string]seo.NotesFacts{}
				}
				stored[state.PackageID][state.Locale] = seo.NotesFacts{Chars: *state.NotesChars, Entries: *state.NotesEntries}
			}
		}
		if err = s.PopulatePageNotes(ctx, rows, 1); err != nil {
			return out, err
		}
		for _, row := range rows {
			for _, locale := range seo.Locales {
				for _, kind := range []seo.PageKind{seo.PageOverview, seo.PageVersions, seo.PageDetails, seo.PageDependencies, seo.PageTroubleshooting, seo.PageAlternatives, seo.PageUninstall} {
					d := seo.DecidePage(row.Facts(), seo.PageRef{Kind: kind, Locale: locale, Page: 1}, policy)
					inSitemap := d.InSitemap
					if kind == seo.PageVersions {
						storedRow := row
						storedRow.NotesPages = map[int]map[string]seo.NotesFacts{1: stored[row.ID]}
						inSitemap = seo.DecidePage(storedRow.Facts(), seo.PageRef{Kind: kind, Locale: locale, Page: 1}, policy).InSitemap
					}
					// Package sitemaps include subpages only when that locale's overview is listed.
					inSitemap = inSitemap && seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageOverview, Locale: locale, Page: 1}, policy).InSitemap
					if row.IsFont && (len(sitemapFonts) == 0 || !sitemapFonts[0]) {
						inSitemap = false
					}
					name := string(kind)
					if kind == seo.PageOverview && row.IsFont {
						name = "fonts"
					}
					counts.add(name, locale, d.Reason, seo.DecisionRobots(d.Robots).Index, inSitemap)
				}
			}
			entries, err := s.TimelineEntries(ctx, row.ID, row.SourceLocale)
			if err != nil {
				return out, err
			}
			for page := 2; page <= (len(entries)+seo.VersionsPageSize-1)/seo.VersionsPageSize; page++ {
				one := []IndexRow{row}
				if err = s.PopulatePageNotes(ctx, one, page); err != nil {
					return out, err
				}
				for _, locale := range seo.Locales {
					d := seo.DecidePage(one[0].Facts(), seo.PageRef{Kind: seo.PageVersions, Locale: locale, Page: page}, policy)
					counts.add("versions", locale, d.Reason, seo.DecisionRobots(d.Robots).Index, false)
				}
			}
		}
		var machine []struct {
			Locale string
			Count  int
		}
		if err = s.DB.WithContext(ctx).Raw(`SELECT t.locale,count(*) FROM package_i18n t JOIN packages p ON p.id=t.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id IN ? AND NOT COALESCE(m.hidden,false) AND t.locale<>p.source_locale AND t.status='machine' AND t.machine_translated AND btrim(t.summary)<>'' AND btrim(t.description)<>'' AND t.source_hash ~ '^[0-9a-f]{64}$' GROUP BY t.locale`, ids).Scan(&machine).Error; err != nil {
			return out, err
		}
		for _, r := range machine {
			if policy.LocaleOpen(r.Locale) {
				out.MachineOnlyOverview[r.Locale] += r.Count
			}
		}
	}
	if err = s.reportOtherPages(ctx, now, policy, counts); err != nil {
		return out, err
	}
	out.Rows = counts.rows()
	return out, nil
}

func (s *Store) reportOtherPages(ctx context.Context, now time.Time, policy seo.Policy, counts reportCounts) error {
	add := func(kind, reason string, eligible, sitemap bool) {
		for _, locale := range seo.Locales {
			why := reason
			ok := eligible
			if !policy.LocaleOpen(locale) {
				ok = false
				why = seo.ReasonLocaleClosed
			}
			counts.add(kind, locale, why, ok, ok && sitemap)
		}
	}
	articles, authors, err := s.ArticleSitemap(ctx)
	if err != nil {
		return err
	}
	listedArticles := false
	for _, article := range articles {
		for _, locale := range seo.Locales {
			_, ready := article.Locales[locale]
			reason := "locale_not_ready"
			if !policy.LocaleOpen(locale) {
				ready = false
				reason = seo.ReasonLocaleClosed
			}
			counts.add("articles", locale, reason, ready, ready)
			listedArticles = listedArticles || ready
		}
	}
	var authorCount int64
	if err = s.DB.WithContext(ctx).Table("article_authors").Count(&authorCount).Error; err != nil {
		return err
	}
	for range authors {
		add("authors", "", true, true)
	}
	for range int(authorCount) - len(authors) {
		add("authors", "no_published_articles", false, false)
	}
	categories, err := s.PublicCategories(domain.WithLinks(ctx, false), "en-US")
	if err != nil {
		return err
	}
	for _, category := range categories {
		add("categories", "empty_category", category.PackageCount > 0, !category.HiddenByDefault)
	}
	var collections []struct{ Count int }
	if err = s.DB.WithContext(ctx).Raw(`SELECT (SELECT count(*) FROM collection_items ci JOIN packages p ON p.id=ci.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE ci.collection_id=c.id AND p.kind='cask' AND p.removed_at IS NULL AND NOT (p.disabled OR p.stale_disabled) AND NOT COALESCE(m.hidden,false)) AS count FROM collections c WHERE `+VisibleCollection, now, now).Scan(&collections).Error; err != nil {
		return err
	}
	for _, collection := range collections {
		add("collections", "empty_collection", collection.Count > 0, true)
	}
	firstReady := map[string]bool{}
	for current := 1; ; current++ {
		labels, _, err := s.BackgroundLabels(ctx, "", current, 100)
		if err != nil {
			return err
		}
		for i, label := range labels {
			owners, err := s.BackgroundOwners(ctx, label)
			if err != nil {
				return err
			}
			fresh := false
			for _, owner := range owners {
				if owner.Visible && facts.NewProof(owner.SourceURL, owner.CheckedAt, facts.StaleAfter, "verified", now).Status == "verified" {
					fresh = true
				}
			}
			ready, err := s.BackgroundReadyLocales(ctx, label, now)
			if err != nil {
				return err
			}
			for _, locale := range seo.Locales {
				ok := fresh && slices.Contains(ready, locale)
				reason := "background_item_not_ready"
				if !policy.LocaleOpen(locale) {
					ok = false
					reason = seo.ReasonLocaleClosed
				}
				counts.add("background-items", locale, reason, ok, ok)
				if current == 1 && i < 24 && ok {
					firstReady[locale] = true
				}
			}
		}
		if len(labels) < 100 {
			break
		}
	}
	var publicPackages int64
	if err = s.DB.WithContext(ctx).Raw("SELECT count(*)" + publicJoins + " WHERE " + publicVisible + " AND NOT (p.disabled OR p.stale_disabled)").Scan(&publicPackages).Error; err != nil {
		return err
	}
	// Fixed route manifest from web pages/ and nuxt.config.ts. Dynamic entity bases are above.
	for _, path := range []string{"/", "/about", "/apps", "/articles", "/background-items", "/brewfile", "/browse", "/browse/fonts", "/categories", "/collections", "/discover", "/download", "/feedback", "/rankings", "/recent", "/search", "/security", "/security/clamav", "/security/firewall", "/security/xprotect"} {
		for _, locale := range seo.Locales {
			ok, reason := true, ""
			listed := true
			switch path {
			case "/search", "/brewfile":
				ok, reason = false, "private_page"
				listed = false
			case "/articles":
				ok, reason = len(articles) > 0, "empty_articles"
				listed = listedArticles
			case "/collections":
				ok, reason = len(collections) > 0, "empty_collection_list"
			case "/rankings", "/recent":
				ok, reason = publicPackages > 0, "empty_package_list"
			case "/background-items":
				ok, reason = firstReady[locale], "background_list_not_ready"
				listed = ok
			}
			if !policy.LocaleOpen(locale) {
				ok = false
				reason = seo.ReasonLocaleClosed
			}
			counts.add("static", locale, reason, ok, listed && policy.LocaleOpen(locale))
		}
	}
	return nil
}

// ValidateIndexReportFormat rejects typos rather than silently changing output formats.
func ValidateIndexReportFormat(format string) error {
	if format != "json" && format != "text" {
		return fmt.Errorf("format must be json or text")
	}
	return nil
}
