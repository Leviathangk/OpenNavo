package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/facts"
	"gorm.io/gorm"
)

const (
	homebrewConfirmationInterval  = 6 * time.Hour
	homebrewConfirmationBatchSize = 500
)

// A confirmation applies only to the exact definition successfully observed.
// It cannot refresh a concurrently replaced definition or revive a removed cask.
type HomebrewConfirmation struct {
	Token   string `json:"token"`
	Tap     string `json:"tap"`
	RawHash string `json:"raw_hash"`
}

func (s *Store) ConfirmHomebrewDefinitions(ctx context.Context, observed []HomebrewConfirmation, at time.Time) error {
	for batch := range slices.Chunk(observed, homebrewConfirmationBatchSize) {
		if err := s.confirmHomebrewBatch(ctx, batch, at); err != nil {
			return err
		}
	}
	return nil
}

// Standalone confirmations commit each batch before taking the catalog lock
// again. When called by a catalog mutation, this joins its existing transaction
// so the replacement and its new confirmation remain atomic.
func (s *Store) confirmHomebrewBatch(ctx context.Context, observed []HomebrewConfirmation, at time.Time) error {
	raw, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		var refreshCache bool
		err := tx.WithContext(ctx).Raw(`WITH observed_packages AS MATERIALIZED (
 SELECT p.id,p.homebrew_checked_at FROM packages p,
 jsonb_to_recordset(?::jsonb) observed(token text,tap text,raw_hash text)
 WHERE p.kind='cask' AND p.removed_at IS NULL AND p.token=observed.token
 AND p.tap=observed.tap AND p.raw_hash=observed.raw_hash
	 AND (p.homebrew_checked_at IS NULL OR p.homebrew_checked_at<?)
 FOR UPDATE OF p), confirmed AS (
 UPDATE packages p SET homebrew_checked_at=? FROM observed_packages o WHERE p.id=o.id
 RETURNING o.homebrew_checked_at)
 SELECT COALESCE(bool_or(homebrew_checked_at IS NULL OR homebrew_checked_at<=?),false) FROM confirmed`, string(raw), at.Add(-homebrewConfirmationInterval), at, at.Add(-facts.StaleAfter+cache.PackageDetailTTL)).Scan(&refreshCache).Error
		if err != nil || !refreshCache {
			return err
		}
		// Fresh, unchanged facts keep their normal cache TTL. Unknown/stale or
		// soon-expiring evidence must refresh before the cached proof can expire.
		// A catalog change in the same transaction already covers this scope.
		return mergeCacheInvalidation(ctx, tx, "catalog", "c:pkg:*")
	})
}

// Freeze the outgoing definition's last observation only when its content is
// replaced. Updating historical snapshots on every unchanged sync would dirty
// page content dates; current versions read packages.homebrew_checked_at directly.
func freezeHomebrewVersionConfirmations(ctx context.Context, tx *gorm.DB, observed []HomebrewConfirmation) error {
	if len(observed) == 0 {
		return nil
	}
	raw, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Exec(`UPDATE package_version_requirements r SET checked_at=p.homebrew_checked_at
 FROM packages p JOIN jsonb_to_recordset(?::jsonb) observed(token text,tap text,raw_hash text)
 ON p.kind='cask' AND p.token=observed.token
 WHERE r.package_id=p.id AND p.homebrew_checked_at IS NOT NULL AND p.raw_hash IS DISTINCT FROM observed.raw_hash
 AND r.checked_at IS DISTINCT FROM p.homebrew_checked_at
 AND (r.app_version=p.version OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(p.dependencies->'platforms','[]')) v WHERE v->>'version'=r.app_version))`, string(raw)).Error; err != nil {
		return err
	}
	// A new definition must not inherit the old one's recent observation and
	// then be skipped by the refresh interval. The caller holds the catalog lock
	// and replaces/reconfirms it in this same transaction, so NULL is never public.
	// Match the upsert identity (kind/token), including a takeover from another tap.
	return tx.WithContext(ctx).Exec(`UPDATE packages p SET homebrew_checked_at=NULL
 FROM jsonb_to_recordset(?::jsonb) observed(token text,tap text,raw_hash text)
 WHERE p.kind='cask' AND p.token=observed.token
 AND p.raw_hash IS DISTINCT FROM observed.raw_hash AND p.homebrew_checked_at IS NOT NULL`, string(raw)).Error
}
