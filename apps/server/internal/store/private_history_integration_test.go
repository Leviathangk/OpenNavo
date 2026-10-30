//go:build integration

package store_test

import (
	"context"
	"maps"
	"testing"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFullPreMigrationHistoryReplayPreservesPrivate(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
VALUES(991,'cask','history-private','history-private','homebrew/cask','History','1','1','{}','fixture');
INSERT INTO package_meta(package_id,notes) VALUES(991,'historical note')`).Error)
	ref := content.Ref{Entity: "package", ID: 991}
	legacy, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	delete(legacy["package_meta"][0], "private")
	delete(legacy["package_meta"][0], "visibility_reason")
	require.NoError(t, st.DB.Exec(`UPDATE package_meta SET private=true,visibility_reason='Later owner decision',notes='later note' WHERE package_id=991`).Error)
	for _, missingMeta := range []bool{false, true} {
		target := maps.Clone(legacy)
		if missingMeta {
			target["package_meta"] = nil
		}
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).RestoreHistoryState(ctx, ref, target)
		}))
		restored, err := st.HistorySnapshot(ctx, ref)
		require.NoError(t, err)
		if !missingMeta {
			require.Equal(t, "historical note", restored["package_meta"][0]["notes"])
		}
		require.Equal(t, true, restored["package_meta"][0]["private"])
		require.Equal(t, "Later owner decision", restored["package_meta"][0]["visibility_reason"])
	}
	require.NotContains(t, legacy["package_meta"][0], "private", "historical data and hashes stay immutable")
}
