//go:build integration

package urlnotify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBulkCatalogRefreshDoesNotReadReleaseTimelines(t *testing.T) {
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 SELECT n,'cask','fixture-'||n,'fixture-'||n,'homebrew/cask','Fixture','1','1','{}','fixture' FROM generate_series(1,51) n;
 INSERT INTO catalog_changes(package_id,kind,token,op,reason)
 SELECT id,kind,token,'upsert','content' FROM packages`).Error)
	blocked := errors.New("fixture: expensive timeline read")
	require.NoError(t, st.DB.Callback().Row().Before("gorm:row").Register("fixture:block-timeline", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "FROM package_versions WHERE package_id IN") {
			_ = tx.AddError(blocked)
		}
	}))
	n := &Notifier{Store: st, Config: config.Config{WebBaseURL: "https://site.example"}, Batch: 2000}
	var end int64
	require.NoError(t, st.DB.Raw("SELECT max(seq) FROM catalog_changes").Scan(&end).Error)
	plan, count, err := n.catalogPlan(context.Background(), 0, end, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 51, count)
	require.Len(t, plan.Paths, 51*len(seo.Locales), "all refresh obligations are preserved")
	require.Empty(t, plan.IndexNowURLs)
	// A real app update in the same batch must still load facts before announcing.
	require.NoError(t, st.DB.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason)
 VALUES(1,'cask','fixture-1','upsert','homebrew')`).Error)
	require.NoError(t, st.DB.Raw("SELECT max(seq) FROM catalog_changes").Scan(&end).Error)
	_, _, err = n.catalogPlan(context.Background(), 0, end, time.Now().Add(time.Minute))
	require.ErrorIs(t, err, blocked)
}
