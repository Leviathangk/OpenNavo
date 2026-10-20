package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
)

// PopulateTroubleshooting uses the same issue validator and proof expiry rules as
// facts.issues, but never counts the public display's English fallback.
func (s *Store) PopulateTroubleshooting(ctx context.Context, rows []IndexRow, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	counts := map[int64]map[string]int{}
	for i := range rows {
		ids[i] = rows[i].ID
		counts[rows[i].ID] = map[string]int{}
		rows[i].TroubleshootingIssues = counts[rows[i].ID]
	}
	var records []struct {
		PackageID       int64
		Value, Evidence []byte
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT package_id,value,evidence FROM package_fact_records WHERE package_id IN ? AND kind='issue'`, ids).Scan(&records).Error; err != nil {
		return err
	}
	for _, r := range records {
		var issue facts.Issue
		var proof facts.Proof
		if json.Unmarshal(r.Value, &issue) != nil || issue.Validate() != nil || json.Unmarshal(r.Evidence, &proof) != nil || proof.At(now).Status != "verified" {
			continue
		}
		counts[r.PackageID][issue.Locale]++
	}
	return nil
}

// Lightweight overview and issue facts for decisions refreshed on detail cache hits.
func (s *Store) TroubleshootingIndexRow(ctx context.Context, token string, now time.Time) (IndexRow, bool, error) {
	rows, err := s.indexRowsQuery(ctx, indexOverviewSelect, "p.token=?", "", token)
	if err != nil || len(rows) == 0 {
		return IndexRow{}, false, err
	}
	if err := s.PopulateTroubleshooting(ctx, rows, now); err != nil {
		return IndexRow{}, false, err
	}
	return rows[0], true, nil
}
