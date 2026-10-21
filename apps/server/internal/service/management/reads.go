package management

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/searchtext"
	"github.com/opennavo/opennavo/server/internal/store"
)

const adminUserJSON = `jsonb_build_object('id',u.id,'userName',u.user_name,'nickName',u.nick_name,'email',u.email,'avatarUrl',u.avatar_url,'status',u.status,'roles',COALESCE((SELECT jsonb_agg(r.role_code ORDER BY r.role_code) FROM admin_user_roles ur JOIN admin_roles r ON r.id=ur.role_id WHERE ur.user_id=u.id),'[]'::jsonb),'locked',COALESCE(u.locked_until>now(),false),'lastLoginAt',u.last_login_at,'lastLoginIp',u.last_login_ip,'createTime',u.created_at,'updateTime',u.updated_at,'createBy',creator.user_name,'updateBy',editor.user_name)`
const roleJSON = `jsonb_build_object('id',r.id,'roleCode',r.role_code,'roleName',r.role_name,'roleDesc',r.role_desc,'status',r.status,'builtin',r.builtin,'permissionCodes',COALESCE((SELECT jsonb_agg(rp.permission_code ORDER BY rp.permission_code) FROM admin_role_permissions rp WHERE rp.role_id=r.id),'[]'::jsonb),'userCount',(SELECT count(*) FROM admin_user_roles ur WHERE ur.role_id=r.id))`
const feedbackJSON = `jsonb_build_object('id',f.id,'type',f.type,'packageId',f.package_id,'packageKind',p.kind,'packageToken',p.token,'packageName',p.name,'content',f.content,'contact',f.contact,'platform',f.platform,'appVersion',f.app_version,'osVersion',f.os_version,'errorCode',f.error_code,'packageVersion',f.package_version,'macosVersion',f.macos_version,'symptom',f.symptom,'diagnosticLog',f.diagnostic_log,'links',f.links,'visibility',f.visibility,'status',f.status,'handlerNote',f.handler_note,'handledBy',u.user_name,'handledAt',f.handled_at,'createTime',f.created_at)`
const jobJSON = `jsonb_build_object('id',j.id,'jobType',j.job_type,'trigger',j.trigger,'triggeredBy',u.user_name,'status',j.status,'stats',j.stats,'error',j.error,'startedAt',j.started_at,'finishedAt',j.finished_at,'durationMs',floor(extract(epoch FROM (j.finished_at-j.started_at))*1000)::bigint)`

func (s *Service) read(ctx context.Context, op string, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	expr, from, order := "", "", ""
	where := []string{"TRUE"}
	args := []any{}
	filter := func(key, col string) {
		if v := text(in.Params, key); v != "" {
			where = append(where, col+"=?")
			args = append(args, v)
		}
	}
	switch op {
	case "ListAssets":
		expr = `jsonb_build_object('id',a.id,'kind',a.kind,'url',a.url,'mime',a.mime,'width',a.width,'height',a.height,'bytes',a.bytes,'sha256',a.sha256,'createTime',a.created_at)`
		from = "FROM assets a"
		order = "a.created_at DESC,a.id DESC"
		filter("kind", "a.kind")
	case "ListFeedback", "GetFeedback":
		expr = feedbackJSON
		from = "FROM feedback f LEFT JOIN packages p ON p.id=f.package_id LEFT JOIN admin_users u ON u.id=f.handled_by"
		order = "f.created_at DESC,f.id DESC"
		filter("status", "f.status")
		filter("type", "f.type")
		if op == "GetFeedback" {
			return s.Store.JSONRow(ctx, "SELECT "+expr+" AS data "+from+" WHERE f.id=?", in.ID)
		}
	case "ListSynonyms":
		expr = `jsonb_build_object('id',s.id,'terms',s.terms,'enabled',s.enabled,'createTime',s.created_at,'updateTime',s.updated_at,'updateBy',u.user_name)`
		from = "FROM search_synonyms s LEFT JOIN admin_users u ON u.id=s.updated_by"
		order = "s.id DESC"
		if q := text(in.Params, "q"); q != "" {
			where = append(where, `s.terms::text ILIKE ? ESCAPE '\'`)
			args = append(args, "%"+searchtext.EscapeLike(q)+"%")
		}
	case "ListTopQueries", "ListZeroResultQueries":
		return s.queryInsights(ctx, op, in, current, size)
	case "ListJobRuns", "GetJobRun":
		expr = jobJSON
		from = "FROM sync_runs j LEFT JOIN admin_users u ON u.id=j.triggered_by"
		order = "j.started_at DESC,j.id DESC"
		filter("jobtype", "j.job_type")
		filter("status", "j.status")
		if op == "GetJobRun" {
			return s.Store.JSONRow(ctx, "SELECT "+expr+" AS data "+from+" WHERE j.id=?", in.ID)
		}
	case "ListMirrors":
		return s.Store.JSONRows(ctx, `SELECT jsonb_build_object('id',m.id,'key',m.key,'sourceLocale',m.source_locale,'i18n',COALESCE((SELECT jsonb_object_agg(t.locale,jsonb_build_object('name',t.name,'status',t.status,'sourceLocale',t.source_locale,'sourceHash',t.source_hash,'model',t.model,'translatedAt',t.translated_at)) FROM mirror_i18n t WHERE t.mirror_id=m.id),'{}'::jsonb),'apiDomain',m.api_domain,'bottleDomain',m.bottle_domain,'brewGitRemote',m.brew_git_remote,'coreGitRemote',m.core_git_remote,'probeUrl',m.probe_url,'recommended',m.recommended,'enabled',m.enabled,'sort',m.sort,'createTime',m.created_at,'updateTime',m.updated_at,'createBy',NULL,'updateBy',u.user_name) AS data FROM mirrors m LEFT JOIN admin_users u ON u.id=m.updated_by ORDER BY m.sort,m.id`)
	case "ListAppConfig":
		return s.Store.JSONRows(ctx, `SELECT jsonb_build_object('key',c.key,'value',c.value,'description',c.description,'updateTime',c.updated_at,'updateBy',u.user_name) AS data FROM app_config c LEFT JOIN admin_users u ON u.id=c.updated_by WHERE c.key NOT LIKE 'catalog.%' AND c.key<>'llm.prices' AND c.key<>'desktop.announcement' ORDER BY c.key`)
	case "ListAdminUsers":
		expr = adminUserJSON
		from = "FROM admin_users u LEFT JOIN admin_users creator ON creator.id=u.created_by LEFT JOIN admin_users editor ON editor.id=u.updated_by"
		order = "u.id"
		filter("status", "u.status")
		if q := text(in.Params, "q"); q != "" {
			where = append(where, `(u.user_name ILIKE ? ESCAPE '\' OR u.nick_name ILIKE ? ESCAPE '\')`)
			like := "%" + searchtext.EscapeLike(q) + "%"
			args = append(args, like, like)
		}
	case "ListRoles":
		return s.Store.JSONRows(ctx, "SELECT "+roleJSON+" AS data FROM admin_roles r ORDER BY r.id")
	case "ListPermissions":
		return s.Store.JSONRows(ctx, `SELECT jsonb_build_object('code',code,'name',name,'group',group_name) AS data FROM admin_permissions ORDER BY group_name,code`)
	case "ListAuditLogs":
		expr = `jsonb_build_object('id',a.id,'actorId',a.actor_id,'actorName',u.user_name,'action',a.action,'entityType',a.entity_type,'entityId',a.entity_id,'before',a.before,'after',a.after,'ip',a.ip,'createTime',a.created_at)`
		from = "FROM audit_logs a LEFT JOIN admin_users u ON u.id=a.actor_id"
		order = "a.created_at DESC,a.id DESC"
		filter("entitytype", "a.entity_type")
		filter("entityid", "a.entity_id")
		if id := number(in.Params, "actorid", 0); id > 0 {
			where = append(where, "a.actor_id=?")
			args = append(args, id)
		}
	default:
		return nil, domain.Validation()
	}
	return s.Store.AdminPage(ctx, expr, from, strings.Join(where, " AND "), order, args, current, size)
}
func (s *Service) queryInsights(ctx context.Context, op string, in Input, current, size int) (any, error) {
	days := number(in.Params, "days", 30)
	cutoff := s.now().Add(-time.Duration(days) * 24 * time.Hour)
	zero := ""
	if op == "ListZeroResultQueries" {
		zero = " AND sq.result_count=0"
	}
	query := `SELECT jsonb_build_object('query',sq.normalized,'count',count(*),'zeroResultCount',count(*) FILTER(WHERE sq.result_count=0),'clickCount',count(*) FILTER(WHERE sq.clicked_package_id IS NOT NULL),'topClicked',(SELECT p.token FROM search_queries sub JOIN packages p ON p.id=sub.clicked_package_id WHERE sub.normalized=sq.normalized AND sub.created_at>=? GROUP BY p.id,p.token ORDER BY count(*) DESC,p.id LIMIT 1)) AS data FROM search_queries sq WHERE sq.created_at>=?` + zero + ` GROUP BY sq.normalized ORDER BY count(*) DESC,sq.normalized LIMIT ? OFFSET ?`
	var total int64
	err := s.Store.DB.WithContext(ctx).Raw("SELECT count(DISTINCT normalized) FROM search_queries sq WHERE sq.created_at>=?"+zero, cutoff).Scan(&total).Error
	if err != nil {
		return nil, err
	}
	rows := []json.RawMessage{}
	if offset, found := domain.PageOffset(current, size, total); found {
		rows, err = s.Store.JSONRows(ctx, query, cutoff, cutoff, size, offset)
	}
	return map[string]any{"current": current, "size": size, "records": rows, "total": total}, err
}
func (s *Service) dashboard(ctx context.Context) (any, error) {
	// All four metrics share the valid-app-and-font set; six-language coverage counts actual current-source content, never display fallback.
	raw, err := s.Store.JSONRow(ctx, `
WITH eligible AS (
 SELECT p.id,p.source_locale FROM packages p
 LEFT JOIN package_meta m ON m.package_id=p.id
 WHERE p.kind='cask' AND p.removed_at IS NULL
 AND NOT (p.disabled OR p.stale_disabled) AND NOT COALESCE(m.hidden,false)
), complete_content AS (
 SELECT e.id FROM eligible e
 JOIN content_translation_sources s ON s.entity='package' AND s.object_key=e.id::text
 AND s.source_locale=e.source_locale
 JOIN package_i18n original ON original.package_id=e.id AND original.locale=e.source_locale
 AND original.source<>'homebrew' AND original.source_hash=s.source_hash
 JOIN package_i18n i ON i.package_id=e.id
 WHERE i.locale IN ? AND i.source_locale=e.source_locale
 AND i.status IN ('source','machine','manual') AND i.source_hash=s.source_hash
 AND i.summary ~ '[^[:space:]]' AND i.description ~ '[^[:space:]]'
 AND s.fields=jsonb_build_object('display_name',original.display_name,'summary',original.summary,'description',original.description)
 GROUP BY e.id HAVING count(*)=?
)
SELECT jsonb_build_object(
 'packages',(SELECT jsonb_build_object(
  'casks',count(*) FILTER(WHERE NOT p.is_font AND NOT (p.disabled OR p.stale_disabled)),'formulae',count(*) FILTER(WHERE p.kind='formula'),
  'fonts',count(*) FILTER(WHERE p.is_font AND NOT (p.disabled OR p.stale_disabled)),'libraries',count(*) FILTER(WHERE p.is_library),
  'hidden',count(*) FILTER(WHERE m.hidden),'deprecated',count(*) FILTER(WHERE p.deprecated),
  'disabled',count(*) FILTER(WHERE p.disabled OR p.stale_disabled))
  FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id
  WHERE p.kind='cask' AND p.removed_at IS NULL),
 'coverage',jsonb_build_object(
  'primaryCategory',jsonb_build_object(
   'done',(SELECT count(DISTINCT e.id) FROM eligible e JOIN package_categories c ON c.package_id=e.id AND c.is_primary),
   'total',(SELECT count(*) FROM eligible)),
  'icon',jsonb_build_object(
   'done',(SELECT count(*) FROM eligible e JOIN package_meta m ON m.package_id=e.id WHERE m.icon_asset_id IS NOT NULL),
   'total',(SELECT count(*) FROM eligible)),
  'latestVersionNotes',jsonb_build_object(
   'done',(SELECT count(*) FROM eligible e JOIN packages p ON p.id=e.id
    WHERE `+store.EditorialNoteSQL(currentBuild, "p.id", "p.version_base")+` IS NOT NULL),
   'total',(SELECT count(*) FROM eligible)),
  'sixLocaleContent',jsonb_build_object('done',(SELECT count(*) FROM complete_content),'total',(SELECT count(*) FROM eligible))),
 'feedbackOpen',(SELECT count(*) FROM feedback WHERE status='open'),
 'jobs',jsonb_build_object(
  'failed24h',(SELECT count(*) FROM sync_runs WHERE status IN ('failed','partial') AND started_at>now()-interval '1 day'),
  'lastCatalogSyncAt',(SELECT max(finished_at) FROM sync_runs WHERE job_type='catalog:sync' AND status='succeeded'),
  'lastAnalyticsSyncAt',(SELECT max(finished_at) FROM sync_runs WHERE job_type='analytics:sync' AND status='succeeded'),
  'lastSnapshotAt',(SELECT max(created_at) FROM catalog_snapshots)),
 'snapshot',COALESCE((SELECT jsonb_build_object('cursor',seq,'itemCount',item_count,'url',url)
  FROM catalog_snapshots ORDER BY created_at DESC,id DESC LIMIT 1),jsonb_build_object('cursor',0,'itemCount',0))
) AS data`, i18n.SiteLocales[:], len(i18n.SiteLocales))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	var metrics struct{ Writes, Translations, Failures int64 }
	day := s.now().Truncate(24 * time.Hour)
	if err := s.Store.DB.WithContext(ctx).Raw(`SELECT (SELECT count(*) FROM agent_call_logs WHERE created_at>=? AND code='0000' AND is_write AND NOT dry_run) writes,(SELECT count(*) FROM content_revisions WHERE created_at>=? AND actor_type='agent' AND operation_id='fixTranslation') translations,(SELECT count(*) FROM agent_call_logs WHERE created_at>=? AND code<>'0000') failures`, day, day, day).Scan(&metrics).Error; err != nil {
		return nil, err
	}
	usage, err := s.Store.UsageOverview(ctx, s.now())
	if err != nil {
		return nil, err
	}
	out["usage"] = usage
	out["hermes"] = map[string]any{"date": day.Format("2006-01-02"), "timeZone": "UTC", "writes": metrics.Writes, "translations": metrics.Translations, "failures": metrics.Failures}
	return out, nil
}
