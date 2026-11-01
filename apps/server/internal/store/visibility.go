package store

import (
	"context"
	"sort"

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

// PrivatePackageTokens lists the tokens of packages that are private and still in the catalog.
// Revocation uses it for its self-check. Removed packages are excluded: they never enter a bundle,
// so keeping them would only make the check refuse a token that cannot appear. The order is fixed
// so the set is reproducible for logs and tests.
func (s *Store) PrivatePackageTokens(ctx context.Context) ([]string, error) {
	tokens := []string{}
	err := s.DB.WithContext(ctx).Raw(`SELECT p.token FROM packages p JOIN package_meta m ON m.package_id=p.id WHERE m.private AND p.removed_at IS NULL ORDER BY p.token`).Scan(&tokens).Error
	sort.Strings(tokens)
	return tokens, err
}
