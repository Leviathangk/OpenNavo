package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"gorm.io/gorm"
)

type AdminPage struct {
	Current int               `json:"current"`
	Size    int               `json:"size"`
	Total   int64             `json:"total"`
	Records []json.RawMessage `json:"records"`
}

func (s *Store) JSONRows(ctx context.Context, query string, args ...any) ([]json.RawMessage, error) {
	var rows []struct{ Data json.RawMessage }
	if err := s.DB.WithContext(ctx).Raw(adminLocaleSQL(ctx, query), args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Data)
	}
	return out, nil
}
func (s *Store) JSONRow(ctx context.Context, query string, args ...any) (json.RawMessage, error) {
	rows, err := s.JSONRows(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	return rows[0], nil
}
func (s *Store) AdminPage(ctx context.Context, expr, from, where, order string, args []any, current, size int) (AdminPage, error) {
	out := AdminPage{Current: current, Size: size, Records: []json.RawMessage{}}
	if err := s.DB.WithContext(ctx).Raw(adminLocaleSQL(ctx, "SELECT count(*) "+from+" WHERE "+where), args...).Scan(&out.Total).Error; err != nil {
		return out, err
	}
	offset, found := domain.PageOffset(current, size, out.Total)
	if !found {
		return out, nil
	}
	params := append(append([]any{}, args...), size, offset)
	rows, err := s.JSONRows(ctx, "SELECT "+expr+" AS data "+from+" WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", params...)
	out.Records = rows
	return out, err
}
func MapAdminError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return &domain.AppError{Code: domain.CodeConflict, HTTPStatus: 409}
		case "23503":
			return &domain.AppError{Code: domain.CodeInvalidState, HTTPStatus: 409}
		case "23514", "22P02":
			return domain.Validation()
		}
	}
	return fmt.Errorf("admin data operation: %w", err)
}
func (s *Store) AppendContentChange(ctx context.Context, id int64) error {
	return s.DB.WithContext(ctx).Exec("INSERT INTO catalog_changes(package_id,kind,token,op,reason) SELECT id,kind,token,'upsert','content' FROM packages WHERE id=?", id).Error
}
func (s *Store) AssetByID(ctx context.Context, id int64) (Asset, error) {
	var a Asset
	if err := s.DB.WithContext(ctx).Raw("SELECT * FROM assets WHERE id=?", id).Scan(&a).Error; err != nil {
		return a, err
	}
	if a.ID == 0 {
		return a, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	return a, nil
}

const adminPackageFrom = `FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id LEFT JOIN LATERAL (SELECT t.* FROM package_i18n t WHERE t.package_id=p.id AND t.locale IN (@onv_locale,p.source_locale,'en-US') ORDER BY ` + adminLocaleOrder + ` LIMIT 1) i ON TRUE LEFT JOIN assets a ON a.id=m.icon_asset_id`
const adminPackageExpr = `jsonb_build_object('id',p.id,'kind',p.kind,'token',p.token,'name',p.name,'sourceLocale',p.source_locale,'displayName',COALESCE((SELECT jsonb_object_agg(t.locale,t.display_name) FROM package_i18n t WHERE t.package_id=p.id),'{}'::jsonb),'summary',COALESCE((SELECT jsonb_object_agg(t.locale,t.summary) FROM package_i18n t WHERE t.package_id=p.id),'{}'::jsonb),'iconUrl',a.url,'version',p.version,'installs30d',p.installs_30d,'rank30d',p.rank_30d,'primaryCategory',(SELECT jsonb_build_object('id',c.id,'slug',c.slug,'name',COALESCE(ci.name,c.slug)) FROM package_categories pc JOIN categories c ON c.id=pc.category_id LEFT JOIN LATERAL (SELECT t.* FROM category_i18n t WHERE t.category_id=c.id AND t.locale IN (@onv_locale,c.source_locale,'en-US') ORDER BY ` + adminLocaleOrder + ` LIMIT 1) ci ON TRUE WHERE pc.package_id=p.id AND pc.is_primary),'translationStatus',COALESCE(i.status,'none'),'changelogStatus',CASE WHEN EXISTS(SELECT 1 FROM changelog_sources cs WHERE cs.package_id=p.id AND cs.last_status='error') THEN 'error' WHEN EXISTS(SELECT 1 FROM changelog_sources cs WHERE cs.package_id=p.id AND cs.enabled) THEN 'ok' ELSE 'none' END,'hasIcon',m.icon_asset_id IS NOT NULL,'screenshotCount',(SELECT count(*) FROM package_screenshots ps WHERE ps.package_id=p.id),'hidden',COALESCE(m.hidden,false),'editorChoice',COALESCE(m.editor_choice,false),'deprecated',p.deprecated,'disabled',` + PackageDisabledSQL + `,'isFont',p.is_font,'isLibrary',p.is_library,'removed',p.removed_at IS NOT NULL,'versionChangedAt',p.version_changed_at,'updateTime',GREATEST(p.updated_at,m.updated_at,i.updated_at))`

func (s *Store) AdminPackages(ctx context.Context, condition, order string, args []any, current, size int) (AdminPage, error) {
	return s.AdminPage(ctx, adminPackageExpr, adminPackageFrom, "p.kind IN ('cask','link') AND ("+condition+")", order, args, current, size)
}
func (s *Store) AdminPackage(ctx context.Context, id int64) (json.RawMessage, error) {
	raw, err := s.JSONRow(ctx, "SELECT "+adminPackageExpr+`||jsonb_build_object('names',p.names,'descEn',p.desc_en,'homepage',p.homepage,'rubySourcePath',p.ruby_source_path,'downloadUrl',p.download_url,'downloadSize',p.download_size,'tap',p.tap,'raw',p.raw,'meta',jsonb_build_object('developer',m.developer,'repoUrl',m.repo_url,'tags',COALESCE(to_jsonb(m.tags),'[]'::jsonb),'notes',m.notes,'accentColor',m.accent_color,'iconSource',m.icon_source),'categories',COALESCE((SELECT jsonb_agg(jsonb_build_object('categoryId',pc.category_id,'slug',c.slug,'name',COALESCE(ci.name,c.slug),'isPrimary',pc.is_primary,'source',pc.source,'confidence',pc.confidence) ORDER BY pc.is_primary DESC,c.sort) FROM package_categories pc JOIN categories c ON c.id=pc.category_id LEFT JOIN LATERAL (SELECT t.* FROM category_i18n t WHERE t.category_id=c.id AND t.locale IN (@onv_locale,c.source_locale,'en-US') ORDER BY `+adminLocaleOrder+` LIMIT 1) ci ON TRUE WHERE pc.package_id=p.id),'[]'::jsonb),'i18n',COALESCE((SELECT jsonb_agg(jsonb_build_object('locale',pi.locale,'sourceLocale',pi.source_locale,'sourceHash',pi.source_hash,'translatedAt',pi.translated_at,'displayName',pi.display_name,'summary',pi.summary,'description',pi.description,'status',pi.status,'source',pi.source,'model',pi.model,'reviewedBy',u.user_name,'reviewedAt',pi.reviewed_at,'updateTime',pi.updated_at,'stale',pi.source_hash IS DISTINCT FROM encode(sha256(convert_to(COALESCE(p.desc_en,''),'UTF8')),'hex'))) FROM package_i18n pi LEFT JOIN admin_users u ON u.id=pi.reviewed_by WHERE pi.package_id=p.id),'[]'::jsonb),'screenshots',COALESCE((SELECT jsonb_agg(`+adminScreenshotExpr+` ORDER BY ps.sort,ps.id) FROM package_screenshots ps JOIN assets sa ON sa.id=ps.asset_id WHERE ps.package_id=p.id),'[]'::jsonb)) AS data `+adminPackageFrom+" WHERE p.kind IN ('cask','link') AND p.id=?", id)
	if err != nil {
		return nil, err
	}
	return s.withContentStale(ctx, raw, content.Ref{Entity: "package", ID: id})
}

const adminScreenshotExpr = `jsonb_build_object('id',ps.id,'assetId',ps.asset_id,'url',sa.url,'thumbUrl',regexp_replace(sa.url,'-1280[.]png$','-640.png'),'width',sa.width,'height',sa.height,'sourceLocale',ps.source_locale,'i18n',COALESCE((SELECT jsonb_object_agg(t.locale,jsonb_build_object('caption',t.caption,'status',t.status,'sourceLocale',t.source_locale,'sourceHash',t.source_hash,'model',t.model,'translatedAt',t.translated_at)) FROM screenshot_i18n t WHERE t.screenshot_id=ps.id),'{}'::jsonb),'theme',ps.theme,'sort',ps.sort)`

func (s *Store) AdminScreenshots(ctx context.Context, id int64) ([]json.RawMessage, error) {
	return s.JSONRows(ctx, "SELECT "+adminScreenshotExpr+" AS data FROM package_screenshots ps JOIN assets sa ON sa.id=ps.asset_id WHERE ps.package_id=? ORDER BY ps.sort,ps.id", id)
}

func adminLocaleSQL(ctx context.Context, query string) string {
	return strings.ReplaceAll(query, "@onv_locale", "'"+i18n.FromContext(ctx)+"'")
}
