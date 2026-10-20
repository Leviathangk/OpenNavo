//go:build integration

package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPageStateMergeDiscardsStateAndRequeuesCanonical(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	var ids []int64
	require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 ('link','old','old','','Old','','','{}','fixture'),('cask','canonical','canonical','homebrew/cask','Canonical','1.0','1.0','{}','fixture') RETURNING id`).Scan(&ids).Error)
	require.Len(t, ids, 2)
	from, to := ids[0], ids[1]
	later := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_page_state(package_id,locale,page_kind,page_number,content_hash,content_modified_at)
 VALUES(?,'en-US','overview',1,?,?),(?,'en-US','overview',1,?,?)`, from, strings.Repeat("a", 64), later, to, strings.Repeat("b", 64), later.Add(-time.Hour)).Error)
	err := st.DB.Transaction(func(tx *gorm.DB) error {
		if err := store.MergePageStatesForTest(ctx, tx, from, to); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	require.Error(t, err)
	rows, err := st.PageStates(ctx, []int64{from, to})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return store.MergePageStatesForTest(ctx, tx, from, to) }))
	rows, err = st.PageStates(ctx, []int64{from, to})
	require.NoError(t, err)
	require.Empty(t, rows)
	var dirty []int64
	require.NoError(t, st.DB.Raw("SELECT package_id FROM package_page_dirty ORDER BY package_id").Scan(&dirty).Error)
	require.Equal(t, []int64{to}, dirty)
}
