package management

import (
	"context"
	"strings"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
)

// Queue cache delivery with the business transaction and coalesce across
// languages. Package feed events remain responsible for edge/IndexNow delivery.
func queueAgentInvalidation(ctx context.Context, st *store.Store, entity string, refs []content.Ref) error {
	kinds := map[string]bool{}
	kind, _, _ := strings.Cut(entity, ":")
	kinds[kind] = true
	for _, ref := range refs {
		kinds[ref.Entity] = true
	}
	patterns := []string{}
	canonical, err := agentgate.CanonicalEntity(ctx, st.DB, entity)
	if err != nil {
		return err
	}
	if strings.HasPrefix(canonical, "pkg:") {
		var row struct{ Kind, Token string }
		if err := st.DB.WithContext(ctx).Raw("SELECT kind,token FROM packages WHERE id=?", strings.TrimPrefix(canonical, "pkg:")).Scan(&row).Error; err != nil {
			return err
		}
		if row.Token != "" {
			key := row.Kind + ":" + row.Token + ":*"
			patterns = append(patterns, cache.PackageDetailPrefix+key, "c:pkg:life:"+key, cache.ReleaseTimelinePrefix+key, "c:home:*", "c:rank:*", "c:sug:*", store.BrowseRefreshPattern, "c:related:*", "c:dep:*")
		}
		entity = canonical
	}
	for kind := range kinds {
		switch kind {
		case "category":
			patterns = append(patterns, "c:cat:*", "c:home:*", "c:pkg:*", "c:rank:*")
		case "collection", "collection_item":
			patterns = append(patterns, "c:col:*", "c:pkg:*", "c:home:*")
		case "feature":
			patterns = append(patterns, "c:home:*")
		case "announcement", "desktop_release":
			patterns = append(patterns, "c:cfg:*")
		case "synonym":
			patterns = append(patterns, "c:sug:*")
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	return store.QueueCacheInvalidation(ctx, st.DB, "seo-content:"+entity, patterns...)
}
