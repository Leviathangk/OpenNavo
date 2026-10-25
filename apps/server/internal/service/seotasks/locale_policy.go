package seotasks

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/seo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Only tasks whose sole purpose is search visibility follow the indexing switch.
// Translation, aliases, factual corrections and operational alerts serve users too.
var searchOnlyKinds = []string{"discover_fix", "snippet_template"}

func SearchLocaleAllowed(kind, locale string, open []string) bool {
	for _, restricted := range searchOnlyKinds {
		if kind == restricted && locale != "" {
			return seo.LocaleOpen(open, locale)
		}
	}
	return true
}

func OpenLocales(ctx context.Context, db *gorm.DB) ([]string, error) {
	var raw string
	if err := db.WithContext(ctx).Raw("SELECT coalesce(settings->'pages'->'openLocales','null'::jsonb)::text FROM seo_settings WHERE id=true").Scan(&raw).Error; err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var open []string
	err := json.Unmarshal([]byte(raw), &open)
	return open, err
}

// Pause ready historical tasks without spending attempts; reopening a language
// resumes only work paused by this policy. Running leases finish normally.
func reconcileSearchLocales(ctx context.Context, tx *gorm.DB, open []string) error {
	base := tx.WithContext(ctx).Model(&Task{}).Where("kind IN ? AND locale IS NOT NULL", searchOnlyKinds)
	closed := base.Session(&gorm.Session{})
	if open != nil {
		closed = closed.Where("locale NOT IN ?", append(append([]string{}, open...), ""))
		if err := updatePolicyBatch(closed.Where("state='open' OR (state='deferred' AND next_retry_at<=clock_timestamp()) OR (state='leased' AND lease_until<=clock_timestamp())"), map[string]any{
			"state": "deferred", "next_retry_at": nil, "lease_owner": nil, "lease_until": nil,
			"result":     gorm.Expr("coalesce(result,'{}'::jsonb)||'{\"localePolicyPause\":\"locale_closed\"}'::jsonb"),
			"updated_at": gorm.Expr("clock_timestamp()"),
		}); err != nil {
			return err
		}
	}
	resumed := base.Session(&gorm.Session{}).Where("state='deferred' AND result->>'localePolicyPause'='locale_closed'")
	if open != nil {
		resumed = resumed.Where("locale IN ?", open)
	}
	return updatePolicyBatch(resumed, map[string]any{"state": "open", "next_retry_at": nil, "result": gorm.Expr("result-'localePolicyPause'"), "updated_at": gorm.Expr("clock_timestamp()")})
}

// Claimers must not wait on each other's task locks while reconciling policy.
func updatePolicyBatch(query *gorm.DB, updates map[string]any) error {
	var rows []Task
	if err := query.Session(&gorm.Session{}).Select("id").Order("id").Limit(1000).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return query.Where("id IN ?", ids).Updates(updates).Error
}
