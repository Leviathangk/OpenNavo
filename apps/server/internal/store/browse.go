package store

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/seo"
	"golang.org/x/text/unicode/norm"
)

const BrowsePageSize = 300

func browseFold(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, strings.ToLower(norm.NFKD.String(name)))
}
func BrowseBucket(displayName string) string {
	for _, r := range browseFold(displayName) {
		switch {
		case r >= 'a' && r <= 'z':
			return string(r)
		case r >= '0' && r <= '9':
			return "0-9"
		case unicode.IsLetter(r):
			return "other"
		}
	}
	return "other"
}

type BrowseDirectory struct {
	Buckets []publicapi.BrowseBucket          `json:"buckets"`
	Groups  map[string][]publicapi.BrowseItem `json:"groups"`
}

func (s *Store) Browse(ctx context.Context, kind, bucket, locale string, page int, policy seo.Policy) (publicapi.BrowsePage, error) {
	directory, err := s.BrowseDirectory(ctx, kind, locale, policy)
	if err != nil {
		return publicapi.BrowsePage{}, err
	}
	return directory.Page(bucket, page)
}

// Cache this compact directory once per kind/locale, rather than rebuilding the
// full catalog for every independently crawled letter and page.
func (s *Store) BrowseDirectory(ctx context.Context, kind, locale string, policy seo.Policy) (BrowseDirectory, error) {
	out := BrowseDirectory{Buckets: []publicapi.BrowseBucket{}, Groups: map[string][]publicapi.BrowseItem{}}
	condition := "NOT COALESCE(m.hidden,false) AND NOT p.is_font"
	if kind == "fonts" {
		condition = "NOT COALESCE(m.hidden,false) AND p.is_font"
	}
	rows, err := s.indexRowsQuery(ctx, indexOverviewSelect, condition, "ORDER BY p.id")
	if err != nil {
		return out, err
	}
	var names []struct {
		ID          int64
		DisplayName string
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT p.id,COALESCE(NULLIF(i.display_name,''),p.name) AS display_name FROM packages p
 LEFT JOIN LATERAL(SELECT t.display_name FROM package_i18n t CROSS JOIN(SELECT ?::text requested) lang WHERE t.package_id=p.id AND NULLIF(t.display_name,'') IS NOT NULL AND t.locale IN(lang.requested,p.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1)i ON true WHERE p.kind IN('cask','link') AND p.removed_at IS NULL`, locale).Scan(&names).Error; err != nil {
		return out, err
	}
	byID := map[int64]string{}
	for _, v := range names {
		byID[v.ID] = v.DisplayName
	}
	groups := out.Groups
	for _, row := range rows {
		if !seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageOverview, Locale: locale, Page: 1}, policy).InSitemap {
			continue
		}
		name := byID[row.ID]
		key := BrowseBucket(name)
		groups[key] = append(groups[key], publicapi.BrowseItem{Token: row.Token, DisplayName: name})
	}
	keys := []string{"0-9"}
	for r := 'a'; r <= 'z'; r++ {
		keys = append(keys, string(r))
	}
	keys = append(keys, "other")
	for _, key := range keys {
		count := len(groups[key])
		if count > 0 {
			out.Buckets = append(out.Buckets, publicapi.BrowseBucket{Key: key, Count: count, Pages: (count + BrowsePageSize - 1) / BrowsePageSize})
		}
	}
	for _, items := range groups {
		slices.SortFunc(items, func(a, b publicapi.BrowseItem) int {
			if n := strings.Compare(browseFold(a.DisplayName), browseFold(b.DisplayName)); n != 0 {
				return n
			}
			return strings.Compare(a.Token, b.Token)
		})
	}
	return out, nil
}

func (directory BrowseDirectory) Page(bucket string, page int) (publicapi.BrowsePage, error) {
	out := publicapi.BrowsePage{Buckets: directory.Buckets, Items: []publicapi.BrowseItem{}}
	if bucket == "" {
		return out, nil
	}
	items := directory.Groups[bucket]
	if page < 1 || page > (len(items)+BrowsePageSize-1)/BrowsePageSize {
		return out, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	start := (page - 1) * BrowsePageSize
	out.Items = items[start:min(len(items), start+BrowsePageSize)]
	return out, nil
}

func (s *Store) PackageCollections(ctx context.Context, id int64, locale string) ([]publicapi.PackageCollectionRef, error) {
	rows := []publicapi.PackageCollectionRef{}
	err := s.DB.WithContext(ctx).Raw(`SELECT c.slug,COALESCE(NULLIF(i.title,''),c.slug) AS title FROM collections c JOIN collection_items ci ON ci.collection_id=c.id
 LEFT JOIN LATERAL(SELECT t.title FROM collection_i18n t CROSS JOIN(SELECT ?::text requested) lang WHERE t.collection_id=c.id AND NULLIF(t.title,'') IS NOT NULL AND t.locale IN(lang.requested,c.source_locale,'en-US') ORDER BY `+publicLocaleOrder+` LIMIT 1)i ON true
 WHERE ci.package_id=? AND c.status IN ('scheduled','published') AND (c.publish_at IS NULL OR c.publish_at<=now()) AND (c.unpublish_at IS NULL OR c.unpublish_at>now()) ORDER BY c.sort,c.slug`, locale, id).Scan(&rows).Error
	return rows, err
}
