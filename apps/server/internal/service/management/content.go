package management

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func parseTime(body map[string]any, key string) (*time.Time, error) {
	v := text(body, key)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, domain.Rejected("field_time_invalid:" + key)
	}
	return &t, nil
}
func (s *Service) contentRead(ctx context.Context, op string, in Input) (any, error) {
	if op == "GetAdminCollection" {
		raw, err := s.Store.AdminCollection(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return s.withWebURL(raw, "collection")
	}
	if op == "GetFeature" {
		return s.Store.AdminFeature(ctx, in.ID)
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	expr, from, order, where := store.AdminCollectionJSON, store.AdminCollectionFrom, "c.sort,c.id", "TRUE"
	args := []any{}
	if strings.Contains(op, "Feature") {
		expr, from, order = store.AdminFeatureJSON, store.AdminFeatureFrom, "f.sort,f.id"
		if v := text(in.Params, "placement"); v != "" {
			where += " AND f.placement=?"
			args = append(args, v)
		}
	}
	if v := text(in.Params, "status"); v != "" {
		prefix := "c"
		if strings.Contains(op, "Feature") {
			prefix = "f"
		}
		where += " AND " + prefix + ".status=?"
		args = append(args, v)
	}
	if q := text(in.Params, "q"); q != "" && !strings.Contains(op, "Feature") {
		where += ` AND (c.slug ILIKE ? ESCAPE '\' OR EXISTS(SELECT 1 FROM collection_i18n i WHERE i.collection_id=c.id AND i.title ILIKE ? ESCAPE '\'))`
		like := "%" + searchtext.EscapeLike(q) + "%"
		args = append(args, like, like)
	}
	result, err := s.Store.AdminPage(ctx, expr, from, where, order, args, current, size)
	if err != nil {
		return nil, err
	}
	if op == "ListAdminCollections" {
		for i, raw := range result.Records {
			result.Records[i], err = s.withWebURL(raw, "collection")
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
func (s *Service) contentWrite(ctx context.Context, op string, in Input) (any, error) {
	entity := "collection"
	table := "collections"
	if strings.Contains(op, "Feature") {
		entity = "feature"
		table = "features"
	}
	return s.write(ctx, in, op, entity, func(st *store.Store) (any, error) {
		in := in
		if op == "UpdateCollection" || op == "UpdateFeature" {
			local := *s
			local.Store = st
			var err error
			in, err = local.mergeContentUpdate(ctx, op, in)
			if err != nil {
				return nil, err
			}
		}
		id := in.ID
		if strings.HasPrefix(op, "Delete") {
			return nil, st.DB.WithContext(ctx).Exec("DELETE FROM "+table+" WHERE id=?", id).Error
		}
		if op == "SetCollectionItems" {
			items, ok := in.Body["items"].([]any)
			if !ok || len(items) > 100 {
				return nil, domain.Rejected("collection_items_invalid")
			}
			seen := map[int64]bool{}
			packageIDs := make([]int64, 0, len(items))
			for index, value := range items {
				row, ok := value.(map[string]any)
				if !ok {
					return nil, domain.Rejected("collection_item_invalid")
				}
				pid := int64(number(row, "packageId", 0))
				if pid < 1 || seen[pid] {
					return nil, domain.Rejected("collection_item_package_invalid")
				}
				seen[pid] = true
				packageIDs = append(packageIDs, pid)
				// Preserve identities of retained items to avoid cascade-deleting their translations and translation metadata.
				if err := st.DB.WithContext(ctx).Exec("INSERT INTO collection_items(collection_id,package_id,sort) VALUES(?,?,?) ON CONFLICT(collection_id,package_id) DO UPDATE SET sort=EXCLUDED.sort", id, pid, index).Error; err != nil {
					return nil, err
				}
				if err := st.WriteLocalized(ctx, "collection_item", id, pid, row, false); err != nil {
					return nil, err
				}
			}
			removed := st.DB.WithContext(ctx).Table("collection_items").Where("collection_id=?", id)
			if len(packageIDs) > 0 {
				removed = removed.Where("package_id NOT IN ?", packageIDs)
			}
			var removedItems []struct{ PackageID int64 }
			if err := removed.Session(&gorm.Session{}).Select("package_id").Find(&removedItems).Error; err != nil {
				return nil, err
			}
			for _, item := range removedItems {
				if err := st.ForgetContentTranslation(ctx, content.Ref{Entity: "collection_item", ID: id, SecondaryID: item.PackageID}); err != nil {
					return nil, err
				}
			}
			if err := removed.Delete(map[string]any{}).Error; err != nil {
				return nil, err
			}
			return nil, st.DB.WithContext(ctx).Exec("UPDATE collections SET updated_by=?,updated_at=? WHERE id=?", in.Actor.ID, s.now(), id).Error
		}
		if op == "PublishCollection" || op == "UnpublishCollection" {
			status := "archived"
			var at *time.Time
			if op == "PublishCollection" {
				var err error
				at, err = parseTime(in.Body, "publishAt")
				if err != nil {
					return nil, err
				}
				status = "published"
				if at != nil && at.After(s.now()) {
					status = "scheduled"
				}
				var count int64
				if err := st.DB.WithContext(ctx).Raw("SELECT count(*) FROM collection_items WHERE collection_id=?", id).Scan(&count).Error; err != nil {
					return nil, err
				}
				if count == 0 {
					return nil, fail(domain.CodeInvalidState)
				}
				var endRow struct{ UnpublishAt *time.Time }
				if err := st.DB.WithContext(ctx).Raw("SELECT unpublish_at FROM collections WHERE id=?", id).Scan(&endRow).Error; err != nil {
					return nil, err
				}
				start := s.now()
				if at != nil {
					start = *at
				}
				if endRow.UnpublishAt != nil && !endRow.UnpublishAt.After(start) {
					return nil, domain.Rejected("collection_unpublish_at_invalid")
				}
			}
			return nil, st.DB.WithContext(ctx).Exec("UPDATE collections SET status=?,publish_at=?,updated_by=?,updated_at=? WHERE id=?", status, at, in.Actor.ID, s.now(), id).Error
		}
		var fields map[string]any
		if entity == "collection" {
			fields = mapped(in.Body, map[string]string{"slug": "slug", "sort": "sort", "unpublishAt": "unpublish_at", "coverAssetId": "cover_asset_id"})
			if aid := number(in.Body, "coverAssetId", 0); aid > 0 {
				a, err := st.AssetByID(ctx, int64(aid))
				if err != nil {
					return nil, err
				}
				if a.Kind != "cover" {
					return nil, domain.Rejected("collection_cover_kind_invalid")
				}
			}
		} else {
			fields = mapped(in.Body, map[string]string{"placement": "placement", "categoryId": "category_id", "targetType": "target_type", "packageId": "package_id", "collectionId": "collection_id", "url": "url", "glowColor": "glow_color", "status": "status", "startsAt": "starts_at", "endsAt": "ends_at", "sort": "sort"})
			fields["package_id"], fields["collection_id"], fields["url"] = nil, nil, nil
			switch text(in.Body, "targetType") {
			case "package":
				fields["package_id"] = in.Body["packageId"]
			case "collection":
				fields["collection_id"] = in.Body["collectionId"]
			case "url":
				v := text(in.Body, "url")
				u, err := url.Parse(v)
				if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
					return nil, domain.Rejected("feature_url_invalid")
				}
				fields["url"] = v
			}
			start, err := parseTime(in.Body, "startsAt")
			if err != nil {
				return nil, err
			}
			end, err := parseTime(in.Body, "endsAt")
			if err != nil {
				return nil, err
			}
			if start != nil && end != nil && !end.After(*start) {
				return nil, domain.Rejected("feature_ends_at_invalid")
			}
		}
		fields["updated_by"], fields["updated_at"] = in.Actor.ID, s.now()
		if id == 0 {
			fields["created_by"] = in.Actor.ID
			if err := st.DB.WithContext(ctx).Table(table).Create(fields).Error; err != nil {
				return nil, err
			}
			if entity == "collection" {
				if err := st.DB.WithContext(ctx).Raw("SELECT id FROM collections WHERE slug=?", in.Body["slug"]).Scan(&id).Error; err != nil {
					return nil, err
				}
			} else {
				if err := st.DB.WithContext(ctx).Raw("SELECT currval(pg_get_serial_sequence('features','id'))").Scan(&id).Error; err != nil {
					return nil, err
				}
			}
		} else {
			if err := st.DB.WithContext(ctx).Table(table).Where("id=?", id).Updates(fields).Error; err != nil {
				return nil, err
			}
		}
		if entity == "feature" {
			if values, ok := in.Body["i18n"].(map[string]any); ok {
				for _, value := range values {
					if row, ok := value.(map[string]any); ok {
						plain := strings.ReplaceAll(text(row, "body"), "**", "")
						if strings.ContainsAny(plain, "<>`[]*_#") || strings.Contains(plain, "\n") {
							return nil, domain.Rejected("feature_body_markup_invalid")
						}
					}
				}
			}
		}
		if err := st.WriteLocalized(ctx, entity, id, 0, in.Body, true); err != nil {
			return nil, err
		}

		if in.ID == 0 {
			return map[string]any{"id": id}, nil
		}
		return nil, nil
	})
}

// Wrap in JSON so audit snapshots match actual API fields.
func contentSnapshot(ctx context.Context, st *store.Store, entity string, id int64) (any, error) {
	var raw json.RawMessage
	var err error
	if entity == "collection" {
		raw, err = st.AdminCollection(ctx, id)
	} else {
		raw, err = st.AdminFeature(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal(raw, &out)
	return out, err
}
