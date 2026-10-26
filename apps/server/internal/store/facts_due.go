package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
)

// FactRecordKinds are the record kinds facts.ValidateRecord accepts.
var FactRecordKinds = []string{"integration", "issue", "compatibility", "attribute", "migration", "security", "price"}

// DueFactRecord is a verified fact record of a live app whose evidence has expired or expires soon.
type DueFactRecord struct {
	ID, PackageID      int64
	PackageKind, Token string
	Name               string
	Kind, Key          string
	Value              json.RawMessage
	Evidence           facts.Proof
	Popularity         float64
	Rank30d            *int
	Installs30d        int
}

// DueFactRecords pages the records stored as verified whose validUntil is before until, the most popular
// apps first and then the earliest expiry. kind filters when set. Records without a validUntil never count
// as verified (facts.Proof.At), so they are not listed.
func (s *Store) DueFactRecords(ctx context.Context, until time.Time, kind string, current, size int) ([]DueFactRecord, int64, error) {
	if kind != "" && !slices.Contains(FactRecordKinds, kind) {
		return nil, 0, domain.Rejected("record_kind_invalid:kind")
	}
	where := `f.evidence->>'status'='verified' AND jsonb_typeof(f.evidence->'validUntil')='string' AND (f.evidence->>'validUntil')::timestamptz<? AND p.kind IN ('cask','link') AND p.removed_at IS NULL`
	args := []any{until}
	if kind != "" {
		where += " AND f.kind=?"
		args = append(args, kind)
	}
	from := " FROM package_fact_records f JOIN packages p ON p.id=f.package_id WHERE " + where
	var total int64
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*)"+from, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []DueFactRecord{}
	offset, found := domain.PageOffset(current, size, total)
	if !found {
		return rows, total, nil
	}
	var scanned []struct {
		ID, PackageID      int64
		PackageKind, Token string
		Name               string
		Kind, Key          string
		Value, Evidence    string
		Popularity         float64
		Rank30d            *int
		Installs30d        int
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT f.id,f.package_id,p.kind AS package_kind,p.token,p.name,f.kind,f.key,f.value::text AS value,f.evidence::text AS evidence,
 p.popularity,p.rank_30d,p.installs_30d`+from+` ORDER BY p.popularity DESC,(f.evidence->>'validUntil')::timestamptz,f.id LIMIT ? OFFSET ?`, append(args, size, offset)...).Scan(&scanned).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range scanned {
		record := DueFactRecord{ID: row.ID, PackageID: row.PackageID, PackageKind: row.PackageKind, Token: row.Token, Name: row.Name,
			Kind: row.Kind, Key: row.Key, Value: json.RawMessage(row.Value), Popularity: row.Popularity, Rank30d: row.Rank30d, Installs30d: row.Installs30d}
		if err := json.Unmarshal([]byte(row.Evidence), &record.Evidence); err != nil {
			return nil, 0, err
		}
		rows = append(rows, record)
	}
	return rows, total, nil
}
