package management

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/service/seokeywords"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
)

func nonHistorySEOObject(object string) bool {
	return object == "background" || object == "keywords" || object == "update_feed"
}

func (s *Service) seoTaskTrace(ctx context.Context, id int64) (any, error) {
	task, err := (&seotasks.Service{DB: s.Store.DB}).Get(ctx, id)
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	trace, err := s.SEOTaskTrace(ctx, id)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(trace)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	entity := task.EntityKey
	kind, key, _ := strings.Cut(entity, ":")
	refKind := kind
	if refKind == "pkg" {
		refKind = "package"
	}
	var revision int64
	revisions := map[string]int64{}
	switch {
	case entity == "site:keywords" || kind == "background" || kind == "update_feed":
		if err := s.Store.DB.WithContext(ctx).Raw("SELECT coalesce((SELECT revision FROM seo_entity_revisions WHERE entity_key=?),0)", entity).Scan(&revision).Error; err != nil {
			return nil, err
		}
	case kind == "listing":
		revision, err = ListingAgentRevision(ctx, s.Store, key)
		if err != nil {
			return nil, err
		}
	case kind == "article" && !strings.HasPrefix(key, "new:"):
		if err := s.Store.DB.WithContext(ctx).Raw("SELECT coalesce(revision,0) FROM articles WHERE id=?", key).Scan(&revision).Error; err != nil {
			return nil, err
		}
	default:
		ref, e := store.HistoryRef(refKind, key)
		if e == nil {
			revision, err = actionRevision(ctx, s.Store, []content.Ref{ref})
			if err != nil {
				return nil, err
			}
			revisions[ref.Entity+":"+ref.Key()] = revision
		}
	}
	canonical, err := seotasks.CanonicalEntity(ctx, s.Store.DB, entity)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(canonical, "pkg:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(canonical, "pkg:"), 10, 64)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			Entity, ObjectKey string
			Revision          int64
		}
		if err := s.Store.DB.WithContext(ctx).Raw(`SELECT entity,object_key,max(id) revision FROM content_revisions WHERE (entity='package' AND object_key=?::text) OR (entity='release' AND object_key IN(SELECT id::text FROM releases WHERE package_id=?)) OR (entity='screenshot' AND object_key IN(SELECT id::text FROM package_screenshots WHERE package_id=?)) GROUP BY entity,object_key`, id, id, id).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			revisions[row.Entity+":"+row.ObjectKey] = row.Revision
		}
	}
	result["currentRevision"], result["revisions"] = revision, revisions
	return result, nil
}

func (s *Service) nonHistorySnapshot(ctx context.Context, object string, in Input) (any, error) {
	switch object {
	case "background":
		rows, err := s.seoRows(ctx, "SELECT to_jsonb(b)::text FROM background_item_content b WHERE label=? ORDER BY locale", in.Key)
		return map[string]any{"background_item_content": rows}, err
	case "keywords":
		names := []string{}
		entities := []string{}
		raw, _ := json.Marshal(in.Body)
		var request seokeywords.Request
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		for _, r := range request.Records {
			names = append(names, seokeywords.Normalize(r.Keyword))
		}
		for _, t := range request.Targets {
			entities = append(entities, t.EntityKey)
		}
		rows, err := s.seoRows(ctx, "SELECT to_jsonb(k)::text FROM seo_keywords k WHERE normalized IN ? ORDER BY id", names)
		if err != nil {
			return nil, err
		}
		targets, err := s.seoRows(ctx, "SELECT to_jsonb(t)::text FROM seo_targets t WHERE entity_key IN ? ORDER BY entity_key,intent,locale", entities)
		if err != nil {
			return nil, err
		}
		return map[string]any{"records": rows, "targets": targets}, nil
	default:
		return snapshotEntity(ctx, s.Store, object, in)
	}
}
