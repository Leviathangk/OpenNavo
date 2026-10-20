package seoingest

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// SyncRegistry includes pages before they receive search traffic. The registry is private,
// not a sitemap, so translation readiness is evaluated later against the shared page policy.
func (s *Service) SyncRegistry(ctx context.Context) error {
	base := strings.TrimRight(s.WebURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ErrUpstream
	}
	// Serialize with opportunity analysis, including its first cursor creation.
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('seo:opportunities',0))").Error; err != nil {
			return err
		}
		scoped := *s
		scoped.DB = tx
		return scoped.syncRegistry(ctx, base)
	})
}

func (s *Service) syncRegistry(ctx context.Context, base string) error {
	changed := false
	for _, locale := range i18n.SiteLocales {
		prefix := i18n.Metadata[locale].Prefix
		result := s.DB.WithContext(ctx).Exec(`INSERT INTO seo_urls(url,page_kind,locale,package_id,updated_at)
SELECT ?||'/apps/'||p.token||v.suffix,v.kind,?,p.id,p.updated_at FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id CROSS JOIN (VALUES('overview',''),('versions','/versions'),('dependencies','/dependencies')) v(kind,suffix)
WHERE p.kind IN ('cask','link') AND p.removed_at IS NULL AND NOT coalesce(m.hidden,false) AND (v.kind!='dependencies' OR p.kind='cask')
ON CONFLICT(url) DO UPDATE SET page_kind=EXCLUDED.page_kind,locale=EXCLUDED.locale,package_id=EXCLUDED.package_id,updated_at=GREATEST(seo_urls.updated_at,EXCLUDED.updated_at) WHERE seo_urls.page_kind IS DISTINCT FROM EXCLUDED.page_kind OR seo_urls.locale IS DISTINCT FROM EXCLUDED.locale OR seo_urls.package_id IS DISTINCT FROM EXCLUDED.package_id OR seo_urls.updated_at<EXCLUDED.updated_at`, base+prefix, string(locale))
		if result.Error != nil {
			return result.Error
		}
		changed = changed || result.RowsAffected > 0
		if err := s.DB.WithContext(ctx).Exec(`INSERT INTO seo_urls(url,page_kind,locale,updated_at)
SELECT ?||v.path,v.kind,?,v.updated_at FROM (
 SELECT '/articles/'||slug path,'article' kind,coalesce(content_updated_at,updated_at) updated_at FROM articles WHERE status='published'
 UNION ALL SELECT '/collections/'||slug,'collection',updated_at FROM collections WHERE status='published' AND (publish_at IS NULL OR publish_at<=now()) AND (unpublish_at IS NULL OR unpublish_at>now())
) v ON CONFLICT(url) DO UPDATE SET updated_at=GREATEST(seo_urls.updated_at,EXCLUDED.updated_at) WHERE seo_urls.updated_at<EXCLUDED.updated_at`, base+prefix, string(locale)).Error; err != nil {
			return err
		}
	}
	// Repair legacy English-prefix/case/escape metadata and formula bindings, including paginated URLs.
	var registered []URL
	if err := s.DB.WithContext(ctx).Table("seo_urls").Where("page_kind='en' OR page_kind<>lower(page_kind) OR split_part(url,'?',1) ~ '[A-Z%]' OR EXISTS(SELECT 1 FROM packages p WHERE p.id=seo_urls.package_id AND p.kind='formula')").Find(&registered).Error; err != nil {
		return err
	}
	for _, entry := range registered {
		parsed, err := url.Parse(entry.URL)
		if err != nil {
			return err
		}
		route := parseAuditRoute(parsed.EscapedPath())
		if route.Package {
			var resolved int64
			if route.Valid {
				if err := s.DB.WithContext(ctx).Raw(packageURLLookup, route.Token, route.Token, route.Token).Scan(&resolved).Error; err != nil {
					return err
				}
			}
			var id *int64
			if resolved > 0 {
				id = &resolved
			}
			result := s.DB.WithContext(ctx).Exec(`UPDATE seo_urls SET package_id=?,page_kind=?,locale=? WHERE id=?
 AND (package_id IS DISTINCT FROM ? OR page_kind IS DISTINCT FROM ? OR locale IS DISTINCT FROM ?)`, id, route.Kind, route.Locale, entry.ID, id, route.Kind, route.Locale)
			if result.Error != nil {
				return result.Error
			}
			changed = changed || result.RowsAffected > 0
		}
	}
	// Both observations and demand derive their inputs from persisted URL metadata.
	// Commit repairs and invalidation together while holding the analysis lock.
	if changed {
		return s.DB.WithContext(ctx).Exec("UPDATE seo_opportunity_cursors SET signature='',updated_at=clock_timestamp() WHERE source IN ('demand','measurements')").Error
	}
	return nil
}

// ExpectedPage reuses BE-1's decision. It never infers a second indexing policy.
func (s *Service) ExpectedPage(ctx context.Context, u URL) (AuditExpectation, error) {
	var expected AuditExpectation
	if u.Expected != "" {
		if err := json.Unmarshal([]byte(u.Expected), &expected); err != nil {
			return expected, err
		}
	}
	parsed, err := url.Parse(u.URL)
	if err != nil {
		return expected, err
	}
	route := parseAuditRoute(parsed.EscapedPath())
	if !route.Package {
		return expected, nil
	}
	if !route.Valid {
		return AuditExpectation{Status: 404}, nil
	}
	// Stored metadata may predate URL normalization. Audit the actual route.
	token := route.Token
	u.PageKind, u.Locale = route.Kind, route.Locale
	var resolvedID int64
	if err := s.DB.WithContext(ctx).Raw(packageURLLookup, token, token, token).Scan(&resolvedID).Error; err != nil {
		return expected, err
	}
	if resolvedID == 0 {
		return AuditExpectation{Status: 404}, nil
	}
	u.PackageID = &resolvedID
	st := &store.Store{DB: s.DB}
	row, found, err := st.IndexRowByID(ctx, *u.PackageID)
	if err != nil {
		return expected, err
	}
	if !found {
		var removed bool
		if err := s.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM packages WHERE id=? AND removed_at IS NOT NULL)", *u.PackageID).Scan(&removed).Error; err != nil {
			return expected, err
		}
		// Only the tab layout renders the removed-package summary. Standalone
		// intent pages reject removed packages before rendering their content.
		if removed && u.PageKind != "alternatives" && u.PageKind != "uninstall" {
			return AuditExpectation{Status: 410}, nil
		}
		// Do not retain yesterday's robots, alternates or content assertions.
		return AuditExpectation{Status: 404}, nil
	}
	policy, err := st.IndexPolicy(ctx)
	if err != nil {
		return expected, err
	}
	// usePackage's rename redirect drops the query. Details redirects likewise
	// land on the overview, whose policy must be evaluated instead.
	if token != row.Token {
		parsed.RawQuery = ""
	}
	if u.PageKind == "details" && row.Kind == "cask" {
		settings, err := st.SEOSettings(ctx)
		if err != nil {
			return expected, err
		}
		overview := seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageOverview, Locale: u.Locale, Page: 1}, policy)
		if settings.Settings.Pages.DetailsRedirect && overview.Robots == "index, follow" {
			u.PageKind, parsed.RawQuery = "overview", ""
		}
	}
	page := 1
	if u.PageKind == "versions" && parsed.Query().Has("page") {
		values := parsed.Query()["page"]
		if len(values) != 1 || !auditPageNumber.MatchString(values[0]) {
			return AuditExpectation{Status: 404}, nil
		}
		page, _ = strconv.Atoi(values[0])
	}
	query := parsed.Query()
	notesEnabled := auditDataFlag(query, "notes")
	originalEnabled := auditDataFlag(query, "original")
	decisionLocale := u.Locale
	rows := []store.IndexRow{row}
	if u.PageKind == "versions" {
		locale := u.Locale
		if originalEnabled {
			locale = "en-US"
			decisionLocale = "en-US"
		}
		entries, err := st.TimelineEntries(ctx, row.ID, locale)
		if err != nil {
			return expected, err
		}
		total := 0
		for _, entry := range entries {
			if !notesEnabled || entry.HasNotes {
				total++
			}
		}
		if page > max(1, (total+seo.VersionsPageSize-1)/seo.VersionsPageSize) {
			return AuditExpectation{Status: 404}, nil
		}
		if err := st.PopulatePageNotes(ctx, rows, max(1, page)); err != nil {
			return expected, err
		}
	}
	if u.PageKind == "alternatives" || u.PageKind == "uninstall" {
		if err := st.PopulateIntentEligibility(ctx, rows, policy, s.WebURL); err != nil {
			return expected, err
		}
	}
	if u.PageKind == "troubleshooting" {
		if err := st.PopulateTroubleshooting(ctx, rows, s.now()); err != nil {
			return expected, err
		}
		count := rows[0].TroubleshootingIssues[u.Locale]
		if count == 0 {
			count = rows[0].TroubleshootingIssues["en-US"]
		}
		if count < seo.TroubleshootingMinIssues {
			return AuditExpectation{Status: 404}, nil
		}
	}
	row = rows[0]
	ref := seo.PageRef{Kind: seo.PageKind(u.PageKind), Locale: decisionLocale, Page: page, Filtered: u.PageKind == "versions" && notesEnabled}
	decision := seo.DecidePage(row.Facts(), ref, policy)
	expected.Status = 200
	if decision.Reason == "unsupported_page" ||
		((u.PageKind == "alternatives" || u.PageKind == "uninstall") &&
			(decision.Robots != "index, follow" || decision.CanonicalPath != "")) {
		return AuditExpectation{Status: 404}, nil
	}
	// Fetch follows redirects: compare the final page with the current token,
	// not the historical URL retained in the registry.
	expected.Canonical = strings.TrimRight(s.WebURL, "/") + packagePagePath(row.Facts(), u.Locale, u.PageKind, parsed.Query())
	base := strings.TrimRight(s.WebURL, "/")
	if decision.CanonicalPath != "" {
		expected.Canonical = base + decision.CanonicalPath
	}
	expected.Robots = decision.Robots
	// app.vue applies the URL-level rule independently of the timeline filter.
	if u.PageKind == "versions" && auditQueryNoindex(query) {
		expected.Robots = "noindex, follow"
	}
	expected.Hreflang = map[string]string{}
	for _, locale := range decision.Hreflang {
		path := packagePagePath(row.Facts(), locale, u.PageKind, query)
		metadata := i18n.Metadata[i18n.LocaleCode(locale)]
		expected.Hreflang[metadata.Hreflang] = base + path
		for _, alias := range metadata.HreflangAliases {
			expected.Hreflang[alias] = base + path
		}
	}
	return expected, nil
}

// Match the page canonical's route/query contract, including localized version
// pagination. Tracking parameters and default query values are not canonical.
func packagePagePath(f seo.Facts, locale, kind string, query url.Values) string {
	path := seo.OverviewPath(f, locale)
	if kind != "overview" {
		path += "/" + kind
	}
	if kind == "versions" {
		params := []string{}
		if page, err := strconv.Atoi(query.Get("page")); len(query["page"]) == 1 && auditPageNumber.MatchString(query.Get("page")) && err == nil && page > 1 {
			params = append(params, "page="+strconv.Itoa(page))
		}
		for _, flag := range []string{"notes", "original"} {
			if len(query[flag]) == 1 && query.Get(flag) == "1" {
				params = append(params, flag+"=1")
			}
		}
		if len(params) > 0 {
			path += "?" + strings.Join(params, "&")
		}
	}
	return path
}

var auditPageNumber = regexp.MustCompile(`^[1-9][0-9]{0,5}$`)

type auditRoute struct {
	Token, Kind, Locale string
	Package, Valid      bool
}

var auditEscape = regexp.MustCompile(`%[0-9A-Fa-f]{2}|[^%]+`)
var auditToken = regexp.MustCompile(`^[a-z0-9][a-z0-9@._+-]{0,127}$`)

// Operate on EscapedPath, just like canonicalUrl.ts: lowercase literal text,
// preserve escapes, then decode the token exactly once and validate it.
// In particular, %4F is an uppercase O, not the lowercase literal o.
func parseAuditRoute(escapedPath string) auditRoute {
	path := auditEscape.ReplaceAllStringFunc(escapedPath, func(part string) string {
		if strings.HasPrefix(part, "%") {
			return part
		}
		return strings.ToLower(part)
	})
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	r := auditRoute{Kind: "other", Locale: "en-US"}
	if len(parts) < 2 || parts[0] != "" {
		return r
	}
	parts = parts[1:]
	if len(parts) > 0 && parts[0] == "en" {
		parts = parts[1:]
	} else if len(parts) > 0 {
		for locale, meta := range i18n.Metadata {
			if meta.Prefix != "" && meta.Prefix == "/"+parts[0] {
				r.Locale = string(locale)
				parts = parts[1:]
				break
			}
		}
	}
	if len(parts) > 0 {
		r.Kind = parts[0]
	}
	if len(parts) < 2 || (parts[0] != "apps" && parts[0] != "fonts") {
		return r
	}
	r.Package, r.Kind = true, "overview"
	if len(parts) > 2 {
		r.Kind = parts[2]
	}
	token, err := url.PathUnescape(parts[1])
	r.Token, r.Valid = token, err == nil && auditToken.MatchString(token) && len(parts) <= 3
	if strings.Contains(r.Kind, "%") {
		r.Valid = false
	}
	return r
}

// Vue Router returns repeated values as arrays; PackageVersions only enables a
// scalar "true" or "1". This is deliberately different from canonicalQuery and
// queryNoindex in the web's shared routeQuery contract.
func auditDataFlag(query url.Values, key string) bool {
	values := query[key]
	return len(values) == 1 && (values[0] == "true" || values[0] == "1")
}

func auditQueryNoindex(query url.Values) bool {
	return slices.Contains(query["notes"], "1") || slices.Contains(query["original"], "1")
}
