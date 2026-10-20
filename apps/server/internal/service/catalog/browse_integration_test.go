//go:build integration

package catalog_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestBrowseReusesDirectoryAcrossBucketsAndPages(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale)
 SELECT 'cask','browse-'||n,'browse-'||n,'homebrew/cask',CASE WHEN n=302 THEN 'Beta' ELSE 'Alpha '||n END,'1','1','{}','fixture','Useful app','en-US' FROM generate_series(1,302) n`).Error)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	root, err := svc.Browse(ctx, "apps", "", "en-US", 1)
	require.NoError(t, err)
	require.Len(t, root.Buckets, 2)
	// A different bucket and page must use the same directory's cached slices.
	svc.Store = nil
	page, err := svc.Browse(ctx, "apps", "a", "en-US", 2)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	page, err = svc.Browse(ctx, "apps", "b", "en-US", 1)
	require.NoError(t, err)
	require.Equal(t, "browse-302", page.Items[0].Token)
	// Existing content/settings invalidation must still clear all slices.
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{"c:browse:*"}))
	svc.Store = st
	require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled=true WHERE token='browse-302'`).Error)
	_, err = svc.Browse(ctx, "apps", "b", "en-US", 1)
	require.Error(t, err)
}
