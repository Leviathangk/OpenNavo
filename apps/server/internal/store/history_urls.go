package store

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
)

func (s *Store) HistoryURLs(ctx context.Context, ref content.Ref, base string, states ...HistoryState) ([]string, error) {
	out := []string{}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return out, nil
	}
	add := func(path string) {
		if !slices.Contains(out, base+path) {
			out = append(out, base+path)
		}
	}
	switch ref.Entity {
	case "package", "release", "screenshot":
		id := ref.ID
		if ref.Entity != "package" {
			parent := content.Registry[ref.Entity].Parent
			for _, state := range states {
				for _, row := range state[parent] {
					if n, ok := row["package_id"].(float64); ok {
						id = int64(n)
					}
				}
			}
		}
		var token string
		if err := s.DB.WithContext(ctx).Raw("SELECT token FROM packages WHERE id=? AND kind IN ('cask','link')", id).Scan(&token).Error; err != nil {
			return nil, err
		}
		if token != "" {
			add("/apps/" + url.PathEscape(token))
		}
	case "collection":
		for _, state := range states {
			for _, row := range state["collections"] {
				if slug, ok := row["slug"].(string); ok {
					add("/collections/" + url.PathEscape(slug))
				}
			}
		}
	case "collection_item":
		var slug string
		if err := s.DB.WithContext(ctx).Raw("SELECT slug FROM collections WHERE id=?", ref.ID).Scan(&slug).Error; err != nil {
			return nil, err
		}
		if slug != "" {
			add("/collections/" + url.PathEscape(slug))
		}
	case "feature":
		add("/")
	case "article":
		var slug string
		if err := s.DB.WithContext(ctx).Raw("SELECT a.slug FROM article_revisions r JOIN articles a ON a.id=r.article_id WHERE r.id=? AND a.status='published'", ref.ID).Scan(&slug).Error; err != nil {
			return nil, err
		}
		if slug != "" {
			add("/articles/" + url.PathEscape(slug))
		}
	case "article_author":
		var slug string
		if err := s.DB.WithContext(ctx).Raw("SELECT slug FROM article_authors WHERE id=?", ref.ID).Scan(&slug).Error; err != nil {
			return nil, err
		}
		if slug != "" {
			add("/authors/" + url.PathEscape(slug))
		}
	}
	return out, nil
}
