package management

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seoingest"
	"github.com/opennavo/opennavo/server/internal/service/seokeywords"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) seoKeywords(ctx context.Context, op string, in Input) (any, error) {
	k := &seokeywords.Service{DB: s.Store.DB, WebURL: s.Config.WebBaseURL, Now: s.Now}
	if op == "ListSeoKeywords" {
		current, size, err := page(in)
		if err != nil {
			return nil, err
		}
		return k.List(ctx, text(in.Params, "language"), text(in.Params, "q"), current, size)
	}
	encoded, err := json.Marshal(in.Body)
	if err != nil {
		return nil, domain.Validation()
	}
	var request seokeywords.Request
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return nil, domain.Validation()
	}
	return k.Upsert(ctx, request, domain.CurrentOperation(ctx).DryRun, in.Actor)
}

func (s *Service) seoRows(ctx context.Context, query string, args ...any) ([]any, error) {
	var rows []string
	if err := s.Store.DB.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := []any{}
	for _, raw := range rows {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Service) seoOverview(ctx context.Context) (any, error) {
	watermarks, err := s.seoRows(ctx, `SELECT jsonb_build_object('source',source,'completeThrough',complete_through,'freshThrough',fresh_through,'updatedAt',updated_at)::text FROM seo_data_watermarks ORDER BY source`)
	if err != nil {
		return nil, err
	}
	var keywordCount, targetCount int64
	if err := s.Store.DB.WithContext(ctx).Table("seo_keywords").Count(&keywordCount).Error; err != nil {
		return nil, err
	}
	if err := s.Store.DB.WithContext(ctx).Table("seo_targets").Count(&targetCount).Error; err != nil {
		return nil, err
	}
	site := seobudget.Site(s.Config.WebBaseURL)
	breakers, err := s.seoRows(ctx, `SELECT jsonb_build_object('kind',kind,'scope',scope,'reason',reason,'openedAt',opened_at,'retryAt',retry_at,'evidence',evidence)::text FROM seo_breakers WHERE site=? AND closed_at IS NULL AND (retry_at IS NULL OR retry_at>clock_timestamp()) ORDER BY kind,scope`, site)
	if err != nil {
		return nil, err
	}
	budgets, err := s.seoRows(ctx, `SELECT jsonb_build_object('action',a.key,'period','day','limit',a.value::int,'used',count(q.entity_key),'remaining',greatest(0,a.value::int-count(q.entity_key)))::text FROM seo_settings s CROSS JOIN jsonb_each_text(s.automation->'dailyEntities') a LEFT JOIN seo_daily_entities q ON q.site=? AND q.action=a.key AND q.day=(clock_timestamp() AT TIME ZONE 'UTC')::date GROUP BY a.key,a.value ORDER BY a.key`, site)
	if err != nil {
		return nil, err
	}
	costs, err := s.seoRows(ctx, `SELECT jsonb_build_object('action','paid_research','period',p.period,'limitMicros',(s.automation->>p.setting)::bigint,'usedMicros',coalesce(sum(r.cost_micros),0),'remainingMicros',greatest(0,(s.automation->>p.setting)::bigint-coalesce(sum(r.cost_micros),0)))::text FROM seo_settings s CROSS JOIN (VALUES ('day','researchDailyMicros'),('month','researchMonthlyMicros')) p(period,setting) LEFT JOIN seo_cost_receipts r ON r.site=? AND r.incurred_at>=date_trunc(p.period,clock_timestamp() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' GROUP BY p.period,p.setting,s.automation`, site)
	if err != nil {
		return nil, err
	}
	budgets = append(budgets, costs...)
	modes, err := s.seoRows(ctx, `SELECT (jsonb_build_object('action',a.key)||a.value||CASE WHEN a.key='article' THEN '{"mode":"live"}'::jsonb ELSE '{}'::jsonb END)::text FROM seo_settings s CROSS JOIN jsonb_each(s.automation->'actions') a ORDER BY a.key`)
	if err != nil {
		return nil, err
	}
	// Report operation-level overrides without misrepresenting other operations
	// that still use the configured action policy.
	for _, raw := range modes {
		mode, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var overrides []string
		for _, operation := range strings.Fields("UpdatePackageText UpdatePackageMeta SetPackagePrice SetPackageMacosSupport SetPackageAlternatives UpdatePackageI18n SetPackageCategories SetPackageFactRecords SetPackageIcon DeletePackageIcon AddPackageScreenshot ReorderPackageScreenshots UpdatePackageScreenshot DeletePackageScreenshot UpdatePackageChangelogSettings UpsertReleaseNotes UpdateRelease UpdateReleaseI18n UpsertSeoListingCandidate") {
			rule, found := agentgate.RuleFor(operation)
			if found && rule.Action == mode["action"] {
				overrides = append(overrides, operation)
			}
		}
		if mode["action"] == "translation" {
			overrides = append(overrides, "FixTranslation (package/release)", "WriteTranslations (package/release)")
		}
		if len(overrides) > 0 {
			mode["liveOperations"] = overrides
		}
	}
	yesterday, err := s.seoRows(ctx, `WITH recorded AS (
 SELECT operation,mode,created_at,compensated_at FROM seo_actions
 UNION ALL
 SELECT operation,result AS mode,created_at,NULL::timestamptz FROM seo_gate_events
 WHERE task_id IS NULL AND result='live'
) SELECT jsonb_build_object('operation',operation,'mode',mode,'count',count(*),'compensated',count(*) FILTER(WHERE compensated_at IS NOT NULL))::text FROM recorded WHERE created_at>=((clock_timestamp() AT TIME ZONE 'UTC')::date-1)::timestamp AT TIME ZONE 'UTC' AND created_at<(clock_timestamp() AT TIME ZONE 'UTC')::date::timestamp AT TIME ZONE 'UTC' GROUP BY operation,mode ORDER BY operation,mode`)
	if err != nil {
		return nil, err
	}
	var automationReady bool
	if err := s.Store.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM seo_data_watermarks WHERE source IN ('gsc_page','gsc_query','bing') AND complete_through >= (clock_timestamp() AT TIME ZONE 'UTC')::date-7 AND fresh_through >= (clock_timestamp() AT TIME ZONE 'UTC')::date-7 AND updated_at >= clock_timestamp()-interval '7 days')`).Scan(&automationReady).Error; err != nil {
		return nil, err
	}
	geo, err := s.geoOverview(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"geo": geo, "watermarks": watermarks, "keywordCount": keywordCount, "targetCount": targetCount, "automationReady": automationReady, "breakers": breakers, "budgets": budgets, "actionModes": modes, "yesterday": map[string]any{"actions": yesterday}}, nil
}

// seoAlerts reads the audit issue that creates tech_alert work. A task being
// completed does not resolve an issue; only a later successful audit does.
func (s *Service) seoAlerts(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	filter := "true"
	switch text(in.Params, "status") {
	case "":
	case "open":
		filter = "a.resolved_at IS NULL"
	case "resolved":
		filter = "a.resolved_at IS NOT NULL"
	default:
		return nil, domain.Validation()
	}
	// One statement gives the count and the page a common database snapshot.
	return s.Store.JSONRow(ctx, `WITH selected AS (
 SELECT a.id,a.resolved_at IS NULL is_open,a.last_seen,
 jsonb_build_object('id',a.id,'kind','tech_alert','rule',a.rule,'url',u.url,
  'layer',a.layer,'severity',a.severity,'detail',a.detail,'firstSeen',a.first_seen,
  'lastSeen',a.last_seen,'resolvedAt',a.resolved_at,
  'status',CASE WHEN a.resolved_at IS NULL THEN 'open' ELSE 'resolved' END) entry
 FROM seo_audit_issues a JOIN seo_urls u ON u.id=a.url_id WHERE `+filter+`
 ORDER BY (a.resolved_at IS NULL) DESC,a.last_seen DESC,a.id DESC LIMIT ? OFFSET ?
) SELECT jsonb_build_object('current',?::bigint,'size',?::integer,
 'total',(SELECT count(*) FROM seo_audit_issues a WHERE `+filter+`),
 'records',coalesce((SELECT jsonb_agg(entry ORDER BY is_open DESC,last_seen DESC,id DESC) FROM selected),'[]'::jsonb)) AS data`,
		size, (current-1)*size, current, size)
}

func (s *Service) seoPage(ctx context.Context, in Input) (any, error) {
	raw := text(in.Params, "url")
	u, err := url.Parse(raw)
	base, baseErr := url.Parse(s.Config.WebBaseURL)
	if err != nil || baseErr != nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil || u.Fragment != "" || len(raw) > 2048 {
		return nil, domain.Validation()
	}
	var registered seoingest.URL
	if err := s.Store.DB.WithContext(ctx).Table("seo_urls").Where("url=?", raw).Find(&registered).Error; err != nil {
		return nil, err
	}
	registered.URL = raw
	path := u.Path
	locale := "en-US"
	for _, code := range i18n.SiteLocales {
		prefix := i18n.Metadata[code].Prefix
		if prefix != "" && strings.HasPrefix(path, prefix+"/") {
			locale = string(code)
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "apps" {
		if registered.ID == 0 {
			return nil, fail(domain.CodeNotFound)
		}
		return s.seoMeasuredPage(ctx, registered, "url:"+strconv.FormatInt(registered.ID, 10), locale, nil, []any{}, []string{})
	}
	row, found, err := s.Store.IndexRowByToken(ctx, parts[1])
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(domain.CodeNotFound)
	}
	registered.Locale = locale
	registered.PackageID = &row.ID
	registered.PageKind = "overview"
	if len(parts) > 2 {
		registered.PageKind = parts[2]
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
	pageNumber, _ := strconv.Atoi(u.Query().Get("page"))
	ref := seo.PageRef{Kind: seo.PageKind(registered.PageKind), Locale: locale, Page: pageNumber, Filtered: u.Query().Get("notes") != "" || u.Query().Get("original") != ""}
	rows := []store.IndexRow{row}
	if ref.Kind == seo.PageVersions {
		if err := s.Store.PopulatePageNotes(ctx, rows, max(1, pageNumber)); err != nil {
			return nil, err
		}
	}
	if ref.Kind == seo.PageAlternatives || ref.Kind == seo.PageUninstall {
		if err := s.Store.PopulateIntentEligibility(ctx, rows, policy, s.Config.WebBaseURL); err != nil {
			return nil, err
		}
	}
	row = rows[0]
	decision := seo.DecidePage(row.Facts(), ref, policy)
	languages := []any{}
	for _, code := range i18n.SiteLocales {
		languages = append(languages, map[string]any{"locale": string(code), "ready": seo.TranslationReady(row.Facts(), string(code))})
	}
	facts, err := factsView(ctx, s.Store, row.ID, s.now())
	if err != nil {
		return nil, err
	}
	return s.seoMeasuredPage(ctx, registered, "pkg:"+strconv.FormatInt(row.ID, 10), locale, decision, languages, facts["gaps"])
}

func (s *Service) seoMeasuredPage(ctx context.Context, registered seoingest.URL, entity, locale string, decision, languages, missing any) (any, error) {
	issues, err := s.seoRows(ctx, `SELECT jsonb_build_object('rule',rule,'layer',layer,'severity',severity,'detail',detail,'firstSeen',first_seen,'lastSeen',last_seen)::text FROM seo_audit_issues WHERE url_id=? AND resolved_at IS NULL ORDER BY rule,layer`, registered.ID)
	if err != nil {
		return nil, err
	}
	stats, err := s.seoRows(ctx, `SELECT jsonb_build_object('completeThrough',w.complete_through,'clicks',coalesce(sum(p.clicks),0),'impressions',coalesce(sum(p.impressions),0),'averagePosition',sum(p.position_sum)/nullif(sum(p.impressions),0))::text FROM seo_data_watermarks w LEFT JOIN seo_gsc_page_daily p ON p.url_id=? AND p.day BETWEEN w.complete_through-27 AND w.complete_through WHERE w.source='gsc_page' GROUP BY w.complete_through`, registered.ID)
	if err != nil {
		return nil, err
	}
	var gsc any
	if len(stats) > 0 {
		gsc = stats[0]
	}
	var checked *time.Time
	if err := s.Store.DB.WithContext(ctx).Raw("SELECT checked_at FROM seo_link_crawls WHERE url_id=? AND layer='edge' AND checked_at>clock_timestamp()-interval '14 days'", registered.ID).Scan(&checked).Error; err != nil {
		return nil, err
	}
	var inbound, outbound any
	coverage := "unknown"
	if checked != nil && !checked.IsZero() {
		var incoming, outgoing int64
		if err := s.Store.DB.WithContext(ctx).Raw("SELECT count(*) FROM seo_internal_links l JOIN seo_link_crawls c ON c.url_id=l.from_url_id AND c.layer=l.layer WHERE l.target_url=? AND l.layer='edge' AND c.checked_at>clock_timestamp()-interval '14 days'", registered.URL).Scan(&incoming).Error; err != nil {
			return nil, err
		}
		if err := s.Store.DB.WithContext(ctx).Table("seo_internal_links").Where("from_url_id=? AND layer='edge'", registered.ID).Count(&outgoing).Error; err != nil {
			return nil, err
		}
		inbound, outbound, coverage = incoming, outgoing, "sampled_html"
	}
	return map[string]any{"url": registered.URL, "urlId": registered.ID, "entityKey": entity, "locale": locale, "indexing": decision, "languages": languages, "missingFacts": missing, "inboundLinks": inbound, "outboundLinks": outbound, "linkCoverage": coverage, "linksCheckedAt": checked, "auditIssues": issues, "gsc": gsc}, nil
}
