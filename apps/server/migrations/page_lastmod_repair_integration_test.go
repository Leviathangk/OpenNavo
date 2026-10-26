//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/migrations"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// 00113 gives pages dated in the hour of the 2026-10-24 hash upgrade the time of the last real change they show,
// touches nothing else, and leaves dates the following hash-v3 refresh keeps.
func TestRepairPageDatesOfTheUpgradeHour(t *testing.T) {
	st := testutil.NewSEOStoreAtVersion(t, 112)
	ctx := context.Background()
	db, err := st.DB.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Files)
	require.NoError(t, goose.SetDialect("postgres"))
	exec := func(query string, args ...any) {
		t.Helper()
		require.NoError(t, st.DB.Exec(query, args...).Error)
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	window := time.Date(2026, 10, 24, 12, 8, 30, 0, time.UTC)
	// Apps 801 and 802 changed long before the hour, 803 after it; 804's pages are dated outside it.
	exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,first_seen_at,version_changed_at) VALUES
 (801,'cask','repaired','repaired','homebrew/cask','Repaired','1.0','1.0','{}','fixture','en-US','2020-01-01',?),
 (802,'cask','released','released','homebrew/cask','Released','1.0','1.0','{}','fixture','en-US','2020-01-01',?),
 (803,'cask','later','later','homebrew/cask','Later','1.0','1.0','{}','fixture','en-US','2020-01-01',?),
 (804,'cask','outside','outside','homebrew/cask','Outside','1.0','1.0','{}','fixture','en-US','2020-01-01',?)`, day(1), day(2), day(25), day(1))
	exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,updated_at) VALUES
 (801,'en-US','Repaired','An editor','A useful editor','source','human','en-US',?),(802,'en-US','Released','An editor','A useful editor','source','human','en-US',?),
 (803,'en-US','Later','An editor','A useful editor','source','human','en-US',?),(804,'en-US','Outside','An editor','A useful editor','source','human','en-US',?)`, day(5), day(3), day(3), day(3))
	exec(`INSERT INTO releases(package_id,source,source_key,version,body_markdown,source_url,source_locale,updated_at) VALUES
 (802,'manual','1.0','1.0','Release notes','https://vendor.example/notes','en-US',?),(802,'manual','0.9','0.9','Hidden notes','https://vendor.example/old','en-US',?)`, day(10), day(20))
	exec(`UPDATE releases SET hidden=true WHERE package_id=802 AND version='0.9'`)
	exec(`UPDATE releases SET updated_at=? WHERE package_id=802 AND version='0.9'`, day(20))
	exec(`INSERT INTO package_versions(package_id,version,version_base,created_at) VALUES(802,'1.0','1.0',?)`, day(8))
	exec(`DELETE FROM package_page_dirty`)
	// Hash-v2 states written in the upgrade hour (and one before it).
	for _, id := range []int{801, 802, 803} {
		for _, kind := range []string{"overview", "details", "dependencies", "versions"} {
			exec(`INSERT INTO package_page_state(package_id,locale,page_kind,page_number,content_hash,content_hash_version,content_modified_at,content_checked_at)
 VALUES(?,'en-US',?,1,repeat('a',64),2,?,'2026-10-24T12:08:31Z')`, id, kind, window)
		}
	}
	exec(`INSERT INTO package_page_state(package_id,locale,page_kind,page_number,content_hash,content_hash_version,content_modified_at,content_checked_at)
 VALUES(804,'en-US','overview',1,repeat('a',64),2,'2026-10-24T11:59:59Z','2026-10-24T12:08:31Z')`)
	var triggers int
	require.NoError(t, st.DB.Raw(`SELECT count(*) FROM pg_trigger WHERE tgrelid='package_page_state'::regclass AND NOT tgisinternal`).Scan(&triggers).Error)
	require.Zero(t, triggers, "a repair must queue nothing")
	counts := func() [3]int64 {
		t.Helper()
		var n [3]int64
		require.NoError(t, st.DB.Raw(`SELECT (SELECT count(*) FROM package_page_dirty),(SELECT count(*) FROM catalog_changes),(SELECT count(*) FROM job_outbox)`).Row().Scan(&n[0], &n[1], &n[2]))
		return n
	}
	before := counts()

	require.NoError(t, goose.UpToContext(ctx, db, ".", 113))
	type state struct {
		ContentModifiedAt, ContentCheckedAt time.Time
		ContentHash                         string
		ContentHashVersion                  int
	}
	read := func(id int, kind string) state {
		t.Helper()
		var row state
		require.NoError(t, st.DB.Raw(`SELECT content_modified_at,content_checked_at,content_hash,content_hash_version FROM package_page_state WHERE package_id=? AND locale='en-US' AND page_kind=?`, id, kind).Scan(&row).Error)
		return row
	}
	for _, tc := range []struct {
		id   int
		kind string
		want time.Time
	}{
		{801, "overview", day(5)}, {801, "details", day(5)}, {801, "dependencies", day(1)}, {801, "versions", day(1)},
		// The shown release (not the hidden one) and the Homebrew version date the timeline pages.
		{802, "overview", day(10)}, {802, "details", day(3)}, {802, "dependencies", day(2)}, {802, "versions", day(10)},
		// Evidence later than the date: kept for the v3 refresh.
		{803, "overview", window}, {803, "details", window}, {803, "dependencies", window}, {803, "versions", window},
		{804, "overview", time.Date(2026, 10, 24, 11, 59, 59, 0, time.UTC)},
	} {
		row := read(tc.id, tc.kind)
		require.True(t, row.ContentModifiedAt.Equal(tc.want), "%d %s: %s", tc.id, tc.kind, row.ContentModifiedAt)
		// Hashes, their version and the check time stay, so the v3 refresh still runs.
		require.True(t, row.ContentCheckedAt.Equal(time.Date(2026, 10, 24, 12, 8, 31, 0, time.UTC)), "%d %s", tc.id, tc.kind)
		require.Equal(t, strings.Repeat("a", 64), row.ContentHash)
		require.Equal(t, 2, row.ContentHashVersion)
	}
	require.Equal(t, before, counts(), "no refresh job, URL change or outbox row")

	// Down keeps the repaired dates.
	require.NoError(t, goose.DownToContext(ctx, db, ".", 112))
	require.True(t, read(801, "overview").ContentModifiedAt.Equal(day(5)))
	require.NoError(t, goose.UpToContext(ctx, db, ".", 113))

	// The current hash refresh finds no change after the repaired dates.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_page_dirty(package_id,changed_at) VALUES(801,now()),(802,now())`).Error)
	for {
		count, err := st.RefreshPackagePageStates(ctx, 100)
		require.NoError(t, err)
		if count == 0 {
			break
		}
	}
	for id, kinds := range map[int]map[string]time.Time{
		801: {"overview": day(5), "details": day(5), "dependencies": day(1), "versions": day(1)},
		802: {"overview": day(10), "details": day(3), "dependencies": day(2), "versions": day(10)},
	} {
		for kind, want := range kinds {
			row := read(id, kind)
			require.Equal(t, 4, row.ContentHashVersion, "%d %s", id, kind)
			require.True(t, row.ContentModifiedAt.Equal(want), "%d %s: %s", id, kind, row.ContentModifiedAt)
		}
	}
}
