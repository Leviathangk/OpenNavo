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

func TestMigration126BackfillsLegacyHiddenReasons(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 125)
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
	require.NoError(t, st.DB.Exec("UPDATE package_meta SET private=true WHERE package_id=903").Error)
	require.NoError(t, st.DB.Raw("SELECT visibility_reason FROM package_meta WHERE package_id=903").Scan(&rows[2].VisibilityReason).Error)
	require.Equal(t, "Legacy setting; reason not recorded.", rows[2].VisibilityReason)
}

func TestMigration126DefaultsReasonForLegacyHiddenWrites(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 125)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (911,'cask','legacy-hidden-write','legacy-hidden-write','homebrew/cask','Fixture','1','1','{}','fixture'),
 (912,'cask','legacy-private-write','legacy-private-write','homebrew/cask','Fixture','1','1','{}','fixture'),
 (913,'cask','legacy-reason-write','legacy-reason-write','homebrew/cask','Fixture','1','1','{}','fixture');
 INSERT INTO package_meta(package_id) VALUES(911),(912),(913)`).Error)
	db, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(context.Background(), db, ".", 126))

	require.NoError(t, st.DB.Exec("UPDATE package_meta SET hidden=true WHERE package_id=911").Error)
	var hidden bool
	var reason string
	require.NoError(t, st.DB.Raw("SELECT hidden,visibility_reason FROM package_meta WHERE package_id=911").Row().Scan(&hidden, &reason))
	require.True(t, hidden)
	require.Equal(t, "Legacy setting; reason not recorded.", reason)

	require.NoError(t, st.DB.Exec("UPDATE package_meta SET private=true,visibility_reason='' WHERE package_id=912").Error)
	var private bool
	require.NoError(t, st.DB.Raw("SELECT private,visibility_reason FROM package_meta WHERE package_id=912").Row().Scan(&private, &reason))
	require.True(t, private)
	require.Equal(t, "Legacy setting; reason not recorded.", reason)

	require.NoError(t, st.DB.Exec("UPDATE package_meta SET hidden=true,visibility_reason='Owner recorded reason' WHERE package_id=913").Error)
	require.NoError(t, st.DB.Raw("SELECT visibility_reason FROM package_meta WHERE package_id=913").Scan(&reason).Error)
	require.Equal(t, "Owner recorded reason", reason)
}

func TestMigration126DownRestoresLegacySchema(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStoreAtVersion(t, 125)
	db, err := st.DB.DB()
	require.NoError(t, err)
	require.NoError(t, goose.UpToContext(ctx, db, ".", 126))

	hasColumn := func(name string) bool {
		var exists bool
		require.NoError(t, st.DB.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='package_meta' AND column_name=?)`, name).Scan(&exists).Error)
		return exists
	}
	hasTrigger := func(name string) bool {
		var exists bool
		require.NoError(t, st.DB.Raw(`SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgname=? AND NOT tgisinternal)`, name).Scan(&exists).Error)
		return exists
	}
	hasFunction := func(name string) bool {
		var exists bool
		require.NoError(t, st.DB.Raw(`SELECT EXISTS(SELECT 1 FROM pg_proc WHERE proname=?)`, name).Scan(&exists).Error)
		return exists
	}
	hasCatalogVisibility := func() bool {
		var exists bool
		require.NoError(t, st.DB.Raw(`SELECT to_regclass('public.catalog_visibility') IS NOT NULL`).Scan(&exists).Error)
		return exists
	}
	assertUp := func() {
		require.True(t, hasColumn("private"))
		require.True(t, hasColumn("visibility_reason"))
		require.True(t, hasTrigger("package_visibility_reason_default"))
		require.True(t, hasFunction("fill_package_visibility_reason"))
		require.True(t, hasTrigger("package_visibility_generation"))
		require.True(t, hasFunction("bump_catalog_visibility"))
		require.True(t, hasCatalogVisibility())
	}
	assertDown := func() {
		require.False(t, hasColumn("private"))
		require.False(t, hasColumn("visibility_reason"))
		require.False(t, hasTrigger("package_visibility_reason_default"))
		require.False(t, hasFunction("fill_package_visibility_reason"))
		require.False(t, hasTrigger("package_visibility_generation"))
		require.False(t, hasFunction("bump_catalog_visibility"))
		require.False(t, hasCatalogVisibility())
	}
	assertUp()
	require.NoError(t, goose.DownToContext(ctx, db, ".", 125))
	assertDown()
	require.NoError(t, goose.UpToContext(ctx, db, ".", 126))
	assertUp()
}

func TestMigration126ProductionScaleLockTime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st := testutil.NewSEOStoreAtVersion(t, 125)
	// Bulk fixture loading need not execute content/search side effects for each
	// synthetic app. Re-enable every user trigger before timing the migration.
	require.NoError(t, st.DB.WithContext(ctx).Exec(`ALTER TABLE packages DISABLE TRIGGER USER;
ALTER TABLE package_meta DISABLE TRIGGER USER;
INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
SELECT n,'cask','scale-'||n,'scale-'||n,'homebrew/cask','Scale fixture '||n,'1','1','{}','fixture' FROM generate_series(1,20000) n;
INSERT INTO package_meta(package_id,hidden,developer,notes)
SELECT id,id<=35,repeat('Fixture vendor ',4),repeat('Fixture editorial note. ',8) FROM packages;
ALTER TABLE packages ENABLE TRIGGER USER;
ALTER TABLE package_meta ENABLE TRIGGER USER;
ANALYZE package_meta`).Error)
	var count, hidden int64
	require.NoError(t, st.DB.Raw("SELECT count(*),count(*) FILTER(WHERE hidden) FROM package_meta").Row().Scan(&count, &hidden))
	require.EqualValues(t, 20000, count)
	require.EqualValues(t, 35, hidden)
	db, err := st.DB.DB()
	require.NoError(t, err)
	type observation struct {
		first, last time.Time
		err         error
	}
	ready, stop := make(chan struct{}), make(chan struct{})
	observed := make(chan observation, 1)
	go func() {
		result := observation{}
		first := true
		for {
			var locked bool
			result.err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='package_meta'::regclass AND mode='AccessExclusiveLock' AND granted)`).Scan(&locked)
			if first {
				close(ready)
				first = false
			}
			if result.err != nil {
				observed <- result
				return
			}
			if locked {
				now := time.Now()
				if result.first.IsZero() {
					result.first = now
				}
				result.last = now
			}
			select {
			case <-stop:
				observed <- result
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	<-ready
	started := time.Now()
	err = goose.UpToContext(ctx, db, ".", 126)
	elapsed := time.Since(started)
	close(stop)
	locks := <-observed
	require.NoError(t, err)
	require.NoError(t, locks.err)
	var backfilled int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM package_meta WHERE hidden AND NOT private AND visibility_reason='Legacy setting; reason not recorded.'").Scan(&backfilled).Error)
	require.EqualValues(t, 35, backfilled)
	// PostgreSQL holds ALTER TABLE's AccessExclusive lock until the migration
	// transaction commits. Its entire elapsed time is a conservative lock-hold
	// upper bound, including goose bookkeeping; sampling supplies a lower bound.
	t.Logf("00126 synthetic scale: package_meta=%d hidden=%d; migration/AccessExclusive upper bound=%s; observed held interval=%s (1ms sampling); PostgreSQL 18, local isolated Docker, no contending workload", count, hidden, elapsed, locks.last.Sub(locks.first))
}
