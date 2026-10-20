package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/articles"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// Public article reads (WP14/WP17) only ever see the published revision of a published article.
// Cache keys start with c:art: so translations becoming ready refresh them without a full flush.

func articleCard(row store.PublicArticleRow) publicapi.ArticleCard {
	card := publicapi.ArticleCard{Slug: row.Slug, Type: publicapi.ArticleType(row.Type), Title: row.Title, Summary: row.Summary,
		PublishedAt: row.FirstPublishedAt.UTC(), UpdatedAt: row.FirstPublishedAt.UTC(), ReadingMinutes: articles.ReadingMinutes(derefText(row.BodyMarkdown)),
		Locale: publicapi.Locale(row.TextLocale), MachineTranslated: row.MachineTranslated}
	if row.ContentUpdatedAt != nil {
		card.UpdatedAt = row.ContentUpdatedAt.UTC()
	}
	if row.CoverURL != nil && row.CoverAlt != nil {
		card.Cover = &publicapi.ArticleCover{Url: *row.CoverURL, Width: row.CoverWidth, Height: row.CoverHeight, Alt: *row.CoverAlt}
	}
	if row.AuthorSlug != nil && row.AuthorName != nil {
		card.Author = &publicapi.ArticleAuthorRef{Slug: *row.AuthorSlug, Name: *row.AuthorName}
	}
	return card
}

func derefText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func articlePage(rows []store.PublicArticleRow, total int64, current, size int) publicapi.ArticlePage {
	page := publicapi.ArticlePage{Current: current, Size: size, Total: int(total), Records: []publicapi.ArticleCard{}}
	for _, row := range rows {
		page.Records = append(page.Records, articleCard(row))
	}
	return page
}

// Articles lists published articles in a language.
func (s *PublicService) Articles(ctx context.Context, locale, kind string, current, size int) (publicapi.ArticlePage, error) {
	if !ValidPage(current, size, 50) || kind != "" && !articles.ValidType(kind) {
		return publicapi.ArticlePage{}, domain.Validation()
	}
	key := fmt.Sprintf("c:art:list:%s:%s:%d:%d", locale, kind, current, size)
	return cache.ReadThrough(ctx, s.Cache, key, 10*time.Minute, func(ctx context.Context) (publicapi.ArticlePage, error) {
		rows, total, err := s.Store.PublicArticles(ctx, locale, kind, 0, current, size)
		return articlePage(rows, total, current, size), err
	})
}

func authorView(row store.PublicAuthorRow) (publicapi.ArticleAuthor, error) {
	author := publicapi.ArticleAuthor{Slug: row.Slug, Name: row.Name, Role: row.Role, Bio: row.Bio, AvatarUrl: row.AvatarURL, Links: []publicapi.ArticleSource{}}
	if row.Links != "" {
		if err := json.Unmarshal([]byte(row.Links), &author.Links); err != nil {
			return author, fmt.Errorf("decode author links: %w", err)
		}
	}
	return author, nil
}

// Article resolves a slug: the published article, a redirect for a former slug, or withdrawn.
func (s *PublicService) Article(ctx context.Context, slug, locale string) (publicapi.ArticleLookup, error) {
	return cache.ReadThrough(ctx, s.Cache, "c:art:item:"+slug+":"+locale, 10*time.Minute, func(ctx context.Context) (publicapi.ArticleLookup, error) {
		row, found, err := s.Store.PublicArticle(ctx, slug, locale)
		if err != nil {
			return publicapi.ArticleLookup{}, err
		}
		if !found {
			state, current, err := s.Store.ArticleState(ctx, slug)
			if err != nil {
				return publicapi.ArticleLookup{}, err
			}
			if state == "" {
				return publicapi.ArticleLookup{}, notFound(gorm.ErrRecordNotFound)
			}
			return publicapi.ArticleLookup{State: publicapi.ArticleLookupState(state), Slug: current}, nil
		}
		article, err := s.articleDetail(ctx, row, locale)
		if err != nil {
			return publicapi.ArticleLookup{}, err
		}
		return publicapi.ArticleLookup{State: publicapi.Published, Slug: row.Slug, Article: &article}, nil
	})
}

func (s *PublicService) articleDetail(ctx context.Context, row store.PublicArticleRow, locale string) (publicapi.Article, error) {
	card := articleCard(row)
	body := derefText(row.BodyMarkdown)
	out := publicapi.Article{Slug: row.Slug, Type: card.Type, Title: row.Title, Summary: row.Summary, BodyMarkdown: row.BodyMarkdown, TestNote: row.TestNote,
		SeoTitle: row.SeoTitle, SeoDescription: row.SeoDescription, Locale: card.Locale, SourceLocale: publicapi.Locale(row.SourceLocale),
		MachineTranslated: row.MachineTranslated, Cover: card.Cover, PublishedAt: card.PublishedAt, UpdatedAt: card.UpdatedAt,
		ReadingMinutes: card.ReadingMinutes, Revision: row.Revision, AvailableLocales: []publicapi.Locale{}, Sources: []publicapi.ArticleSource{},
		Packages: []publicapi.PackageSummary{}, Images: []publicapi.ArticleImage{}, Related: []publicapi.ArticleCard{}}
	var ready []string
	if err := json.Unmarshal([]byte(row.ReadyLocales), &ready); err != nil {
		return out, fmt.Errorf("decode article locales: %w", err)
	}
	for _, code := range ready {
		out.AvailableLocales = append(out.AvailableLocales, publicapi.Locale(code))
	}
	if err := json.Unmarshal([]byte(row.Sources), &out.Sources); err != nil {
		return out, fmt.Errorf("decode article sources: %w", err)
	}
	if row.AuthorID != nil {
		author, err := s.Store.PublicAuthorByID(ctx, *row.AuthorID, locale)
		if err != nil {
			return out, err
		}
		view, err := authorView(author)
		if err != nil {
			return out, err
		}
		out.Author = &view
	}
	scan := articles.ScanBody(body)
	ids, err := s.Store.ArticlePackageIDs(ctx, row.RevisionID)
	if err != nil {
		return out, err
	}
	// App cards in the body need their apps too, after the related ones.
	cards, err := s.Store.LiveCaskIDs(ctx, scan.AppCards)
	if err != nil {
		return out, err
	}
	for _, token := range scan.AppCards {
		if id, ok := cards[token]; ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	visible, err := s.Store.VisiblePackageIDs(ctx, ids)
	if err != nil {
		return out, err
	}
	summaries, err := s.SummariesByID(ctx, visible, locale)
	if err != nil {
		return out, err
	}
	for _, id := range visible {
		if item, ok := summaries[id]; ok {
			out.Packages = append(out.Packages, item)
		}
	}
	urls := []string{}
	for _, image := range scan.Images {
		if !slices.Contains(urls, image.URL) {
			urls = append(urls, image.URL)
		}
	}
	assets, err := s.Store.ImageAssets(ctx, urls)
	if err != nil {
		return out, err
	}
	for _, u := range urls {
		if asset, ok := assets[u]; ok {
			out.Images = append(out.Images, publicapi.ArticleImage{Url: asset.URL, Width: asset.Width, Height: asset.Height})
		}
	}
	related, err := s.Store.RelatedArticleSlugs(ctx, row.ID, row.RevisionID, row.Type, 3)
	if err != nil {
		return out, err
	}
	out.Related, err = s.articleCards(ctx, related, locale)
	return out, err
}

// articleCards reads published articles as cards, in the given order.
func (s *PublicService) articleCards(ctx context.Context, slugs []string, locale string) ([]publicapi.ArticleCard, error) {
	out := []publicapi.ArticleCard{}
	for _, slug := range slugs {
		row, found, err := s.Store.PublicArticle(ctx, slug, locale)
		if err != nil {
			return out, err
		}
		if found {
			out = append(out, articleCard(row))
		}
	}
	return out, nil
}

// Author returns an author's profile and published articles.
func (s *PublicService) Author(ctx context.Context, slug, locale string, current, size int) (publicapi.AuthorPage, error) {
	if !ValidPage(current, size, 50) {
		return publicapi.AuthorPage{}, domain.Validation()
	}
	key := fmt.Sprintf("c:art:author:%s:%s:%d:%d", slug, locale, current, size)
	return cache.ReadThrough(ctx, s.Cache, key, 10*time.Minute, func(ctx context.Context) (publicapi.AuthorPage, error) {
		row, found, err := s.Store.PublicAuthor(ctx, slug, locale)
		if err != nil {
			return publicapi.AuthorPage{}, err
		}
		if !found {
			return publicapi.AuthorPage{}, notFound(gorm.ErrRecordNotFound)
		}
		author, err := authorView(row)
		if err != nil {
			return publicapi.AuthorPage{}, err
		}
		rows, total, err := s.Store.PublicArticles(ctx, locale, "", row.ID, current, size)
		return publicapi.AuthorPage{Author: author, Articles: articlePage(rows, total, current, size)}, err
	})
}

// ArticleSitemap lists article and author URLs with ready languages.
func (s *PublicService) ArticleSitemap(ctx context.Context) (publicapi.ArticleSitemap, error) {
	return cache.ReadThrough(ctx, s.Cache, "c:art:sitemap", 10*time.Minute, func(ctx context.Context) (publicapi.ArticleSitemap, error) {
		rows, authors, err := s.Store.ArticleSitemap(ctx)
		out := publicapi.ArticleSitemap{Articles: []publicapi.ArticleSitemapEntry{}, Authors: []publicapi.AuthorSitemapEntry{}}
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			entry := publicapi.ArticleSitemapEntry{Slug: row.Slug, Locales: []publicapi.SitemapLocale{}}
			for _, code := range i18n.SiteLocales {
				locale := string(code)
				if at, ok := row.Locales[locale]; ok {
					stamp := at.UTC()
					entry.Locales = append(entry.Locales, publicapi.SitemapLocale{Locale: publicapi.Locale(locale), Lastmod: &stamp})
				}
			}
			out.Articles = append(out.Articles, entry)
		}
		for _, author := range authors {
			out.Authors = append(out.Authors, publicapi.AuthorSitemapEntry{Slug: author.Slug, Lastmod: author.Lastmod})
		}
		return out, nil
	})
}

// packageArticles lists up to three published articles covering an app.
func (s *PublicService) packageArticles(ctx context.Context, packageID int64, locale string) ([]publicapi.ArticleCard, error) {
	slugs, err := s.Store.PackageArticleSlugs(ctx, packageID, 3)
	if err != nil {
		return []publicapi.ArticleCard{}, err
	}
	return s.articleCards(ctx, slugs, locale)
}
