package store

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
)

// QueueTranslationInvalidation complements ContentChanged in the batch writer's
// transaction. Use the same package identity prefixes as changelog writes; never
// evict unrelated detail, dependency or recommendation caches site-wide.
func (s *Store) QueueTranslationInvalidation(ctx context.Context, ref content.Ref) error {
	patterns := []string{}
	query := ""
	switch ref.Entity {
	case "article", "article_author", "collection", "collection_item":
		// ContentChanged / collection_url_invalidation already queue these paths.
		return nil
	case "package":
		query = "SELECT kind,token FROM packages WHERE id=?"
		patterns = append(patterns, "c:home:*", "c:rank:*", "c:sug:*", "c:browse:*")
	case "release":
		query = "SELECT p.kind,p.token FROM packages p JOIN releases r ON r.package_id=p.id WHERE r.id=?"
	case "screenshot":
		query = "SELECT p.kind,p.token FROM packages p JOIN package_screenshots r ON r.package_id=p.id WHERE r.id=?"
	case "category":
		query = "SELECT p.kind,p.token FROM packages p JOIN package_categories r ON r.package_id=p.id WHERE r.category_id=?"
		patterns = append(patterns, "c:cat:*", "c:home:*", "c:rank:*")
	case "feature":
		patterns = append(patterns, "c:home:*")
	case "desktop_release", "mirror", "announcement":
		patterns = append(patterns, "c:cfg:client:*")
	}
	if query != "" {
		var rows []struct{ Kind, Token string }
		if err := s.DB.WithContext(ctx).Raw(query, ref.ID).Scan(&rows).Error; err != nil {
			return err
		}
		for _, p := range rows {
			identity := cache.PatternLiteral(p.Kind) + ":" + cache.PatternLiteral(p.Token) + ":*"
			patterns = append(patterns, cache.PackageDetailPrefix+identity, "c:rel:*:"+identity, "c:dep:*:"+identity, "c:related:*:"+identity)
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	return QueueCacheInvalidation(ctx, s.DB, "translation:"+ref.Entity+":"+ref.Key(), patterns...)
}
