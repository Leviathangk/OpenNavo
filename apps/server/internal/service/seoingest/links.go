package seoingest

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"gorm.io/gorm"
)

// InternalLinks measures rendered anchors, ignoring external hosts and fragments.
// It neither fetches linked pages nor treats a sampled crawl as a complete graph.
func InternalLinks(page, html string) []string {
	base, err := url.Parse(page)
	if err != nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		value, _ := a.Attr("href")
		u, err := base.Parse(value)
		if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil {
			return
		}
		u.Fragment = ""
		u.RawQuery = u.Query().Encode()
		if u.String() == base.String() || len(u.String()) > 2048 {
			return
		}
		seen[u.String()] = true
	})
	links := make([]string, 0, len(seen))
	for value := range seen {
		links = append(links, value)
	}
	sort.Strings(links)
	return links
}

func (s *Service) recordLinks(ctx context.Context, page URL, layer string, doc Document, at time.Time) error {
	if doc.Status != 200 {
		return nil
	}
	links := InternalLinks(page.URL, doc.HTML)
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var id int64
		r := tx.Raw(`INSERT INTO seo_link_crawls(url_id,layer,checked_at) VALUES(?,?,?) ON CONFLICT(url_id,layer) DO UPDATE SET checked_at=EXCLUDED.checked_at WHERE seo_link_crawls.checked_at<=EXCLUDED.checked_at RETURNING url_id`, page.ID, layer, at).Scan(&id)
		if r.Error != nil || r.RowsAffected == 0 {
			return r.Error
		}
		if err := tx.Exec("DELETE FROM seo_internal_links WHERE from_url_id=? AND layer=?", page.ID, layer).Error; err != nil {
			return err
		}
		raw, err := json.Marshal(links)
		if err != nil {
			return err
		}
		return tx.Exec("INSERT INTO seo_internal_links(from_url_id,target_url,layer) SELECT ?,value,? FROM jsonb_array_elements_text(?::jsonb)", page.ID, layer, string(raw)).Error
	})
}
