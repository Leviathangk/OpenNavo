package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
)

// PageLookup is how the site treats one of its URLs. App pages, articles and background items are decided
// by the server (seo.DecidePage and the article and background item rules the pages apply); every other
// page is decided by the web app (DecidedBy "web"), which keeps a language closed for indexing out of the
// index on every page. It reads the same facts the pages render from, never measurements.
type PageLookup struct {
	URL        string          `json:"url"`
	Path       string          `json:"path"`
	Locale     string          `json:"locale"`
	LocaleOpen bool            `json:"localeOpen"`
	Kind       string          `json:"kind"`
	Entity     *PageEntity     `json:"entity"`
	DecidedBy  string          `json:"decidedBy"`
	Indexable  *bool           `json:"indexable"`
	Robots     *string         `json:"robots"`
	Reason     *string         `json:"reason"`
	Canonical  *string         `json:"canonical"`
	Hreflang   []PageAlternate `json:"hreflang"`
	InSitemap  *bool           `json:"inSitemap"`
	Announce   *bool           `json:"announce"`
}

// PageEntity is what a page is about. ID is set for entities the server looked up.
type PageEntity struct {
	Type        string `json:"type"`
	ID          *int64 `json:"id"`
	Key         string `json:"key"`
	PackageKind string `json:"packageKind,omitempty"`
}

// PageAlternate is one language version a page lists in hreflang.
type PageAlternate struct {
	Locale string `json:"locale"`
	URL    string `json:"url"`
}

// Page kinds of LookupPage. App pages keep webRouteType's names; lists cover the browse pages.
const (
	PageKindHome           = "home"
	PageKindApp            = "app"
	PageKindArticle        = "article"
	PageKindArticleList    = "article_list"
	PageKindAuthor         = "author"
	PageKindCategory       = "category"
	PageKindCollection     = "collection"
	PageKindBackgroundItem = "background_item"
	PageKindSecurityTopic  = "security_topic"
	PageKindList           = "list"
	PageKindStatic         = "static"
	PageKindOther          = "other"
)

var appPageKinds = map[string]seo.PageKind{"": seo.PageOverview, "versions": seo.PageVersions, "details": seo.PageDetails,
	"dependencies": seo.PageDependencies, "troubleshooting": seo.PageTroubleshooting, "alternatives": seo.PageAlternatives, "uninstall": seo.PageUninstall}

var (
	listSections   = []string{"apps", "categories", "collections", "rankings", "discover", "browse", "background-items", "recent"}
	staticSections = []string{"download", "security", "about", "feedback", "brewfile", "search"}
)

// LookupPage parses a URL of the site (absolute, or a path) and returns its decision. The URL must be on the
// site's own origin.
func (s *PublicService) LookupPage(ctx context.Context, raw string) (PageLookup, error) {
	base, err := url.Parse(strings.TrimRight(s.WebBaseURL, "/"))
	if err != nil || base.Host == "" {
		return PageLookup{}, domain.Internal(errors.New("site URL is not configured"))
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return PageLookup{}, domain.Rejected("url_invalid:url")
	}
	if u.Host == "" && u.Scheme == "" && strings.HasPrefix(u.Path, "/") {
		u.Scheme, u.Host = base.Scheme, base.Host
	}
	if !strings.EqualFold(u.Host, base.Host) || u.Scheme != base.Scheme {
		return PageLookup{}, domain.Rejected("url_not_on_site:url")
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	locale, rest := splitLocale(path)
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return PageLookup{}, err
	}
	out := PageLookup{URL: base.Scheme + "://" + base.Host + u.EscapedPath(), Path: path, Locale: locale, LocaleOpen: policy.LocaleOpen(locale), Hreflang: []PageAlternate{}, DecidedBy: "web"}
	if u.RawQuery != "" {
		out.URL += "?" + u.RawQuery
	}
	segments := strings.Split(strings.Trim(rest, "/"), "/")
	if rest == "/" || rest == "" {
		segments = nil
	}
	query := u.Query()
	site := base.Scheme + "://" + base.Host
	switch {
	case len(segments) == 0:
		out.Kind = PageKindHome
	case segments[0] == "apps" && len(segments) >= 2 && len(segments) <= 3:
		tab := ""
		if len(segments) == 3 {
			tab = segments[2]
		}
		kind, ok := appPageKinds[tab]
		if !ok || strings.HasSuffix(segments[1], ".md") {
			out.Kind = PageKindOther
			break
		}
		return s.appPage(ctx, out, site, segments[1], kind, query, policy)
	case segments[0] == "articles" && len(segments) == 2 && segments[1] != "feed.xml":
		return s.articlePage(ctx, out, site, segments[1], policy)
	case segments[0] == "articles" && len(segments) == 1:
		out.Kind = PageKindArticleList
	case segments[0] == "background-items" && len(segments) == 2:
		return s.backgroundPage(ctx, out, site, segments[1], policy)
	case segments[0] == "authors" && len(segments) == 2:
		out.Kind, out.Entity = PageKindAuthor, &PageEntity{Type: "author", Key: segments[1]}
	case segments[0] == "categories" && len(segments) == 2:
		out.Kind, out.Entity = PageKindCategory, &PageEntity{Type: "category", Key: segments[1]}
	case segments[0] == "collections" && len(segments) == 2:
		out.Kind, out.Entity = PageKindCollection, &PageEntity{Type: "collection", Key: segments[1]}
	case segments[0] == "security" && len(segments) == 2:
		out.Kind, out.Entity = PageKindSecurityTopic, &PageEntity{Type: "security_topic", Key: segments[1]}
	case segments[0] == "browse" && (len(segments) <= 2 || len(segments) == 3 && segments[1] == "fonts"):
		out.Kind = PageKindList
	case len(segments) == 1 && slices.Contains(listSections, segments[0]):
		out.Kind = PageKindList
	case len(segments) == 1 && slices.Contains(staticSections, segments[0]):
		out.Kind, out.Entity = PageKindStatic, &PageEntity{Type: "static", Key: segments[0]}
	default:
		out.Kind = PageKindOther
	}
	// The web app decides these pages; a language closed for indexing is noindex on all of them.
	if !out.LocaleOpen {
		decided(&out, "web", seo.PageDecision{Robots: "noindex, follow", Reason: seo.ReasonLocaleClosed})
	}
	return out, nil
}

// splitLocale strips a site language prefix (the longest first, so /zh-hant is not /zh).
func splitLocale(path string) (string, string) {
	codes := append([]i18n.LocaleCode{}, i18n.SiteLocales[:]...)
	sort.SliceStable(codes, func(a, b int) bool { return len(i18n.Metadata[codes[a]].Prefix) > len(i18n.Metadata[codes[b]].Prefix) })
	for _, code := range codes {
		prefix := i18n.Metadata[code].Prefix
		if prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
			rest := strings.TrimPrefix(path, prefix)
			if rest == "" {
				rest = "/"
			}
			return string(code), rest
		}
	}
	return string(i18n.DefaultLocale), path
}

func localePath(locale, rest string) string {
	if rest == "/" {
		if prefix := i18n.Metadata[i18n.LocaleCode(locale)].Prefix; prefix != "" {
			return prefix
		}
		return "/"
	}
	return i18n.Metadata[i18n.LocaleCode(locale)].Prefix + rest
}

// decided fills the decision fields; canonical and hreflang come from the caller.
func decided(out *PageLookup, by string, d seo.PageDecision) {
	indexable := seo.DecisionRobots(string(d.Robots)).Index
	robots, inSitemap, announce := d.Robots, d.InSitemap, d.Announce
	out.DecidedBy, out.Indexable, out.Robots, out.InSitemap, out.Announce = by, &indexable, &robots, &inSitemap, &announce
	if d.Reason != "" {
		reason := d.Reason
		out.Reason = &reason
	}
}

func (s *PublicService) appPage(ctx context.Context, out PageLookup, site, token string, kind seo.PageKind, query url.Values, policy seo.Policy) (PageLookup, error) {
	out.Kind = PageKindApp
	if kind != seo.PageOverview {
		out.Kind = "app_" + string(kind)
	}
	row, found, err := s.Store.IndexRowByToken(ctx, token)
	if err != nil {
		return out, err
	}
	if !found {
		return s.missingApp(ctx, out, site, token, kind)
	}
	id := row.ID
	out.Entity = &PageEntity{Type: "package", ID: &id, Key: row.Token, PackageKind: row.Kind}
	ref := seo.PageRef{Kind: kind, Locale: out.Locale, Page: 1}
	tab := ""
	if kind != seo.PageOverview {
		tab = "/" + string(kind)
	}
	canonicalQuery := url.Values{}
	if kind == seo.PageVersions {
		if value := query.Get("page"); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 1 || number > 999999 || len(query["page"]) > 1 {
				decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "invalid_page"})
				return out, nil
			}
			ref.Page = number
			if number > 1 {
				canonicalQuery.Set("page", value)
			}
		}
		// Flags are on only as 1, like the web's query contract.
		for _, flag := range []string{"notes", "original"} {
			if slices.Contains(query[flag], "1") {
				ref.Filtered = true
				canonicalQuery.Set(flag, "1")
			}
		}
	}
	rows := []store.IndexRow{row}
	if kind == seo.PageVersions && !ref.Filtered {
		if err := s.Store.PopulatePageNotes(ctx, rows, ref.Page); err != nil {
			return out, err
		}
	}
	if kind == seo.PageAlternatives || kind == seo.PageUninstall {
		if err := s.Store.PopulateIntentEligibility(ctx, rows, policy, s.WebBaseURL); err != nil {
			return out, err
		}
	}
	d := seo.DecidePage(rows[0].Facts(), ref, policy)
	decided(&out, "server", d)
	pagePath := func(locale string) string {
		path := seo.OverviewPath(rows[0].Facts(), locale) + tab
		if encoded := canonicalQuery.Encode(); encoded != "" {
			path += "?" + encoded
		}
		return path
	}
	canonical := site + pagePath(out.Locale)
	if d.CanonicalPath != "" {
		canonical = site + d.CanonicalPath
	}
	out.Canonical = &canonical
	for _, locale := range d.Hreflang {
		out.Hreflang = append(out.Hreflang, PageAlternate{Locale: locale, URL: site + pagePath(locale)})
	}
	return out, nil
}

// missingApp explains a token no live app holds: a former token redirects, a removed app keeps an
// unindexed notice page, anything else is not found.
func (s *PublicService) missingApp(ctx context.Context, out PageLookup, site, token string, kind seo.PageKind) (PageLookup, error) {
	for _, packageKind := range []string{"cask", "link"} {
		row, found, err := s.Store.PackageLifecycle(ctx, packageKind, token)
		if err != nil {
			return out, err
		}
		if !found {
			continue
		}
		id := row.ID
		out.Entity = &PageEntity{Type: "package", ID: &id, Key: row.Token, PackageKind: packageKind}
		if row.RemovedAt != nil {
			decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "removed"})
			return out, nil
		}
		decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "redirect"})
		tab := ""
		if kind != seo.PageOverview {
			tab = "/" + string(kind)
		}
		canonical := site + seo.OverviewPath(seo.Facts{Token: row.Token}, out.Locale) + tab
		out.Canonical = &canonical
		return out, nil
	}
	decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "not_found"})
	return out, nil
}

// articlePage applies the article page's rule: indexed in the languages whose text is ready and that are
// open for indexing; other languages show the source text unindexed.
func (s *PublicService) articlePage(ctx context.Context, out PageLookup, site, slug string, policy seo.Policy) (PageLookup, error) {
	out.Kind = PageKindArticle
	out.Entity = &PageEntity{Type: "article", Key: slug}
	row, found, err := s.Store.PublicArticle(ctx, slug, out.Locale)
	if err != nil {
		return out, err
	}
	if !found {
		state, current, err := s.Store.ArticleState(ctx, slug)
		if err != nil {
			return out, err
		}
		switch state {
		case "moved":
			decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "redirect"})
			canonical := site + localePath(out.Locale, "/articles/"+current)
			out.Canonical = &canonical
		case "withdrawn":
			decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "withdrawn"})
		default:
			decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "not_found"})
		}
		return out, nil
	}
	id := row.ID
	out.Entity = &PageEntity{Type: "article", ID: &id, Key: row.Slug}
	var ready []string
	if err := json.Unmarshal([]byte(row.ReadyLocales), &ready); err != nil {
		return out, err
	}
	indexed := []string{}
	for _, code := range seo.Locales {
		if slices.Contains(ready, code) && policy.LocaleOpen(code) {
			indexed = append(indexed, code)
		}
	}
	d := seo.PageDecision{Robots: "noindex, follow", Hreflang: indexed}
	switch {
	case !policy.LocaleOpen(out.Locale):
		d.Reason = seo.ReasonLocaleClosed
	case !slices.Contains(indexed, out.Locale):
		d.Reason = "locale_not_ready"
	default:
		d.Robots, d.InSitemap, d.Announce = "index, follow", true, true
	}
	decided(&out, "server", d)
	canonical := site + localePath(out.Locale, "/articles/"+row.Slug)
	out.Canonical = &canonical
	for _, locale := range indexed {
		out.Hreflang = append(out.Hreflang, PageAlternate{Locale: locale, URL: site + localePath(locale, "/articles/"+row.Slug)})
	}
	return out, nil
}

// backgroundPage applies the background item page's rule: indexable with a verified description in this
// language, a catalog app still declaring the label and the language open; the page lists only itself.
func (s *PublicService) backgroundPage(ctx context.Context, out PageLookup, site, label string, policy seo.Policy) (PageLookup, error) {
	out.Kind = PageKindBackgroundItem
	out.Entity = &PageEntity{Type: "background_item", Key: label}
	if !facts.ValidBackgroundLabel(label) {
		decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "not_found"})
		return out, nil
	}
	item, err := s.backgroundItem(ctx, label, out.Locale, policy.OpenLocales)
	var appErr *domain.AppError
	if errors.As(err, &appErr) && appErr.Code == domain.CodeNotFound {
		decided(&out, "server", seo.PageDecision{Robots: "noindex, follow", Reason: "not_found"})
		return out, nil
	}
	if err != nil {
		return out, err
	}
	indexable, _ := item["indexable"].(bool)
	d := seo.PageDecision{Robots: "noindex, follow", Reason: "background_item_not_ready"}
	if indexable {
		d = seo.PageDecision{Robots: "index, follow", InSitemap: true, Reason: ""}
	}
	if !policy.LocaleOpen(out.Locale) {
		d.Reason = seo.ReasonLocaleClosed
	}
	decided(&out, "server", d)
	canonical := site + localePath(out.Locale, "/background-items/"+backgroundSegment(label))
	out.Canonical = &canonical
	if indexable {
		out.Hreflang = append(out.Hreflang, PageAlternate{Locale: out.Locale, URL: canonical})
	}
	return out, nil
}

// backgroundSegment encodes a label like the web's labelPath: encodeURIComponent, with @, + and : kept.
func backgroundSegment(label string) string {
	var out strings.Builder
	for _, b := range []byte(label) {
		switch {
		case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9', strings.IndexByte("-_.!~*'()@+:", b) >= 0:
			out.WriteByte(b)
		default:
			out.WriteString("%" + strings.ToUpper(strconv.FormatUint(uint64(b)|0x100, 16)[1:]))
		}
	}
	return out.String()
}
