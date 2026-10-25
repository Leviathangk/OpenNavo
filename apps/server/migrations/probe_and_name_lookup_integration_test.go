//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestProbeAndConcurrentNameLookupUpgradeRetry(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 105)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES('cask','name-one','name-one','homebrew/cask','Same','1','1','{}','fixture'),('cask','name-two','name-two','homebrew/cask','Same','1','1','{}','fixture')`).Error)
	// Failed concurrent creation leaves an invalid index, as a deployment interruption can.
	require.Error(t, st.DB.Exec("CREATE UNIQUE INDEX CONCURRENTLY packages_active_name_lookup ON packages(lower(trim(name)))").Error)
	var valid bool
	require.NoError(t, st.DB.Raw("SELECT indisvalid FROM pg_index WHERE indexrelid='packages_active_name_lookup'::regclass").Scan(&valid).Error)
	require.False(t, valid)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 107))
	require.NoError(t, st.DB.Raw("SELECT indisvalid FROM pg_index WHERE indexrelid='packages_active_name_lookup'::regclass").Scan(&valid).Error)
	require.True(t, valid)
	require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at=now(),probe_failures=2").Error)
	require.NoError(t, goose.DownToContext(ctx, db, ".", 105))
	var count int64
	require.NoError(t, st.DB.Table("packages").Where("token IN ('name-one','name-two')").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 107))
}
