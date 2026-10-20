package seoingest

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"gorm.io/gorm"
)

// AuditRules is also the frontend SSR test's rule registry; do not duplicate IDs there.
//
//go:embed audit-rules.json
var AuditRules []byte

type AuditExpectation struct {
	Status        int               `json:"status"`
	Canonical     string            `json:"canonical"`
	Robots        string            `json:"robots"`
	RequiredText  []string          `json:"requiredText"`
	Hreflang      map[string]string `json:"hreflang"`
	FactsSelector string            `json:"factsSelector"`
	FAQSelector   string            `json:"faqSelector"`
}
type Document struct {
	Status int
	Header http.Header
	HTML   string
}
type Issue struct{ Rule, Severity, Detail string }
type Auditor interface {
	Fetch(context.Context, URL, string) (Document, error)
}
type httpAuditor struct {
	client         *http.Client
	origin, public *url.URL
}

func (a *httpAuditor) Fetch(ctx context.Context, u URL, layer string) (Document, error) {
	target, err := url.Parse(u.URL)
	if err != nil || target.Host != a.public.Host || target.Scheme != a.public.Scheme {
		return Document{}, ErrUpstream
	}
	if layer == "origin" {
		target.Host = a.origin.Host
		target.Scheme = a.origin.Scheme
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Document{}, ErrUpstream
	}
	if layer == "origin" {
		req.Host = a.public.Host
	}
	req.Header.Set("User-Agent", "OpenNavo-SEO-Audit/1.0")
	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Document{}, ctx.Err()
		}
		return Document{}, ErrUpstream
	}
	defer func() { _ = resp.Body.Close() }()
	const limit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) > limit {
		return Document{}, ErrUpstream
	}
	return Document{Status: resp.StatusCode, Header: resp.Header, HTML: string(raw)}, nil
}

func normalizeRobots(raw string) string {
	parts := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ',' || r == ' ' })
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
func CheckDocument(u URL, d Document) ([]Issue, error) {
	var expected AuditExpectation
	if u.Expected != "" && json.Unmarshal([]byte(u.Expected), &expected) != nil {
		return nil, errors.New("invalid SEO audit expectation")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(d.HTML))
	if err != nil {
		return nil, err
	}
	issues := []Issue{}
	add := func(rule, severity, detail string) { issues = append(issues, Issue{rule, severity, detail}) }
	if d.Status >= 500 || (expected.Status != 0 && d.Status != expected.Status) {
		add("seo.status", "p0", "unexpected HTTP status")
	}
	canonical := doc.Find(`link[rel="canonical"]`)
	canonicalURL, _ := canonical.Attr("href")
	parsed, parseErr := url.Parse(canonicalURL)
	if d.Status == 200 && (canonical.Length() != 1 || parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || (expected.Canonical != "" && canonicalURL != expected.Canonical)) {
		add("seo.canonical", "p0", "canonical does not match page decision")
	}
	meta, _ := doc.Find(`meta[name="robots"]`).Attr("content")
	header := d.Header.Get("X-Robots-Tag")
	if (expected.Robots != "" && normalizeRobots(meta) != normalizeRobots(expected.Robots)) || (header != "" && normalizeRobots(header) != normalizeRobots(meta)) {
		add("seo.robots", "p0", "robots directives disagree")
	}
	ids := map[string]bool{}
	refs := []string{}
	invalidJSON := false
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			if id, ok := x["@id"].(string); ok {
				if len(x) > 1 {
					ids[id] = true
				} else {
					refs = append(refs, id)
				}
			}
			for _, item := range x {
				walk(item)
			}
		}
	}
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, sel *goquery.Selection) {
		var v any
		if json.Unmarshal([]byte(sel.Text()), &v) != nil {
			invalidJSON = true
			return
		}
		walk(v)
	})
	for _, ref := range refs {
		if (strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, u.URL+"#")) && !ids[ref] {
			invalidJSON = true
		}
	}
	if invalidJSON {
		add("seo.jsonld", "p0", "invalid JSON-LD or unresolved local @id")
	}
	visible := doc.Clone()
	visible.Find("script,style,template").Remove()
	for _, required := range expected.RequiredText {
		if !strings.Contains(visible.Text(), required) {
			add("seo.ssr_content", "p1", "required content absent from SSR")
			break
		}
	}
	for locale, target := range expected.Hreflang {
		found := false
		doc.Find(`link[rel="alternate"][hreflang]`).Each(func(_ int, sel *goquery.Selection) {
			l, _ := sel.Attr("hreflang")
			href, _ := sel.Attr("href")
			if l == locale && href == target {
				found = true
			}
		})
		if !found {
			add("seo.hreflang", "p1", "language alternate absent or incorrect")
			break
		}
	}
	badLink := false
	doc.Find("a[href]").Each(func(_ int, sel *goquery.Selection) {
		href, _ := sel.Attr("href")
		parsed, err := url.Parse(href)
		if err == nil && parsed.Query().Has("returnTo") {
			badLink = true
		}
	})
	if badLink {
		add("seo.return_to", "p1", "navigation leaks returnTo")
	}
	if expected.FactsSelector != "" && expected.FAQSelector != "" {
		facts, faq := -1, -1
		doc.Find("*").Each(func(i int, sel *goquery.Selection) {
			if sel.Is(expected.FactsSelector) && facts < 0 {
				facts = i
			}
			if sel.Is(expected.FAQSelector) && faq < 0 {
				faq = i
			}
		})
		if facts < 0 || (faq >= 0 && facts > faq) {
			add("seo.facts_order", "p1", "facts must precede FAQ")
		}
	}
	return issues, nil
}

func (s *Service) AuditPages(ctx context.Context) (map[string]any, error) {
	client, err := s.Audit(ctx)
	if errors.Is(err, ErrDisabled) {
		return map[string]any{"disabled": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.SyncRegistry(ctx); err != nil {
		return nil, err
	}
	var urls []URL
	// Rotate daily through the registry; recently changed URLs receive priority.
	if err := s.DB.WithContext(ctx).Raw(`SELECT * FROM seo_urls ORDER BY (updated_at>now()-interval '1 day') DESC,md5(id::text || ?),id LIMIT 100`, s.now().Format("2006-01-02T15")).Scan(&urls).Error; err != nil {
		return nil, err
	}
	for i, u := range urls {
		expected, err := s.ExpectedPage(ctx, u)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(expected)
		if err != nil {
			return nil, err
		}
		urls[i].Expected = string(raw)
		if err := s.DB.WithContext(ctx).Exec("UPDATE seo_urls SET expected=?::jsonb WHERE id=?", string(raw), u.ID).Error; err != nil {
			return nil, err
		}
	}
	count := 0
	for _, layer := range []string{"origin", "edge"} {
		ok := true
		for _, u := range urls {
			started := s.now()
			doc, fetchErr := client.Fetch(ctx, u, layer)
			var issues []Issue
			if fetchErr != nil {
				ok = false
				issues = []Issue{{"seo.fetch", "p1", "page fetch failed"}}
			} else {
				issues, err = CheckDocument(u, doc)
				if err != nil {
					return nil, err
				}
			}
			if err := s.recordAudit(ctx, u.ID, layer, issues, fetchErr == nil, started); err != nil {
				return nil, err
			}
			if fetchErr == nil {
				if err := s.recordLinks(ctx, u, layer, doc, started); err != nil {
					return nil, err
				}
			}
			count++
		}
		if ok && len(urls) > 0 {
			source := "audit"
			if layer == "edge" {
				source = "edge_probe"
			}
			if err := s.DB.WithContext(ctx).Exec(`UPDATE seo_data_watermarks SET fresh_through=GREATEST(fresh_through,?::date),updated_at=? WHERE source=?`, day(s.now()), s.now(), source).Error; err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{"checked": count, "coverage": "sample"}, nil
}

func (s *Service) recordAudit(ctx context.Context, id int64, layer string, issues []Issue, success bool, at time.Time) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := seobudget.Lock(ctx, tx, seobudget.Site(s.WebURL)); err != nil {
			return err
		}
		if err := tx.Exec("SELECT id FROM seo_urls WHERE id=? FOR UPDATE", id).Error; err != nil {
			return err
		}
		active := []string{}
		for _, issue := range issues {
			active = append(active, issue.Rule)
			if err := tx.Exec(`INSERT INTO seo_audit_issues(rule,url_id,layer,severity,detail,first_seen,last_seen) VALUES(?,?,?,?,?,?,?) ON CONFLICT(rule,url_id,layer) DO UPDATE SET severity=EXCLUDED.severity,detail=EXCLUDED.detail,last_seen=EXCLUDED.last_seen,resolved_at=NULL WHERE seo_audit_issues.last_seen<=EXCLUDED.last_seen AND (seo_audit_issues.resolved_at IS NULL OR seo_audit_issues.resolved_at<=EXCLUDED.last_seen)`, issue.Rule, id, layer, issue.Severity, issue.Detail, at, at).Error; err != nil {
				return err
			}
		}
		if !success {
			return seobudget.AssessTechnical(ctx, tx, seobudget.Site(s.WebURL))
		}
		q := tx.Table("seo_audit_issues").Where("url_id=? AND layer=? AND resolved_at IS NULL AND last_seen<=?", id, layer, at)
		if len(active) > 0 {
			q = q.Where("rule NOT IN ?", active)
		}
		if err := q.Update("resolved_at", at).Error; err != nil {
			return err
		}
		return seobudget.AssessTechnical(ctx, tx, seobudget.Site(s.WebURL))
	})
}
