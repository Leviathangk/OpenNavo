package store

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/facts"
)

type CollectionCriteria struct {
	Locale   string      `json:"locale"`
	Criteria string      `json:"criteria"`
	Evidence facts.Proof `json:"evidence"`
}

func (s *Store) CollectionCriteria(ctx context.Context, slug, locale string) (*CollectionCriteria, error) {
	var row struct {
		Locale, Criteria string
		Evidence         []byte
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT x.locale,x.criteria,x.evidence FROM collection_setup_criteria x JOIN collections c ON c.id=x.collection_id WHERE c.slug=? AND x.locale IN (?,'en-US') ORDER BY(x.locale=?) DESC LIMIT 1`, slug, locale, locale).Scan(&row).Error
	if err != nil || row.Locale == "" {
		return nil, err
	}
	out := &CollectionCriteria{Locale: row.Locale, Criteria: row.Criteria}
	err = json.Unmarshal(row.Evidence, &out.Evidence)
	return out, err
}
func (s *Store) SaveCollectionCriteria(ctx context.Context, id int64, v CollectionCriteria) error {
	proof, err := json.Marshal(v.Evidence)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec(`INSERT INTO collection_setup_criteria(collection_id,locale,criteria,evidence) VALUES(?,?,?,?::jsonb) ON CONFLICT(collection_id,locale) DO UPDATE SET criteria=EXCLUDED.criteria,evidence=EXCLUDED.evidence`, id, v.Locale, v.Criteria, string(proof)).Error
}
