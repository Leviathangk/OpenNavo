package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/store"
)

type LinkCatalogItem struct {
	publicapi.CatalogItem
	BundleIDs []string            `json:"bundleIds"`
	Link      *publicapi.LinkInfo `json:"link"`
}
type CatalogLinks struct {
	ETag  string            `json:"etag"`
	Items []LinkCatalogItem `json:"items"`
}

func (s *Service) Links(ctx context.Context) (CatalogLinks, error) {
	result := CatalogLinks{Items: []LinkCatalogItem{}}
	ctx = domain.WithLinks(ctx, true)
	err := s.Store.CatalogRead(ctx, func(view *store.Store) error {
		links, err := view.LinkApps(ctx, true)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(links))
		for _, link := range links {
			ids = append(ids, link.ID)
		}
		packages, err := view.PublicPackagesByID(ctx, ids, "en-US")
		if err != nil {
			return err
		}
		items, err := catalogItems(ctx, view, packages)
		if err != nil {
			return err
		}
		for i, item := range items {
			item.Tap = nil
			bundles := []string{}
			if packages[i].BundleIDs != nil {
				bundles = *packages[i].BundleIDs
			}
			result.Items = append(result.Items, LinkCatalogItem{CatalogItem: item, BundleIDs: bundles, Link: packages[i].Link})
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	raw, err := json.Marshal(result.Items)
	if err != nil {
		return result, err
	}
	result.ETag = fmt.Sprintf(`"%x"`, sha256.Sum256(raw))
	return result, nil
}
