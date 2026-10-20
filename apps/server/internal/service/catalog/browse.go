package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
)

func (s *PublicService) Browse(ctx context.Context, kind, bucket, locale string, page int) (publicapi.BrowsePage, error) {
	if (kind != "apps" && kind != "fonts") || page < 1 || !i18n.Valid(locale) {
		return publicapi.BrowsePage{}, domain.Validation()
	}
	if bucket != "" && bucket != "0-9" && bucket != "other" && (len(bucket) != 1 || bucket[0] < 'a' || bucket[0] > 'z') {
		return publicapi.BrowsePage{}, &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	if bucket == "" {
		page = 1
	}
	key := func(bucket string, page int) string {
		return fmt.Sprintf("c:browse:v2:%s:%s:%s:%d", kind, locale, bucket, page)
	}
	return cache.ReadThrough(ctx, s.Cache, key(bucket, page), 5*time.Minute, func(ctx context.Context) (publicapi.BrowsePage, error) {
		policy, err := s.Store.IndexPolicy(ctx)
		if err != nil {
			return publicapi.BrowsePage{}, err
		}
		directory, err := s.Store.BrowseDirectory(ctx, kind, locale, policy)
		if err != nil {
			return publicapi.BrowsePage{}, err
		}
		// Populate every page from one directory read, keeping individual entries
		// below the cache's size limit even when the whole catalog exceeds it.
		save := func(bucket string, page int) {
			if s.Cache == nil {
				return
			}
			value, err := directory.Page(bucket, page)
			if err != nil {
				return
			}
			if data, err := json.Marshal(value); err == nil {
				_ = s.Cache.Save(ctx, key(bucket, page), data, 5*time.Minute)
			}
		}
		for _, b := range directory.Buckets {
			for p := 1; p <= b.Pages; p++ {
				save(b.Key, p)
			}
		}
		save("", 1)
		return directory.Page(bucket, page)
	})
}
