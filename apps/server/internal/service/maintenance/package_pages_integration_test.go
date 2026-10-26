//go:build integration

package maintenance_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/maintenance"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The daily cleanup deletes intent switches and overview overrides whose app row is gone.
func TestCleanupSweepsOrphanPackagePageSettings(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(1501,'cask','swept-live','swept-live','homebrew/cask','Live','1','1','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_intent_pages(package_id,page_kind,locale) VALUES(1501,'alternatives','en-US'),(1599,'alternatives','en-US'),(1599,'uninstall','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_seo_overrides(package_id,locale,title) VALUES(1501,'en-US','Live'),(1599,'en-US','Gone')`).Error)
	result, err := (&maintenance.Service{Store: st}).Run(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, result["intentPageOrphans"])
	require.EqualValues(t, 1, result["seoOverrideOrphans"])
	var left int64
	require.NoError(t, st.DB.Raw("SELECT (SELECT count(*) FROM package_intent_pages)+(SELECT count(*) FROM package_seo_overrides)").Scan(&left).Error)
	require.EqualValues(t, 2, left, "the live app keeps its rows")
}
