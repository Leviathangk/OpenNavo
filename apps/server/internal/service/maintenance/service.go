package maintenance

import (
	"context"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/opennavo/opennavo/server/internal/storage"
	"github.com/opennavo/opennavo/server/internal/store"
)

type Objects interface {
	Delete(context.Context, string) error
}
type S3Objects struct{ Client *storage.Client }

func (s S3Objects) Delete(ctx context.Context, key string) error {
	return s.Client.S3.RemoveObject(ctx, s.Client.Bucket, key, minio.RemoveObjectOptions{})
}

type Service struct {
	Store   *store.Store
	Objects Objects
	Now     func() time.Time
}

func VariantKeys(a store.Asset) []string {
	out := []string{a.StorageKey}
	seen := map[string]bool{a.StorageKey: true}
	var files []store.AssetFile
	if json.Unmarshal(a.Files, &files) == nil && len(files) > 0 {
		hasSource := false
		for _, f := range files {
			if f.StorageKey != "" && (f.Role == "normalized" || f.Role == "original") {
				hasSource = true
			}
			if f.StorageKey != "" && !seen[f.StorageKey] {
				out = append(out, f.StorageKey)
				seen[f.StorageKey] = true
			}
		}
		// A backfilled legacy manifest may contain only new WebP renditions.
		// Keep inferring its pre-manifest PNG siblings until source entries exist.
		if hasSource {
			return out
		}
	}
	if a.MIME != "image/png" {
		return out
	}
	suffix := "-" + strconv.Itoa(a.Width) + ".png"
	if !strings.HasSuffix(path.Base(a.StorageKey), suffix) {
		return out
	}
	base := strings.TrimSuffix(a.StorageKey, suffix)
	widths := []int{1280, 640}
	if a.Kind == "icon" {
		widths = []int{256, 512}
	}
	for _, width := range widths {
		key := base + "-" + strconv.Itoa(width) + ".png"
		if !seen[key] {
			out = append(out, key)
			seen[key] = true
		}
	}
	return out
}
func (s *Service) Run(ctx context.Context) (map[string]any, error) {
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	changes, err := s.Store.PruneCatalogChanges(ctx, now.AddDate(0, 0, -14))
	if err != nil {
		return nil, err
	}
	// Bound each transaction and each daily run; a later run continues any backlog.
	var notifications int64
	for range 100 {
		count, err := s.Store.PruneURLNotifications(ctx, now.Add(-store.URLNotificationRetention), store.URLNotificationPruneBatch)
		if err != nil {
			return nil, err
		}
		notifications += count
		if count == 0 {
			break
		}
	}
	logs, err := s.Store.PruneLogs(ctx, now.AddDate(0, 0, -90))
	if err != nil {
		return nil, err
	}
	// Intent switches and overview overrides have no foreign key this release; rows of deleted apps go here.
	orphans, err := s.Store.PruneOrphanPackagePageSettings(ctx)
	if err != nil {
		return nil, err
	}
	assets, err := s.Store.PruneAssets(ctx, now.AddDate(0, 0, -30), func(ctx context.Context, a store.Asset) error {
		for _, key := range VariantKeys(a) {
			if err := s.Objects.Delete(ctx, key); err != nil {
				return err
			}
		}
		return nil
	})
	return map[string]any{"catalogChanges": changes, "urlNotifications": notifications, "searchQueries": logs["search_queries"], "syncRuns": logs["sync_runs"], "agentWriteControls": logs["agent_write_controls"],
		"intentPageOrphans": orphans["package_intent_pages"], "seoOverrideOrphans": orphans["package_seo_overrides"], "assets": assets}, err
}
