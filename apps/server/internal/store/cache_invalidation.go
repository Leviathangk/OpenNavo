package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"gorm.io/gorm"
)

func addCacheInvalidation(ctx context.Context, tx *gorm.DB, key string, patterns ...string) error {
	payload, err := json.Marshal(patterns)
	if err != nil {
		return err
	}
	// Updating a pending row also locks it against delivery: a later commit cannot
	// lose its notification when a concurrent dispatcher acknowledges an older one.
	return tx.WithContext(ctx).Exec(`INSERT INTO job_outbox(unique_key,job_type,payload) VALUES(?,'cache:invalidate',?::jsonb) ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=now()`, "cache:invalidate:"+key, string(payload)).Error
}

// QueueCacheInvalidation publishes patterns once tx commits. Rows share a key, so use one key per
// independent change (a later row with the same key replaces the patterns).
func QueueCacheInvalidation(ctx context.Context, tx *gorm.DB, key string, patterns ...string) error {
	return addCacheInvalidation(ctx, tx, key, patterns...)
}

// Merge scopes which share one delivery key without discarding a wider pending
// invalidation. The upsert retains the same row lock as ordinary delivery.
func mergeCacheInvalidation(ctx context.Context, tx *gorm.DB, key string, patterns ...string) error {
	payload, err := json.Marshal(patterns)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec(`INSERT INTO job_outbox(unique_key,job_type,payload) VALUES(?,'cache:invalidate',?::jsonb)
 ON CONFLICT(unique_key) DO UPDATE SET payload=(
 SELECT jsonb_agg(pattern ORDER BY pattern) FROM (
 SELECT DISTINCT jsonb_array_elements_text(job_outbox.payload || EXCLUDED.payload) AS pattern
 ) scopes),available_at=now()`, "cache:invalidate:"+key, string(payload)).Error
}

// BrowseRefreshPattern matches only the fresh marks of the browse directories (c:browse:<version>:<kind>:fresh), never
// their pages: a catalog or translation change leaves the old pages serving while one rebuild per kind replaces them,
// instead of making the next request wait for it. c:browse:* still clears the pages too.
const BrowseRefreshPattern = "c:browse:*:fresh"

func addCatalogInvalidation(ctx context.Context, tx *gorm.DB) error {
	return mergeCacheInvalidation(ctx, tx, "catalog", "c:pkg:*", "c:home:*", "c:rank:*", "c:cat:*", "c:sug:*", "c:dep:*", "c:related:*", BrowseRefreshPattern)
}

// DispatchCacheInvalidations acknowledges only successful publications. Keeping
// these separate from job delivery lets partial catalog syncs invalidate caches
// even when enqueueing a downstream job fails.
func (s *Store) DispatchCacheInvalidations(ctx context.Context) error {
	dueBefore := time.Now().UTC()
	for {
		count := 0
		err := s.WithTx(ctx, func(tx *gorm.DB) error {
			var rows []struct {
				ID      int64
				Payload string
			}
			if err := tx.WithContext(ctx).Raw(`SELECT id,payload::text FROM job_outbox WHERE job_type='cache:invalidate' AND available_at<=? ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED`, dueBefore).Scan(&rows).Error; err != nil {
				return fmt.Errorf("load cache invalidations: %w", err)
			}
			count = len(rows)
			if count == 0 {
				return nil
			}
			if s.Redis == nil {
				return errors.New("cache invalidation requires Redis")
			}
			patterns := []string{}
			ids := make([]int64, 0, len(rows))
			for _, row := range rows {
				var pending []string
				if err := json.Unmarshal([]byte(row.Payload), &pending); err != nil {
					return fmt.Errorf("decode cache invalidation: %w", err)
				}
				patterns = append(patterns, pending...)
				ids = append(ids, row.ID)
			}
			if err := cache.PublishInvalidation(ctx, s.Redis, patterns...); err != nil {
				return fmt.Errorf("publish cache invalidations: %w", err)
			}
			return tx.WithContext(ctx).Exec("DELETE FROM job_outbox WHERE id IN ?", ids).Error
		})
		if err != nil || count == 0 {
			return err
		}
	}
}
