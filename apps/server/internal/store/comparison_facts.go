package store

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/facts"
)

func (s *Store) validateMigrationTarget(ctx context.Context, id int64, r facts.Record) error {
	if r.Kind != "migration" {
		return nil
	}
	var guide facts.Migration
	if json.Unmarshal(r.Value, &guide) != nil {
		return facts.ErrInvalid
	}
	var exists bool
	if err := s.DB.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM package_alternatives a JOIN packages p ON p.id=a.alternative_id WHERE a.package_id=? AND a.alternative_id=? AND p.removed_at IS NULL)`, id, guide.TargetPackageID).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return facts.ErrInvalid
	}
	var bases []string
	if err := s.DB.WithContext(ctx).Raw(`SELECT url FROM (SELECT p.homepage url FROM packages p WHERE p.id IN (?,?) UNION SELECT m.repo_url FROM package_meta m WHERE m.package_id IN (?,?)) b WHERE url IS NOT NULL`, id, guide.TargetPackageID, id, guide.TargetPackageID).Scan(&bases).Error; err != nil {
		return err
	}
	for _, step := range guide.Steps {
		if !facts.OfficialDocument(step.Evidence.SourceURL, bases) {
			return facts.ErrInvalid
		}
	}
	return nil
}
