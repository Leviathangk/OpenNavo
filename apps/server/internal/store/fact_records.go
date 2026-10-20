package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"gorm.io/gorm"
)

func (s *Store) FactRecords(ctx context.Context, id int64) ([]facts.Record, error) {
	var rows []struct {
		ID              int64
		Kind, Key       string
		Value, Evidence []byte
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT id,kind,key,value,evidence FROM package_fact_records WHERE package_id=? ORDER BY kind,key", id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]facts.Record, 0, len(rows))
	for _, row := range rows {
		record := facts.Record{ID: row.ID, Kind: row.Kind, Key: row.Key, Value: row.Value}
		if err := json.Unmarshal(row.Evidence, &record.Evidence); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *Store) UpsertFactRecords(ctx context.Context, id int64, records []facts.Record, now time.Time) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		var eligible bool
		if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM packages WHERE id=? AND kind IN ('cask','link') AND removed_at IS NULL)", id).Scan(&eligible).Error; err != nil {
			return err
		}
		if !eligible {
			return gorm.ErrRecordNotFound
		}
		for _, r := range records {
			if err := facts.ValidateRecord(r, now); err != nil {
				return err
			}
			if err := (&Store{DB: tx, Redis: s.Redis}).validateMigrationTarget(ctx, id, r); err != nil {
				return err
			}
			proof, err := json.Marshal(r.Evidence)
			if err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,?,?,?::jsonb,?::jsonb)
    ON CONFLICT(package_id,kind,key) DO UPDATE SET value=EXCLUDED.value,evidence=EXCLUDED.evidence,updated_at=now()`, id, r.Kind, r.Key, string(r.Value), string(proof)).Error; err != nil {
				return MapAdminError(err)
			}
		}
		var token string
		if err := tx.Raw("SELECT token FROM packages WHERE id=?", id).Scan(&token).Error; err != nil {
			return err
		}
		return sourceFactInvalidation(ctx, tx, id, token)
	})
}
