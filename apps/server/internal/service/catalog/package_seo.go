package catalog

import (
	"context"

	"github.com/opennavo/opennavo/server/gen/publicapi"
)

// seoOverride is the overview title and description an editor or agent set for locale (zh-Hant converts
// zh-CN's), or nil when the templates apply. The detail cache holds it; writes invalidate that cache.
func (s *PublicService) seoOverride(ctx context.Context, id int64, locale string) (*publicapi.PackageSeoOverride, error) {
	row, err := s.Store.PackageSEOOverride(ctx, id, locale)
	if err != nil || row == nil {
		return nil, err
	}
	return &publicapi.PackageSeoOverride{Title: row.Title, Description: row.Description}, nil
}
