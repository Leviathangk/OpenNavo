package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// PublicArticleRow is a published article in one language: the requested language when its text
// is ready and not held back, otherwise the source language.
type PublicArticleRow struct {
	ID                                   int64
	Slug, Type, SourceLocale, TextLocale string
	Title                                string
	Summary, BodyMarkdown, TestNote      *string
	SeoTitle, SeoDescription             *string
	MachineTranslated                    bool
	CoverURL                             *string
	CoverWidth, CoverHeight              *int
	CoverAlt                             *string
	AuthorID                             *int64
	AuthorSlug, AuthorName               *string
	FirstPublishedAt                     time.Time
	ContentUpdatedAt                     *time.Time
	Revision                             int
	RevisionID                           int64
	ReadyLocales, Sources                string
}

// ReadyText lists the statuses whose text pages may show.
const readyText = `x.status IN ('source','machine','manual') AND NOT x.withheld AND btrim(x.title)<>''`

const publicArticleSelect = `SELECT a.id,a.slug,r.type,r.source_locale,t.locale AS text_locale,t.title,t.summary,t.body_markdown,t.test_note,t.seo_title,t.seo_description,
 t.machine_translated,ca.url AS cover_url,ca.width AS cover_width,ca.height AS cover_height,
 COALESCE((SELECT NULLIF(btrim(x.cover_alt),'') FROM article_revision_i18n x WHERE x.revision_id=r.id
 AND x.locale IN (lang.requested,'en-US') AND ` + readyText + ` AND NULLIF(btrim(x.cover_alt),'') IS NOT NULL
 ORDER BY (x.locale=lang.requested) DESC LIMIT 1),r.cover_alt) AS cover_alt,r.author_id,au.slug AS author_slug,au.name AS author_name,
 a.first_published_at,a.content_updated_at,r.number AS revision,r.id AS revision_id,r.sources::text AS sources,
 COALESCE((SELECT json_agg(x.locale ORDER BY x.locale) FROM article_revision_i18n x WHERE x.revision_id=r.id AND ` + readyText + `),'[]')::text AS ready_locales
 FROM articles a JOIN article_revisions r ON r.id=a.published_revision_id CROSS JOIN (SELECT ?::text requested) lang
 LEFT JOIN assets ca ON ca.id=r.cover_asset_id LEFT JOIN article_authors au ON au.id=r.author_id
 JOIN LATERAL (SELECT x.* FROM article_revision_i18n x WHERE x.revision_id=r.id AND ((x.locale=lang.requested AND ` + readyText + `) OR x.locale=r.source_locale)
   ORDER BY (x.locale=lang.requested) DESC LIMIT 1) t ON TRUE
 WHERE a.status='published'`

// PublicArticles lists published articles, newest first; authorID limits them to one author.
func (s *Store) PublicArticles(ctx context.Context, locale, kind string, authorID int64, current, size int) ([]PublicArticleRow, int64, error) {
	where, args := "", []any{locale}
	if kind != "" {
		where += " AND r.type=?"
		args = append(args, kind)
	}
	if authorID > 0 {
		where += " AND r.author_id=?"
		args = append(args, authorID)
	}
	var total int64
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*) FROM ("+publicArticleSelect+where+") n", args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PublicArticleRow
	err := s.DB.WithContext(ctx).Raw(publicArticleSelect+where+" ORDER BY a.first_published_at DESC,a.id DESC LIMIT ? OFFSET ?", append(args, size, (current-1)*size)...).Scan(&rows).Error
	return rows, total, err
}

// PublicArticle reads one published article by its current slug.
func (s *Store) PublicArticle(ctx context.Context, slug, locale string) (PublicArticleRow, bool, error) {
	var row PublicArticleRow
	err := s.DB.WithContext(ctx).Raw(publicArticleSelect+" AND a.slug=?", locale, slug).Scan(&row).Error
	return row, row.ID != 0, err
}

// ArticleState resolves a slug that is not a published article: a former slug or a withdrawn article.
func (s *Store) ArticleState(ctx context.Context, slug string) (state, current string, err error) {
	var row struct{ Slug, Status string }
	if err := s.DB.WithContext(ctx).Raw("SELECT slug,status FROM articles WHERE slug=?", slug).Scan(&row).Error; err != nil {
		return "", "", err
	}
	if row.Slug != "" {
		if row.Status == "archived" {
			return "withdrawn", row.Slug, nil
		}
		return "", "", nil
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT a.slug,a.status FROM article_slug_aliases s JOIN articles a ON a.id=s.article_id WHERE s.slug=?", slug).Scan(&row).Error; err != nil {
		return "", "", err
	}
	switch row.Status {
	case "published":
		return "moved", row.Slug, nil
	case "archived":
		return "withdrawn", row.Slug, nil
	}
	return "", "", nil
}

// ArticlePackageIDs lists the apps a published revision covers, in editor order.
func (s *Store) ArticlePackageIDs(ctx context.Context, revisionID int64) ([]int64, error) {
	var ids []int64
	err := s.DB.WithContext(ctx).Raw("SELECT package_id FROM article_revision_packages WHERE revision_id=? ORDER BY sort", revisionID).Scan(&ids).Error
	return ids, err
}

// PublicAuthorRow is an author's profile in one language.
type PublicAuthorRow struct {
	ID         int64
	Slug, Name string
	Role, Bio  *string
	AvatarURL  *string
	Links      string
	UpdatedAt  time.Time
}

// PublicAuthor reads an author with role and bio in the requested language when ready.
func (s *Store) PublicAuthor(ctx context.Context, slug, locale string) (PublicAuthorRow, bool, error) {
	var row PublicAuthorRow
	err := s.DB.WithContext(ctx).Raw(`SELECT a.id,a.slug,a.name,t.role,t.bio,av.url AS avatar_url,a.links::text AS links,a.updated_at FROM article_authors a
 LEFT JOIN assets av ON av.id=a.avatar_asset_id
 LEFT JOIN LATERAL (SELECT x.* FROM article_author_i18n x WHERE x.author_id=a.id AND ((x.locale=? AND x.status IN ('source','machine','manual')) OR x.locale=a.source_locale)
   ORDER BY (x.locale=?) DESC LIMIT 1) t ON TRUE WHERE a.slug=?`, locale, locale, slug).Scan(&row).Error
	return row, row.ID != 0, err
}

// PublicAuthorByID reads an author's profile for an article byline.
func (s *Store) PublicAuthorByID(ctx context.Context, id int64, locale string) (PublicAuthorRow, error) {
	var slug string
	if err := s.DB.WithContext(ctx).Raw("SELECT slug FROM article_authors WHERE id=?", id).Scan(&slug).Error; err != nil {
		return PublicAuthorRow{}, err
	}
	row, _, err := s.PublicAuthor(ctx, slug, locale)
	return row, err
}

// PackageArticleIDs lists published articles covering a package, newest first.
func (s *Store) PackageArticleSlugs(ctx context.Context, packageID int64, limit int) ([]string, error) {
	var slugs []string
	err := s.DB.WithContext(ctx).Raw(`SELECT a.slug FROM articles a JOIN article_revision_packages p ON p.revision_id=a.published_revision_id
 WHERE a.status='published' AND p.package_id=? ORDER BY a.first_published_at DESC,a.id DESC LIMIT ?`, packageID, limit).Scan(&slugs).Error
	return slugs, err
}

// RelatedArticleSlugs picks other published articles for an article page: the most shared apps
// first, then the same type, then the newest.
func (s *Store) RelatedArticleSlugs(ctx context.Context, articleID, revisionID int64, kind string, limit int) ([]string, error) {
	var slugs []string
	err := s.DB.WithContext(ctx).Raw(`SELECT a.slug FROM articles a JOIN article_revisions r ON r.id=a.published_revision_id
 WHERE a.status='published' AND a.id<>?
 ORDER BY (SELECT count(*) FROM article_revision_packages p WHERE p.revision_id=r.id
   AND p.package_id IN (SELECT package_id FROM article_revision_packages WHERE revision_id=?)) DESC,
 (r.type=?) DESC,a.first_published_at DESC,a.id DESC LIMIT ?`, articleID, revisionID, kind, limit).Scan(&slugs).Error
	return slugs, err
}

// ArticleSitemapRow is one published article with per-language lastmod.
type ArticleSitemapRow struct {
	Slug    string
	Locales map[string]time.Time
}

// ArticleSitemap lists published articles and the ready languages of each, plus authors with
// published articles.
func (s *Store) ArticleSitemap(ctx context.Context) ([]ArticleSitemapRow, []AuthorSitemapRow, error) {
	var rows []struct {
		Slug    string
		Locales string
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT a.slug,COALESCE((SELECT json_object_agg(x.locale,GREATEST(COALESCE(a.content_updated_at,a.first_published_at),COALESCE(x.translated_at,a.first_published_at)))
 FROM article_revision_i18n x WHERE x.revision_id=a.published_revision_id AND ` + readyText + `),'{}')::text AS locales
 FROM articles a WHERE a.status='published' ORDER BY a.first_published_at DESC,a.id DESC`).Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	articles := make([]ArticleSitemapRow, 0, len(rows))
	for _, row := range rows {
		item := ArticleSitemapRow{Slug: row.Slug, Locales: map[string]time.Time{}}
		if err := json.Unmarshal([]byte(row.Locales), &item.Locales); err != nil {
			return nil, nil, fmt.Errorf("decode article sitemap locales: %w", err)
		}
		articles = append(articles, item)
	}
	var authors []AuthorSitemapRow
	err = s.DB.WithContext(ctx).Raw(`SELECT au.slug,GREATEST(au.updated_at,max(COALESCE(a.content_updated_at,a.first_published_at))) AS lastmod FROM article_authors au
 JOIN article_revisions r ON r.author_id=au.id JOIN articles a ON a.published_revision_id=r.id AND a.status='published' GROUP BY au.id,au.slug,au.updated_at ORDER BY au.slug`).Scan(&authors).Error
	return articles, authors, err
}

// AuthorSitemapRow is an author with published articles.
type AuthorSitemapRow struct {
	Slug    string
	Lastmod *time.Time
}

// VisiblePackageIDs keeps, in order, the IDs of apps pages may show.
func (s *Store) VisiblePackageIDs(ctx context.Context, ids []int64) ([]int64, error) {
	out := []int64{}
	if len(ids) == 0 {
		return out, nil
	}
	var found []int64
	if err := s.DB.WithContext(ctx).Raw("SELECT p.id FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id IN ? AND "+publicVisible, ids).Scan(&found).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		if slices.Contains(found, id) {
			out = append(out, id)
		}
	}
	return out, nil
}
