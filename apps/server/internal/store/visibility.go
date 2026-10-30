package store

import (
	"context"

	"gorm.io/gorm"
)

func (s *Store) VisibilityGeneration(ctx context.Context) (int64, error) {
	var generation int64
	err := s.DB.WithContext(ctx).Raw("SELECT generation FROM catalog_visibility WHERE id=true").Scan(&generation).Error
	return generation, err
}

type SnapshotVisibility struct {
	RevocationGeneration int64
	SnapshotRevocation   int64
}

func (s *Store) SnapshotVisibility(ctx context.Context) (SnapshotVisibility, error) {
	var state SnapshotVisibility
	err := s.DB.WithContext(ctx).Raw("SELECT revocation_generation,snapshot_revocation FROM catalog_visibility WHERE id=true").Scan(&state).Error
	return state, err
}

func (s *Store) CompleteSnapshotRevocation(ctx context.Context, generation int64, current SnapshotRecord) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM catalog_snapshots WHERE storage_key<>?", current.StorageKey).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM app_config WHERE (key LIKE 'catalog.snapshotManifest:%' OR key LIKE 'catalog.snapshotModernBase:%') AND key NOT IN (?,?)`, "catalog.snapshotManifest:"+current.SHA256, "catalog.snapshotModernBase:"+current.SHA256).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE catalog_visibility SET snapshot_revocation=GREATEST(snapshot_revocation,?) WHERE id=true", generation).Error
	})
}
