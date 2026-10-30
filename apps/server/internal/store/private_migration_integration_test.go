//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestMigration126BackfillsLegacyHiddenReasons(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 123)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (901,'cask','legacy-hidden-one','legacy-hidden-one','homebrew/cask','Fixture','1','1','{}','fixture'),
 (902,'cask','legacy-hidden-two','legacy-hidden-two','homebrew/cask','Fixture','1','1','{}','fixture'),
 (903,'cask','public-sample','public-sample','homebrew/cask','Fixture','1','1','{}','fixture');
 INSERT INTO package_meta(package_id,hidden) VALUES(901,true),(902,true),(903,false)`).Error)
	db, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(context.Background(), db, ".", 126))
	var rows []struct {
		Hidden, Private  bool
		VisibilityReason string
	}
	require.NoError(t, st.DB.Raw("SELECT hidden,private,visibility_reason FROM package_meta ORDER BY package_id").Scan(&rows).Error)
	require.Len(t, rows, 3)
	for _, row := range rows[:2] {
		require.True(t, row.Hidden)
		require.False(t, row.Private)
		require.Equal(t, "Legacy setting; reason not recorded.", row.VisibilityReason)
	}
	require.Empty(t, rows[2].VisibilityReason)
	require.Error(t, st.DB.Exec("UPDATE package_meta SET private=true WHERE package_id=903").Error)
}
