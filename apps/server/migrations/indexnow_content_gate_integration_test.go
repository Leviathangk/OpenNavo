//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestIndexNowContentGateMigration(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 119)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	old := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	latest := old.Add(30 * time.Minute)
	for _, row := range []struct {
		path, state, robots, canonical, problem string
		announce                                bool
		at                                      time.Time
	}{
		{"/apps/sent", "announced", "index, follow", "", "", true, old},
		{"/apps/sent", "announced", "noindex, follow", "/apps/new", "", true, latest},
		// A higher id need not be the latest accepted time.
		{"/apps/sent", "announced", "index, follow", "/apps/older", "", true, old},
		{"/apps/sent", "announced", "index, follow", "", "", false, latest.Add(time.Minute)},
		{"/apps/sent", "dead", "index, follow", "", "failed", true, latest.Add(2 * time.Minute)},
		{"/apps/failed", "announced", "index, follow", "", "failed", true, latest},
		{"/apps/suppressed", "announced", "noindex, follow", "", "", false, latest},
		{"/apps/refresh", "announced", "", "", "", false, latest},
		{"/apps/pending", "probed", "index, follow", "", "", true, latest},
		{"/apps/retired", "announced", "", "", "", true, latest},
		{"/articles/sent", "announced", "index, follow", "", "", true, latest},
	} {
		require.NoError(t, st.DB.Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,state,robots,canonical_path,last_error,announce,updated_at)
 VALUES(?,?,'en-US','updated',?,?,?,?,?,?)`, "https://site.example"+row.path, row.path, row.state, row.robots, row.canonical, row.problem, row.announce, row.at).Error)
	}
	// Older receipts must also leave the deduplication baseline empty.
	require.NoError(t, st.DB.Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,state,robots,announce,updated_at)
 VALUES('https://site.example/apps/too-old','/apps/too-old','en-US','updated','announced','index, follow',true,now()-interval '15 days')`).Error)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 120))
	// Legacy completion times cannot prove which content version was sent.
	// Even successful receipts must leave the gate cold so an edit made during
	// an old request can be announced by a later outbox task.
	var receipts int64
	require.NoError(t, st.DB.Table("indexnow_url_state").Count(&receipts).Error)
	require.Zero(t, receipts, "historical receipts must not establish a deduplication baseline")
	var nonempty int64
	require.NoError(t, st.DB.Table("url_notify_outbox").Where("skip_reason<>''").Count(&nonempty).Error)
	require.Zero(t, nonempty)
	require.Error(t, st.DB.Exec("UPDATE url_notify_outbox SET skip_reason='unsupported'").Error)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 119))
	var exists bool
	require.NoError(t, st.DB.Raw("SELECT to_regclass('indexnow_url_state') IS NOT NULL").Scan(&exists).Error)
	require.False(t, exists)
	require.NoError(t, st.DB.Raw("SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='url_notify_outbox' AND column_name='skip_reason')").Scan(&exists).Error)
	require.False(t, exists)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 120))
}
