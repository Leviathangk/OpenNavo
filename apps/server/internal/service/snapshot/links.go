package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"

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
			link := desktopLink(packages[i].Link, domain.AllCatalogLocales(ctx))
			if link == nil {
				continue
			}
			item.Tap = nil
			bundles := []string{}
			if packages[i].BundleIDs != nil {
				bundles = *packages[i].BundleIDs
			}
			result.Items = append(result.Items, LinkCatalogItem{CatalogItem: item, BundleIDs: bundles, Link: link})
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

// The desktop endpoint retains the original five-price contract; unknown prices cannot be truthfully represented in
// it. Notes keep the original six languages unless the client asked for every desktop language (locales=all).
func desktopLink(input *publicapi.LinkInfo, allLocales bool) *publicapi.LinkInfo {
	if input == nil || !slices.Contains([]string{"free", "paid", "freemium", "subscription", "open_source"}, string(input.Pricing)) {
		return nil
	}
	out := *input
	if input.Notes != nil && !allLocales {
		notes := *input.Notes
		notes.DeDE, notes.FrFR, notes.ItIT, notes.ZhHant = nil, nil, nil, nil
		out.Notes = &notes
	}
	return &out
}
