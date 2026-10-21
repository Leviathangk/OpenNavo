// Package seobudget maintains shared action limits and reconciles external
// research receipts. It never calls or proxies a paid provider.
package seobudget

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

var ErrLimit = errors.New("SEO shared action quota exhausted")
var ErrFrozen = errors.New("SEO action frozen")
var ErrReceipt = errors.New("invalid or conflicting SEO receipt")

type Settings struct {
	DailyEntities     map[string]int `json:"dailyEntities"`
	DailyMicros       int64          `json:"researchDailyMicros"`
	MonthlyMicros     int64          `json:"researchMonthlyMicros"`
	BusinessDropRatio float64        `json:"businessDropRatio"`
}

func Site(base string) string {
	u, err := url.Parse(base)
	if err == nil && u.Host != "" {
		return strings.ToLower(u.Host)
	}
	return "default"
}

func settings(ctx context.Context, db *gorm.DB) (Settings, error) {
	var row struct{ Automation json.RawMessage }
	out := Settings{}
	if err := db.WithContext(ctx).Raw("SELECT automation FROM seo_settings LIMIT 1").Scan(&row).Error; err != nil {
		return out, err
	}
	err := json.Unmarshal(row.Automation, &out)
	return out, err
}

func exempt(action string) bool {
	return action == "fact_fix" || action == "withdraw" || action == "compensate"
}

func Lock(ctx context.Context, tx *gorm.DB, site string) error {
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:breakers:"+site).Error
}

func Check(ctx context.Context, tx *gorm.DB, site, action string, paid bool) error {
	if exempt(action) {
		return nil
	}
	if err := Lock(ctx, tx, site); err != nil {
		return err
	}
	var blocked int64
	err := tx.WithContext(ctx).Raw(`SELECT count(*) FROM seo_breakers WHERE site=? AND closed_at IS NULL AND (retry_at IS NULL OR retry_at>clock_timestamp()) AND ((kind='technical' AND scope IN ('*',?)) OR (kind='business' AND ? IN ('listing','snippet') AND scope IN ('*',?)) OR (kind='budget' AND ?))`, site, action, action, action, paid).Scan(&blocked).Error
	if err != nil {
		return err
	}
	if blocked > 0 {
		return ErrFrozen
	}
	return nil
}

// Reserve must share the business transaction. An exact entity already changed
// today is free, irrespective of which token or tool performed the first write.
func Reserve(ctx context.Context, tx *gorm.DB, site, action, entity, requestID string) error {
	if site == "" || entity == "" || requestID == "" {
		return ErrLimit
	}
	if err := Check(ctx, tx, site, action, false); err != nil {
		return err
	}
	limits, err := settings(ctx, tx)
	if err != nil {
		return err
	}
	limit, ok := limits.DailyEntities[action]
	if !ok || limit < 0 {
		return ErrLimit
	}
	var day string
	if err := tx.Raw("SELECT (clock_timestamp() AT TIME ZONE 'UTC')::date::text").Scan(&day).Error; err != nil {
		return err
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:quota:"+site+":"+day+":"+action).Error; err != nil {
		return err
	}
	var existing, count int64
	if err := tx.Raw("SELECT count(*) FROM seo_daily_entities WHERE site=? AND day=?::date AND action=? AND entity_key=?", site, day, action, entity).Scan(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	if err := tx.Raw("SELECT count(*) FROM seo_daily_entities WHERE site=? AND day=?::date AND action=?", site, day, action).Scan(&count).Error; err != nil {
		return err
	}
	if count >= int64(limit) {
		return ErrLimit
	}
	return tx.Exec("INSERT INTO seo_daily_entities(site,day,action,entity_key,first_request_id) VALUES(?,?::date,?,?,?) ON CONFLICT DO NOTHING", site, day, action, entity, requestID).Error
}

// Rebind moves reservations from a provisional creation key to the entity it
// created, so creating and then publishing a new entity uses one daily slot.
func Rebind(ctx context.Context, tx *gorm.DB, site, oldKey, newKey string) error {
	if oldKey == "" || oldKey == newKey {
		return nil
	}
	if err := tx.WithContext(ctx).Exec(`UPDATE seo_daily_entities d SET entity_key=? WHERE site=? AND entity_key=? AND NOT EXISTS(SELECT 1 FROM seo_daily_entities x WHERE x.site=d.site AND x.day=d.day AND x.action=d.action AND x.entity_key=?)`, newKey, site, oldKey, newKey).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec("DELETE FROM seo_daily_entities WHERE site=? AND entity_key=?", site, oldKey).Error
}

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(?:\.([0-9]{1,6}))?$`)

func Micros(value string) (int64, error) {
	match := decimal.FindStringSubmatch(value)
	if match == nil {
		return 0, ErrReceipt
	}
	whole, _ := strconv.ParseInt(match[1], 10, 64)
	fraction, _ := strconv.ParseInt(match[2]+strings.Repeat("0", 6-len(match[2])), 10, 64)
	return whole*1000000 + fraction, nil
}

type Receipt struct {
	VendorTaskID, CostUSD string
	IncurredAt            time.Time
}

// Record never rejects a legitimate over-budget invoice: it records the cost
// and opens the breaker for future paid research in the same transaction.
func Record(ctx context.Context, tx *gorm.DB, site string, receipt Receipt) error {
	if err := Lock(ctx, tx, site); err != nil {
		return err
	}
	cost, err := Micros(receipt.CostUSD)
	if err != nil || receipt.VendorTaskID == "" || len(receipt.VendorTaskID) > 200 || strings.ContainsAny(receipt.VendorTaskID, "\r\n\x00") || receipt.IncurredAt.IsZero() {
		return ErrReceipt
	}
	var now time.Time
	if err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
		return err
	}
	if receipt.IncurredAt.After(now.Add(5 * time.Minute)) {
		return ErrReceipt
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "seo:receipts:"+site).Error; err != nil {
		return err
	}
	var row struct {
		Site       string
		CostMicros int64
		IncurredAt time.Time
	}
	r := tx.Raw("SELECT site,cost_micros,incurred_at FROM seo_cost_receipts WHERE provider='openseo' AND vendor_task_id=?", receipt.VendorTaskID).Scan(&row)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected > 0 {
		if row.Site != site || row.CostMicros != cost {
			return ErrReceipt
		}
	} else if err := tx.Exec("INSERT INTO seo_cost_receipts(site,provider,vendor_task_id,cost_micros,incurred_at) VALUES(?,'openseo',?,?,?)", site, receipt.VendorTaskID, cost, receipt.IncurredAt).Error; err != nil {
		return err
	}
	limits, err := settings(ctx, tx)
	if err != nil {
		return err
	}
	var total struct{ Daily, Monthly int64 }
	err = tx.Raw(`SELECT coalesce(sum(cost_micros) FILTER(WHERE incurred_at>=date_trunc('day',?::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),0) daily,coalesce(sum(cost_micros),0) monthly FROM seo_cost_receipts WHERE site=? AND incurred_at>=date_trunc('month',?::timestamptz AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, now, site, now).Scan(&total).Error
	if err != nil {
		return err
	}
	if total.Monthly >= limits.MonthlyMicros {
		retry := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		return Open(ctx, tx, site, "budget", "paid_research", "monthly_receipts", total, &retry)
	}
	if total.Daily >= limits.DailyMicros {
		retry := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		return Open(ctx, tx, site, "budget", "paid_research", "daily_receipts", total, &retry)
	}
	return nil
}

func Open(ctx context.Context, tx *gorm.DB, site, kind, scope, reason string, evidence any, retry *time.Time) error {
	if err := Lock(ctx, tx, site); err != nil {
		return err
	}
	if kind != "technical" && kind != "business" && kind != "budget" {
		return ErrFrozen
	}
	if site == "" || scope == "" || reason == "" {
		return ErrFrozen
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec(`INSERT INTO seo_breakers(site,kind,scope,reason,evidence,retry_at) VALUES(?,?,?,?,?::jsonb,?) ON CONFLICT(site,kind,scope) DO UPDATE SET reason=EXCLUDED.reason,evidence=EXCLUDED.evidence,retry_at=EXCLUDED.retry_at,closed_at=NULL,opened_at=clock_timestamp()`, site, kind, scope, reason, string(raw), retry).Error
}

// Business assessment compares adjacent complete 28-day windows only. A
// truncated or missing day fails closed for measurement, never fabricates loss.
func AssessBusiness(ctx context.Context, tx *gorm.DB, site string) error {
	if err := Lock(ctx, tx, site); err != nil {
		return err
	}
	limits, err := settings(ctx, tx)
	if err != nil {
		return err
	}
	var stats struct {
		Days                                                 int
		OldClicks, NewClicks, OldImpressions, NewImpressions int64
	}
	err = tx.WithContext(ctx).Raw(`WITH watermark AS (SELECT complete_through d FROM seo_data_watermarks WHERE source='gsc_page'), valid AS (SELECT count(*) days FROM seo_ingest_days,watermark WHERE source='gsc_page' AND day BETWEEN d-55 AND d AND temporal_complete AND retrieval_complete), totals AS (SELECT coalesce(sum(clicks) FILTER(WHERE day<d-27),0) old_clicks,coalesce(sum(clicks) FILTER(WHERE day>=d-27),0) new_clicks,coalesce(sum(impressions) FILTER(WHERE day<d-27),0) old_impressions,coalesce(sum(impressions) FILTER(WHERE day>=d-27),0) new_impressions FROM seo_gsc_page_daily,watermark WHERE day BETWEEN d-55 AND d) SELECT * FROM valid,totals`).Scan(&stats).Error
	if err != nil {
		return err
	}
	if stats.Days != 56 || stats.OldClicks < 100 || stats.OldImpressions < 1000 {
		return nil
	}
	if limits.BusinessDropRatio <= 0 || limits.BusinessDropRatio >= 1 {
		return ErrFrozen
	}
	if float64(stats.NewClicks) < float64(stats.OldClicks)*limits.BusinessDropRatio && float64(stats.NewImpressions) < float64(stats.OldImpressions)*limits.BusinessDropRatio {
		return Open(ctx, tx, site, "business", "*", "complete_window_decline", stats, nil)
	}
	return tx.Exec("UPDATE seo_breakers SET closed_at=clock_timestamp() WHERE site=? AND kind='business' AND reason='complete_window_decline' AND closed_at IS NULL", site).Error
}

func AssessTechnical(ctx context.Context, tx *gorm.DB, site string) error {
	if err := Lock(ctx, tx, site); err != nil {
		return err
	}
	var issues int64
	if err := tx.Raw("SELECT count(*) FROM seo_audit_issues WHERE severity='p0' AND resolved_at IS NULL").Scan(&issues).Error; err != nil {
		return err
	}
	if issues > 0 {
		return Open(ctx, tx, site, "technical", "*", "audit_p0", map[string]any{"unresolved": issues, "layers": []string{"origin", "edge"}}, nil)
	}
	return tx.Exec("UPDATE seo_breakers SET closed_at=clock_timestamp() WHERE site=? AND kind='technical' AND reason='audit_p0' AND closed_at IS NULL", site).Error
}

// ProviderRefused records an OpenSEO reservation rejection without making a
// paid call. The scheduling layer uses this signal only for research work.
func ProviderRefused(ctx context.Context, tx *gorm.DB, site, receiptID string) error {
	var retry time.Time
	if err := tx.Raw("SELECT (date_trunc('day',clock_timestamp() AT TIME ZONE 'UTC')+interval '1 day') AT TIME ZONE 'UTC'").Scan(&retry).Error; err != nil {
		return err
	}
	return Open(ctx, tx, site, "budget", "paid_research", "openseo_reservation_rejected", map[string]any{"receiptId": receiptID}, &retry)
}
