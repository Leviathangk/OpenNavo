package management

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// Report filter for apps kept in by a manual override; the other values are seo reasons.
const reasonForcedIndex = "forced_index"

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func indexStatusView(row store.IndexRow, policy seo.Policy) map[string]any {
	d := seo.Decide(row.Facts(), policy)
	var overrideAt any
	if row.OverrideAt != nil {
		overrideAt = row.OverrideAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"eligible":       d.Eligible,
		"reason":         nullableText(d.EligibleReason),
		"indexable":      d.Indexable,
		"enforced":       d.Enforced,
		"locales":        d.Locales,
		"override":       row.Override,
		"overrideReason": row.OverrideReason,
		"overrideAt":     overrideAt,
	}
}

type indexOverride struct {
	Override string  `json:"override"`
	Reason   *string `json:"reason"`
}

// decodeIndexOverride requires a reason for index and noindex; auto clears it.
func decodeIndexOverride(body map[string]any) (string, *string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", nil, domain.Validation()
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var in indexOverride
	if err := decoder.Decode(&in); err != nil || !slices.Contains([]string{seo.OverrideAuto, seo.OverrideIndex, seo.OverrideNoindex}, in.Override) {
		return "", nil, domain.Validation()
	}
	if in.Override == seo.OverrideAuto {
		return in.Override, nil, nil
	}
	if in.Reason == nil {
		return "", nil, domain.Validation()
	}
	reason := strings.TrimSpace(*in.Reason)
	if !validLabel(reason, 500) {
		return "", nil, domain.Validation()
	}
	return in.Override, &reason, nil
}

func (s *Service) packageIndexStatus(ctx context.Context, op string, in Input) (any, error) {
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if op == "GetPackageIndexStatus" {
		row, found, err := s.Store.IndexRowByID(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fail(domain.CodeNotFound)
		}
		return indexStatusView(row, policy), nil
	}
	override, reason, err := decodeIndexOverride(in.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		before, found, err := st.IndexRowByID(ctx, in.ID)
		if err != nil {
			return err
		}
		if !found {
			return fail(domain.CodeNotFound)
		}
		if err := tx.WithContext(ctx).Exec("INSERT INTO package_meta(package_id) VALUES(?) ON CONFLICT DO NOTHING", in.ID).Error; err != nil {
			return err
		}
		var at any
		if override != seo.OverrideAuto {
			at = s.now()
		}
		if err := tx.WithContext(ctx).Table("package_meta").Where("package_id=?", in.ID).Updates(map[string]any{
			"index_override": override, "index_override_reason": reason, "index_override_at": at,
		}).Error; err != nil {
			return err
		}
		after, _, err := st.IndexRowByID(ctx, in.ID)
		if err != nil {
			return err
		}
		result = indexStatusView(after, policy)
		// Refresh API data, then let the URL worker refresh HTML and CDN pages.
		if err := st.AppendContentChange(ctx, in.ID); err != nil {
			return err
		}
		if err := store.QueueCacheInvalidation(ctx, tx, "seo-index:"+before.Token, cache.PackageDetailPrefix+before.Kind+":"+before.Token+":*"); err != nil {
			return err
		}
		return store.Audit(ctx, tx, in.Actor, "SetPackageIndexStatus", "package", fmt.Sprint(in.ID), indexStatusView(before, policy), result)
	})
	return result, store.MapAdminError(err)
}

// indexReport lists apps the rule keeps out (or an override changes) with counts per reason, so
// the impact of enforcing can be reviewed in shadow mode first.
func (s *Service) indexReport(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	filter, _ := in.Params["reason"].(string)
	if filter != "" && !slices.Contains([]string{seo.ReasonHidden, seo.ReasonForcedNoindex, seo.ReasonUnavailable, seo.ReasonMissingSummary, seo.ReasonThin, reasonForcedIndex}, filter) {
		return nil, domain.Validation()
	}
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.IndexReportRows(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{seo.ReasonHidden: 0, seo.ReasonForcedNoindex: 0, seo.ReasonUnavailable: 0, seo.ReasonMissingSummary: 0, seo.ReasonThin: 0, reasonForcedIndex: 0}
	records := []map[string]any{}
	total := 0
	start := (current - 1) * size
	for _, row := range rows {
		d := seo.Decide(row.Facts(), policy)
		key := d.EligibleReason
		if d.Eligible && row.Override == seo.OverrideIndex {
			key = reasonForcedIndex
		}
		if key == "" {
			continue
		}
		counts[key]++
		if filter != "" && filter != key {
			continue
		}
		total++
		if total <= start || len(records) >= size {
			continue
		}
		records = append(records, map[string]any{
			"id": row.ID, "token": row.Token, "displayName": row.DisplayName, "firstSeenAt": row.FirstSeenAt.UTC().Format(time.RFC3339),
			"eligible": d.Eligible, "reason": nullableText(d.EligibleReason), "enforced": d.Enforced,
			"override": row.Override, "overrideReason": row.OverrideReason,
		})
	}
	return map[string]any{
		"records": records, "current": current, "size": size, "total": total,
		"translationImpact": translationImpact(rows, policy, current, size),
		"counts": map[string]int{
			"hidden": counts[seo.ReasonHidden], "forcedNoindex": counts[seo.ReasonForcedNoindex], "unavailable": counts[seo.ReasonUnavailable],
			"missingSummary": counts[seo.ReasonMissingSummary], "thin": counts[seo.ReasonThin], "forcedIndex": counts[reasonForcedIndex],
		},
	}, nil
}
