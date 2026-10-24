package management

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) categories(ctx context.Context, _ Input) (any, error) {
	rows, err := s.Store.JSONRows(ctx, `SELECT jsonb_build_object('id',c.id,'sourceLocale',c.source_locale,'parentId',c.parent_id,'slug',c.slug,'icon',c.icon,'sort',c.sort,'appliesTo',c.applies_to,'visible',c.visible,'hiddenByDefault',c.hidden_by_default,'packageCount',(SELECT count(*) FROM package_categories pc WHERE pc.category_id=c.id),'i18n',COALESCE((SELECT jsonb_object_agg(ci.locale,jsonb_build_object('status',ci.status,'sourceLocale',ci.source_locale,'sourceHash',ci.source_hash,'model',ci.model,'translatedAt',ci.translated_at,'name',ci.name,'description',ci.description,'seoTitle',ci.seo_title,'seoDescription',ci.seo_description)) FROM category_i18n ci WHERE ci.category_id=c.id),'{}'::jsonb),'children','[]'::jsonb) AS data FROM categories c ORDER BY c.sort,c.id`)
	if err != nil {
		return nil, err
	}
	byID := map[int64]map[string]any{}
	flat := []map[string]any{}
	for _, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		flat = append(flat, row)
		byID[int64(number(row, "id", 0))] = row
	}
	roots := []map[string]any{}
	for _, row := range flat {
		parent := int64(number(row, "parentId", 0))
		if p, ok := byID[parent]; ok {
			children := p["children"].([]any)
			p["children"] = append(children, row)
		} else {
			roots = append(roots, row)
		}
	}
	return roots, nil
}
func (s *Service) categoryWrite(ctx context.Context, op string, in Input) (any, error) {
	return s.write(ctx, in, op, "category", func(st *store.Store) (any, error) {
		id := in.ID
		var result any
		switch op {
		case "DeleteCategory":
			var count int64
			if err := st.DB.WithContext(ctx).Raw("SELECT (SELECT count(*) FROM categories WHERE parent_id=?)+(SELECT count(*) FROM package_categories WHERE category_id=?)", id, id).Scan(&count).Error; err != nil {
				return nil, err
			}
			if count > 0 {
				return nil, fail(domain.CodeInvalidState)
			}
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM categories WHERE id=?", id).Error; err != nil {
				return nil, err
			}
		case "CreateCategory", "UpdateCategory":
			fields := mapped(in.Body, map[string]string{"parentId": "parent_id", "slug": "slug", "icon": "icon", "appliesTo": "applies_to", "visible": "visible", "hiddenByDefault": "hidden_by_default"})
			fields["updated_at"] = s.now()
			if op == "CreateCategory" {
				if err := st.DB.WithContext(ctx).Table("categories").Create(fields).Error; err != nil {
					return nil, err
				}
				if err := st.DB.WithContext(ctx).Raw("SELECT id FROM categories WHERE slug=?", text(in.Body, "slug")).Scan(&id).Error; err != nil {
					return nil, err
				}
				result = map[string]any{"id": id}
			} else {
				if err := st.DB.WithContext(ctx).Table("categories").Where("id=?", id).Updates(fields).Error; err != nil {
					return nil, err
				}
			}
			if err := st.WriteLocalized(ctx, "category", id, 0, in.Body, true); err != nil {
				return nil, err
			}

		case "ReorderCategories":
			list, ok := in.Body["items"].([]any)
			if !ok {
				return nil, domain.Validation()
			}
			seen := map[int64]bool{}
			for _, value := range list {
				row, ok := value.(map[string]any)
				if !ok {
					return nil, domain.Validation()
				}
				id := int64(number(row, "id", 0))
				if id < 1 || seen[id] {
					return nil, domain.Validation()
				}
				seen[id] = true
				r := st.DB.WithContext(ctx).Exec("UPDATE categories SET parent_id=?,sort=?,updated_at=? WHERE id=?", row["parentId"], number(row, "sort", 0), s.now(), id)
				if r.Error != nil {
					return nil, r.Error
				}
				if r.RowsAffected != 1 {
					return nil, fail(domain.CodeNotFound)
				}
			}
		default:
			return nil, fmt.Errorf("unknown category write")
		}
		var cycles int64
		if err := st.DB.WithContext(ctx).Raw(`WITH RECURSIVE ancestors AS(SELECT id,parent_id,ARRAY[id] AS path,false AS cycle FROM categories UNION ALL SELECT a.id,c.parent_id,a.path||c.id,c.id=ANY(a.path) FROM ancestors a JOIN categories c ON c.id=a.parent_id WHERE NOT a.cycle) SELECT count(*) FROM ancestors WHERE cycle`).Scan(&cycles).Error; err != nil {
			return nil, err
		}
		if cycles > 0 {
			return nil, fail(domain.CodeInvalidState)
		}
		if err := st.DB.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT DISTINCT p.id,p.kind,p.token,'upsert','content' FROM packages p JOIN package_categories pc ON pc.package_id=p.id WHERE p.removed_at IS NULL`).Error; err != nil {
			return nil, err
		}
		return result, nil
	})
}
