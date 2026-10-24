//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDependencyIndexDecisionMatchesVisibleDependents(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en)
 VALUES(941,'cask','dependency','dependency','homebrew/cask','Dependency','1','1','{}','fixture','Useful dependency'),
 (942,'cask','dependent','dependent','homebrew/cask','Dependent','1','1','{}','fixture','Useful application');
 UPDATE packages SET dependencies='{"dependsOn":{"cask":["dependency"]}}' WHERE id=942;
 INSERT INTO package_meta(package_id,hidden) VALUES(942,false)`).Error)
	for _, state := range []string{"visible", "disabled", "stale", "hidden", "removed", "visible"} {
		require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled=?,stale_disabled=?,removed_at=CASE WHEN ? THEN now() END WHERE id=942`, state == "disabled", state == "stale", state == "removed").Error)
		require.NoError(t, st.DB.Exec("UPDATE package_meta SET hidden=? WHERE package_id=942", state == "hidden").Error)
		row, found, err := st.IndexRowByID(ctx, 941)
		require.NoError(t, err)
		require.True(t, found)
		_, count, err := st.PublicDependents(ctx, store.PublicPackage{ID: 941, Kind: "cask", Token: "dependency"}, "en-US")
		require.NoError(t, err)
		require.Equal(t, state == "visible", count > 0, state)
		require.Equal(t, count > 0, row.HasDependencyContent, state)
		decision := seo.DecidePage(row.Facts(), seo.PageRef{Kind: seo.PageDependencies, Locale: "en-US"}, seo.Policy{Mode: "shadow"})
		require.False(t, decision.InSitemap)
		require.Equal(t, count > 0, decision.Announce, state)
		if count == 0 {
			require.Equal(t, "noindex, follow", decision.Robots)
			require.Equal(t, "/apps/dependency", decision.CanonicalPath)
		}
	}
}
