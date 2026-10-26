//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestNotificationLocaleAndRetentionMigrations(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 113)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','legacy','legacy','homebrew/cask','Legacy','1','1','{}','fixture');
 INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(701,'cask','legacy','upsert','content');
 INSERT INTO content_url_changes(paths,announce) VALUES(ARRAY['/articles/legacy'],false)`).Error)
	db, err := st.DB.DB()
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, goose.UpToContext(ctx, db, ".", 115))
		changes, err := st.PendingURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
		require.NoError(t, err)
		require.Len(t, changes, 1)
		require.Nil(t, changes[0].Locales)
		content, err := st.PendingContentURLChanges(ctx, 0, time.Now().Add(time.Minute), 100)
		require.NoError(t, err)
		require.Len(t, content, 1)
		require.Nil(t, content[0].Locales)
		require.NoError(t, st.AppendContentChange(ctx, 701, "zh-Hant"))
		require.NoError(t, st.DB.Exec("DELETE FROM catalog_changes WHERE locales IS NOT NULL").Error)
		var indexes int64
		require.NoError(t, st.DB.Raw(`SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indisvalid AND c.relname IN ('url_notify_outbox_url_history','url_notify_outbox_settings_history','url_notify_outbox_terminal','url_notify_outbox_dead')`).Scan(&indexes).Error)
		require.Equal(t, int64(4), indexes)
		require.NoError(t, goose.DownToContext(ctx, db, ".", 113))
	}
}
