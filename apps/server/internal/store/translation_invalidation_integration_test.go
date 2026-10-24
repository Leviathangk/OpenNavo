//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTranslationInvalidationKeepsUnrelatedCachedPages(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 SELECT i,'cask','translation-'||i,'translation-'||i,'homebrew/cask','Fixture','1','1','{}','fixture' FROM generate_series(1,2) i`).Error)
	affected := []string{cache.PackageDetailPrefix + "cask:translation-1:en-US", "c:rel:c4:cask:translation-1:en-US", "c:dep:c1:cask:translation-1:en-US:1:", "c:related:c1:cask:translation-1:en-US:5"}
	unaffected := []string{cache.PackageDetailPrefix + "cask:translation-2:en-US", "c:dep:c1:cask:translation-2:en-US:1:", "c:related:c1:cask:translation-2:en-US:5", "c:cfg:client:macos:en-US"}
	for _, key := range append(append([]string{}, affected...), unaffected...) {
		require.NoError(t, st.Redis.Set(ctx, key, "{}", time.Minute).Err())
	}
	require.NoError(t, st.QueueTranslationInvalidation(ctx, content.Ref{Entity: "package", ID: 1}))
	var patterns []string
	require.NoError(t, st.DB.Raw(`SELECT jsonb_array_elements_text(payload) FROM job_outbox WHERE unique_key='cache:invalidate:translation:package:1'`).Scan(&patterns).Error)
	require.NotContains(t, patterns, "c:*")
	require.NoError(t, cache.Invalidate(ctx, st.Redis, patterns))
	for _, key := range affected {
		require.EqualValues(t, 0, st.Redis.Exists(ctx, key).Val(), key)
	}
	for _, key := range unaffected {
		require.EqualValues(t, 1, st.Redis.Exists(ctx, key).Val(), key)
	}
}
