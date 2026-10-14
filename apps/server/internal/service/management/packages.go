package management

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func (s *Service) packages(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	where := []string{"TRUE"}
	gap, err := gapConditions(in)
	if err != nil {
		return nil, err
	}
	if gap != "" {
		where = append(where, gap)
	}
	args := []any{}
	if kind := text(in.Params, "kind"); kind != "" {
		where = append(where, "p.kind=?")
		args = append(args, kind)
	}
	if q := text(in.Params, "q"); q != "" {
		where = append(where, `(p.token ILIKE ? ESCAPE '\' OR p.names::text ILIKE ? ESCAPE '\' OR EXISTS(SELECT 1 FROM package_i18n names WHERE names.package_id=p.id AND names.display_name ILIKE ? ESCAPE '\'))`)
		like := "%" + searchtext.EscapeLike(q) + "%"
		args = append(args, like, like, like)
	}
	if id := number(in.Params, "categoryid", 0); id > 0 {
		where = append(where, "EXISTS(SELECT 1 FROM package_categories pc WHERE pc.package_id=p.id AND pc.category_id=?)")
		args = append(args, id)
	}
	if status := text(in.Params, "translationstatus"); status != "" {
		where = append(where, "COALESCE(i.status,'none')=?")
		args = append(args, status)
	}
	for key, col := range map[string]string{"hasicon": "m.icon_asset_id IS NOT NULL", "hidden": "COALESCE(m.hidden,false)", "editorchoice": "COALESCE(m.editor_choice,false)", "deprecated": "p.deprecated", "disabled": store.PackageDisabledSQL, "isfont": "p.is_font", "islibrary": "p.is_library"} {
		if v, ok := in.Params[key].(bool); ok {
			where = append(where, "("+col+")=?")
			args = append(args, v)
		}
	}
	order := map[string]string{"popular": "p.popularity DESC,p.id", "updated": "p.updated_at DESC,p.id", "name": "lower(p.name),p.id", "created": "p.created_at DESC,p.id"}[text(in.Params, "sort")]
	if order == "" {
		order = "p.popularity DESC,p.id"
	}
	return s.Store.AdminPackages(ctx, strings.Join(where, " AND "), order, args, current, size)
}
func (s *Service) versions(ctx context.Context, in Input) (any, error) {
	if _, err := s.Store.AdminPackage(ctx, in.ID); err != nil {
		return nil, err
	}
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	note := store.EditorialNoteSQL("v.id", "v.package_id", "v.version_base")
	return s.Store.AdminPage(ctx, `jsonb_build_object('id',v.id,'version',v.version,'firstSeenAt',v.first_seen_at,'brewCommittedAt',v.brew_committed_at,'brewCommitSha',v.brew_commit_sha,'hasEditorial',`+note+` IS NOT NULL,'editorialReleaseId',`+note+`,'editorialScope',`+store.EditorialScopeSQL("v.id", "v.package_id", "v.version_base")+`,'releaseId',COALESCE(`+store.BuildNoteSQL("v.id")+`,(SELECT r.id FROM releases r WHERE r.package_id=v.package_id AND r.version=v.version_base AND r.package_version_id IS NULL AND NOT r.hidden ORDER BY r.published_at DESC NULLS LAST LIMIT 1)))`, `FROM package_versions v`, `v.package_id=?`, `COALESCE(v.brew_committed_at,v.first_seen_at,v.created_at) DESC,v.id DESC`, []any{in.ID}, current, size)
}

var accentColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (s *Service) packageWrite(ctx context.Context, op string, in Input) (any, error) {
	if op == "UpdatePackageMeta" {
		if value := in.Body["downloadSize"]; value != nil {
			n, ok := value.(int64)
			if !ok || n < 0 {
				return nil, domain.Validation()
			}
		}
		if value := in.Body["accentColor"]; value != nil {
			color, ok := value.(string)
			if !ok || !accentColorPattern.MatchString(color) {
				return nil, domain.Validation()
			}
		}
	}
	var accent *string
	if op == "SetPackageIcon" && s.IconAccent != nil {
		var err error
		accent, err = s.IconAccent(ctx, int64(number(in.Body, "assetId", 0)))
		if err != nil {
			return nil, err
		}
	}
	return s.write(ctx, in, op, "package", func(st *store.Store) (any, error) {
		if _, err := st.AdminPackage(ctx, in.ID); err != nil {
			return nil, err
		}
		result := any(nil)
		switch op {
		case "UpdatePackageMeta", "SetPackageIcon", "DeletePackageIcon":
			if err := st.DB.WithContext(ctx).Exec("INSERT INTO package_meta(package_id) VALUES(?) ON CONFLICT DO NOTHING", in.ID).Error; err != nil {
				return nil, err
			}
			if value, present := in.Body["downloadSize"]; present {
				if err := st.DB.WithContext(ctx).Table("packages").Where("id=?", in.ID).Updates(map[string]any{"download_size": value, "updated_at": s.now()}).Error; err != nil {
					return nil, err
				}
			}
			fields := mapped(in.Body, map[string]string{"developer": "developer", "repoUrl": "repo_url", "hidden": "hidden", "editorChoice": "editor_choice", "notes": "notes", "accentColor": "accent_color"})
			fields["updated_by"] = in.Actor.ID
			fields["updated_at"] = s.now()
			if value, ok := in.Body["tags"]; ok {
				data, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				fields["tags"] = gorm.Expr("ARRAY(SELECT jsonb_array_elements_text(?::jsonb))", string(data))
			}
			if op == "SetPackageIcon" {
				assetID := int64(number(in.Body, "assetId", 0))
				a, err := st.AssetByID(ctx, assetID)
				if err != nil {
					return nil, err
				}
				if a.Kind != "icon" {
					return nil, domain.Validation()
				}
				fields["icon_asset_id"] = assetID
				fields["icon_source"] = "manual"
				fields["accent_color"] = accent
			}
			if op == "DeletePackageIcon" {
				fields["icon_asset_id"] = nil
				fields["icon_source"] = nil
				fields["accent_color"] = nil
			}
			if err := st.DB.WithContext(ctx).Table("package_meta").Where("package_id=?", in.ID).Updates(fields).Error; err != nil {
				return nil, err
			}
		case "UpdatePackageI18n":
			source, err := st.TextSourceLocale(ctx, "package", in.ID, in.Locale, in.Body)
			if err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec(`INSERT INTO package_i18n(package_id,locale,status,source) VALUES(?,?,'manual','human') ON CONFLICT DO NOTHING`, in.ID, in.Locale).Error; err != nil {
				return nil, err
			}
			fields := mapped(in.Body, map[string]string{"displayName": "display_name", "summary": "summary", "description": "description"})
			fields["status"] = "manual"
			if in.Locale == source {
				fields["status"] = "source"
			}
			fields["source_locale"] = source
			fields["source"] = "human"
			fields["reviewed_by"] = in.Actor.ID
			fields["reviewed_at"] = s.now()
			fields["updated_at"] = s.now()
			fields["source_hash"] = gorm.Expr("(SELECT encode(sha256(convert_to(COALESCE(desc_en,''),'UTF8')),'hex') FROM packages WHERE id=?)", in.ID)
			if err := st.DB.WithContext(ctx).Table("package_i18n").Where("package_id=? AND locale=?", in.ID, in.Locale).Updates(fields).Error; err != nil {
				return nil, err
			}
			if err := st.StampContentHash(ctx, content.Ref{Entity: "package", ID: in.ID}, in.Locale); err != nil {
				return nil, err
			}

			if in.Locale == source {
				if err := st.ScheduleContentTranslation(ctx, content.Ref{Entity: "package", ID: in.ID}); err != nil {
					return nil, err
				}
			}
		case "SetPackageCategories":
			list, ok := in.Body["items"].([]any)
			if !ok || len(list) > 3 {
				return nil, domain.Validation()
			}
			seen := map[int64]bool{}
			primaries := 0
			for _, value := range list {
				row, ok := value.(map[string]any)
				if !ok {
					return nil, domain.Validation()
				}
				id := int64(number(row, "categoryId", 0))
				if id < 1 || seen[id] {
					return nil, domain.Validation()
				}
				seen[id] = true
				if boolean(row, "isPrimary") {
					primaries++
				}
				var applies, kind string
				if err := st.DB.WithContext(ctx).Raw("SELECT applies_to FROM categories WHERE id=?", id).Scan(&applies).Error; err != nil {
					return nil, err
				}
				if applies == "" {
					return nil, fail(domain.CodeNotFound)
				}
				if err := st.DB.WithContext(ctx).Raw("SELECT kind FROM packages WHERE id=?", in.ID).Scan(&kind).Error; err != nil {
					return nil, err
				}
				if applies != "both" && applies != kind {
					return nil, domain.Validation()
				}
			}
			if len(list) > 0 && primaries != 1 {
				return nil, domain.Validation()
			}
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM package_categories WHERE package_id=?", in.ID).Error; err != nil {
				return nil, err
			}
			for _, value := range list {
				row := value.(map[string]any)
				if err := st.DB.WithContext(ctx).Exec(`INSERT INTO package_categories(package_id,category_id,is_primary,source) VALUES(?,?,?,'human')`, in.ID, number(row, "categoryId", 0), boolean(row, "isPrimary")).Error; err != nil {
					return nil, err
				}
			}
		case "AddPackageScreenshot":
			assetID := int64(number(in.Body, "assetId", 0))
			a, err := st.AssetByID(ctx, assetID)
			if err != nil {
				return nil, err
			}
			if a.Kind != "screenshot" {
				return nil, domain.Validation()
			}
			theme := text(in.Body, "theme")
			if theme == "" {
				theme = "dark"
			}
			var id int64
			if err := st.DB.WithContext(ctx).Raw("INSERT INTO package_screenshots(package_id,asset_id,theme,sort) VALUES(?,?,?,COALESCE((SELECT max(sort)+1 FROM package_screenshots WHERE package_id=?),0)) RETURNING id", in.ID, assetID, theme, in.ID).Scan(&id).Error; err != nil {
				return nil, err
			}
			if err := st.WriteLocalized(ctx, "screenshot", id, 0, in.Body, false); err != nil {
				return nil, err
			}
			result = map[string]any{"id": id}
		case "UpdatePackageScreenshot", "DeletePackageScreenshot":
			var count int64
			if err := st.DB.WithContext(ctx).Table("package_screenshots").Where("id=? AND package_id=?", in.ScreenshotID, in.ID).Count(&count).Error; err != nil {
				return nil, err
			}
			if count == 0 {
				return nil, fail(domain.CodeNotFound)
			}
			if op == "DeletePackageScreenshot" {
				if err := st.DB.WithContext(ctx).Exec("DELETE FROM package_screenshots WHERE id=? AND package_id=?", in.ScreenshotID, in.ID).Error; err != nil {
					return nil, err
				}
			} else {
				if err := st.WriteLocalized(ctx, "screenshot", in.ScreenshotID, 0, in.Body, false); err != nil {
					return nil, err
				}
				fields := mapped(in.Body, map[string]string{"theme": "theme"})
				if len(fields) > 0 {
					if err := st.DB.WithContext(ctx).Table("package_screenshots").Where("id=? AND package_id=?", in.ScreenshotID, in.ID).Updates(fields).Error; err != nil {
						return nil, err
					}
				}
			}
		case "ReorderPackageScreenshots":
			order, err := ids(in.Body["ids"])
			if err != nil {
				return nil, err
			}
			var count int64
			if err := st.DB.WithContext(ctx).Table("package_screenshots").Where("package_id=?", in.ID).Count(&count).Error; err != nil {
				return nil, err
			}
			if len(order) != int(count) {
				return nil, domain.Validation()
			}
			for i, id := range order {
				r := st.DB.WithContext(ctx).Exec("UPDATE package_screenshots SET sort=? WHERE id=? AND package_id=?", i, id, in.ID)
				if r.Error != nil {
					return nil, r.Error
				}
				if r.RowsAffected != 1 {
					return nil, domain.Validation()
				}
			}
		default:
			return nil, fmt.Errorf("unknown package write")
		}
		if err := store.UpdateSearchIndex(ctx, st.DB, []int64{in.ID}); err != nil {
			return nil, err
		}
		if err := st.AppendContentChange(ctx, in.ID); err != nil {
			return nil, err
		}
		return result, nil
	})
}
