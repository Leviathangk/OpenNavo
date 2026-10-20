package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"gorm.io/gorm"
)

type SecuritySubject struct {
	PackageID       int64
	Token, Version  string
	Value, Evidence []byte
	CheckedAt       *time.Time
}

func (s *Store) SecuritySubjects(ctx context.Context) ([]SecuritySubject, error) {
	rows := []SecuritySubject{}
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id AS package_id,p.token,p.version,f.value,f.evidence,c.checked_at
 FROM package_fact_records f JOIN packages p ON p.id=f.package_id LEFT JOIN package_meta m ON m.package_id=p.id
 LEFT JOIN package_cve_checks c ON c.package_id=p.id AND c.product_cpe=f.value->>'productCpe'
 WHERE f.kind='security' AND coalesce(f.value->>'productCpe','')<>'' AND f.value->>'appVersion'=p.version AND f.evidence->>'status'='verified' AND (f.evidence->>'validUntil')::timestamptz>now() AND p.kind IN ('cask','link') AND p.removed_at IS NULL AND NOT coalesce(m.hidden,false)
 ORDER BY c.checked_at NULLS FIRST,p.id LIMIT 100`).Scan(&rows).Error
	return rows, err
}
func (s *Store) SaveCVECheck(ctx context.Context, id int64, token, cpe, version string, matches []facts.Vulnerability, now time.Time) error {
	data, err := json.Marshal(matches)
	if err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO package_cve_checks(package_id,product_cpe,app_version,matches,checked_at,valid_until) VALUES(?,?,?,?::jsonb,?,?)
  ON CONFLICT(package_id,product_cpe) DO UPDATE SET app_version=EXCLUDED.app_version,matches=EXCLUDED.matches,checked_at=EXCLUDED.checked_at,valid_until=EXCLUDED.valid_until`, id, cpe, version, string(data), now, now.Add(24*time.Hour)).Error; err != nil {
			return err
		}
		return sourceFactInvalidation(ctx, tx, id, token)
	})
}
func (s *Store) PublicCVEs(ctx context.Context, id int64, cpe, version string, now time.Time) ([]map[string]any, error) {
	var row struct {
		Matches               []byte
		CheckedAt, ValidUntil time.Time
	}
	err := s.DB.WithContext(ctx).Raw("SELECT matches,checked_at,valid_until FROM package_cve_checks WHERE package_id=? AND product_cpe=? AND app_version=? AND valid_until>?", id, cpe, version, now).Scan(&row).Error
	out := []map[string]any{}
	if err != nil || len(row.Matches) == 0 {
		return out, err
	}
	var matches []facts.Vulnerability
	if err = json.Unmarshal(row.Matches, &matches); err != nil {
		return nil, err
	}
	for _, v := range matches {
		if v.ProductCPE == cpe && v.AppVersion == version {
			out = append(out, map[string]any{"id": v.ID, "productCpe": cpe, "appVersion": version, "sourceUrl": v.SourceURL, "evidence": facts.Proof{SourceURL: v.SourceURL, CheckedAt: &row.CheckedAt, ValidUntil: &row.ValidUntil, Status: "verified"}})
		}
	}
	return out, nil
}
