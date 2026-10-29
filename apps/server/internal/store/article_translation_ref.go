package store

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
)

// ResolveArticleTranslationRef validates external translation references without
// reading any revision text. lock must only be used inside a write transaction,
// before taking the content lock, matching the article publication lock order.
func (s *Store) ResolveArticleTranslationRef(ctx context.Context, ref content.Ref, articleID *int64, lock bool) (content.Ref, error) {
	if ref.Entity != "article" {
		if articleID != nil {
			return ref, domain.Rejected("article_id_requires_article")
		}
		return ref, nil
	}
	if articleID != nil {
		if *articleID <= 0 {
			return ref, domain.Rejected("field_invalid:articleId")
		}
		article, found, err := s.LoadArticle(ctx, *articleID, lock)
		if err != nil {
			return ref, err
		}
		if !found {
			return ref, domain.Rejected("article_not_found")
		}
		if article.Status != "published" || article.PublishedRevisionID == nil {
			return ref, domain.Rejected("article_must_be_published_first")
		}
		if ref.ID != 0 && ref.ID != *article.PublishedRevisionID {
			return ref, domain.Rejected("article_ref_mismatch:articleId_and_ref.id")
		}
		ref.ID = *article.PublishedRevisionID
		return ref, nil
	}

	// Article and revision IDs use independent sequences. Reject ambiguous IDs
	// rather than silently addressing another article's published revision.
	article, found, err := s.LoadArticle(ctx, ref.ID, false)
	if err != nil {
		return ref, err
	}
	if found && (article.Status != "published" || article.PublishedRevisionID == nil) {
		return ref, domain.Rejected("article_ref_must_be_published_revision:article_id_probably_passed_publish_article_first")
	}
	if found && *article.PublishedRevisionID != ref.ID {
		return ref, domain.Rejected("article_ref_must_be_published_revision:article_id_probably_passed_use_articleId_or_article_get.published.id")
	}
	query := "SELECT id FROM articles WHERE published_revision_id=? AND status='published'"
	if lock {
		query += " FOR UPDATE"
	}
	var ownerID int64
	if err := s.DB.WithContext(ctx).Raw(query, ref.ID).Scan(&ownerID).Error; err != nil {
		return ref, err
	}
	if ownerID == 0 {
		var status string
		if err := s.DB.WithContext(ctx).Raw("SELECT a.status FROM articles a JOIN article_revisions r ON r.article_id=a.id WHERE r.id=?", ref.ID).Scan(&status).Error; err != nil {
			return ref, err
		}
		if status != "" && status != "published" {
			return ref, domain.Rejected("article_ref_must_be_published_revision:publish_article_first")
		}
		return ref, domain.Rejected("article_ref_must_be_published_revision:use_articleId_or_article_get.published.id")
	}
	return ref, nil
}
