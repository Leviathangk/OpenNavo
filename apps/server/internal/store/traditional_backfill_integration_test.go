//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const previousConverter = "opencc-data-1.1.9-s2twp-v4"

// upgradeFixture stores two apps and three background items as the previous converter left them:
// app 701 and com.example.same derive the same text with the new wording rules, app 702 and the
// other two items change.
func upgradeFixture(t *testing.T) *store.Store {
	t.Helper()
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES
 (701,'cask','same-wording','same-wording','homebrew/cask','Same','1','1','{}','fixture'),
 (702,'cask','new-wording','new-wording','homebrew/cask','New','1','1','{}','fixture')`).Error)
	for id, summary := range map[int64]string{701: "软件和鼠标", 702: "需要管理员权限"} {
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteContentText(ctx, content.Ref{Entity: "package", ID: id}, "zh-CN", map[string]any{"summary": summary}, true)
		}))
	}
	for _, statement := range []string{
		`UPDATE package_i18n SET summary='需要管理員許可權' WHERE package_id=702 AND locale='zh-Hant'`,
		`UPDATE package_i18n SET model=? WHERE package_id IN (701,702) AND locale='zh-Hant'`,
		`UPDATE derived_traditional d SET converter=?,after_data=(SELECT to_jsonb(i) FROM package_i18n i WHERE i.package_id=d.object_key::bigint AND i.locale='zh-Hant') WHERE entity='package'`,
		`INSERT INTO background_items(label) VALUES('com.example.login'),('com.example.menu'),('com.example.same')`,
		`INSERT INTO background_item_content(label,locale,description,item_type,evidence) SELECT label,locale,description,'login_item','{"status":"verified"}'::jsonb FROM (VALUES
 ('com.example.login','zh-CN','登录项'),('com.example.login','zh-Hant','登入項'),
 ('com.example.menu','zh-CN','菜单栏图标'),('com.example.menu','zh-Hant','選單欄圖示'),
 ('com.example.same','zh-CN','软件'),('com.example.same','zh-Hant','軟體')) v(label,locale,description)`,
		`INSERT INTO derived_traditional(entity,object_key,simplified_hash,converter,after_data) SELECT 'background_item',label,'fixture',?,to_jsonb(c) FROM background_item_content c WHERE locale='zh-Hant'`,
	} {
		var args []any
		if strings.Contains(statement, "?") {
			args = []any{previousConverter}
		}
		require.NoError(t, st.DB.Exec(statement, args...).Error, statement)
	}
	// Count side effects from here.
	require.NoError(t, st.DB.Exec(`DELETE FROM package_page_dirty; DELETE FROM catalog_changes; DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	return st
}

// fakeClock records every wait; sleeping only advances it, so a run's waits are exact.
type fakeClock struct {
	now    time.Time
	slept  []time.Duration
	cancel func(int) bool // stops the run before the given sleep
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	if c.cancel != nil && c.cancel(len(c.slept)) {
		return context.Canceled
	}
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func TestTraditionalUpgradeStampsSameTextAndPacesWrites(t *testing.T) {
	st := upgradeFixture(t)
	ctx := context.Background()
	scalar := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, st.DB.Raw(query, args...).Scan(&n).Error)
		return n
	}
	var untouched string
	require.NoError(t, st.DB.Raw("SELECT to_jsonb(i)::text FROM package_i18n i WHERE package_id=701 AND locale='zh-Hant'").Scan(&untouched).Error)
	revisions := scalar("SELECT count(*) FROM content_revisions")
	sameItem := scalar("SELECT revision FROM entity_revisions WHERE entity_key='background:com.example.same'")
	changedItems := scalar("SELECT sum(revision) FROM entity_revisions WHERE entity_key IN ('background:com.example.login','background:com.example.menu')")

	clock := &fakeClock{now: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)}
	var progress []store.TraditionalProgress
	counts, err := st.RunTraditionalBackfill(ctx, store.TraditionalBackfill{ObjectsPerSecond: 20, WritesPerMinute: 30, Now: clock.Now, Sleep: clock.Sleep,
		Progress: func(p store.TraditionalProgress) { progress = append(progress, p) }})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"package": 1, "package:stamped": 1, "background_item": 2, "background_item:stamped": 1}, counts)
	// Objects 50 ms apart; each write 2 s after the previous one, waited for outside its transaction.
	require.Equal(t, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 1950 * time.Millisecond, 2 * time.Second}, clock.slept)
	require.Len(t, progress, 2)
	require.Equal(t, store.TraditionalProgress{Objects: 5, Pending: 5, Remaining: 5, ETA: 250 * time.Millisecond}, progress[0])
	require.Equal(t, store.TraditionalProgress{Objects: 5, Pending: 5, Processed: 5, Stamped: 2, Written: 3, Elapsed: 4050 * time.Millisecond, Done: true}, progress[1])

	// Same text: only the receipt names the new converter. The row, revisions, page state, caches and
	// URL notifications stay as they were.
	var row string
	require.NoError(t, st.DB.Raw("SELECT to_jsonb(i)::text FROM package_i18n i WHERE package_id=701 AND locale='zh-Hant'").Scan(&row).Error)
	require.JSONEq(t, untouched, row)
	require.Zero(t, scalar("SELECT count(*) FROM derived_traditional WHERE converter<>?", traditional.Version))
	require.Equal(t, revisions, scalar("SELECT count(*) FROM content_revisions"), "derivation records no content revision")
	require.Equal(t, sameItem, scalar("SELECT revision FROM entity_revisions WHERE entity_key='background:com.example.same'"))
	// Changed text is written as a live edit would be: the app's page, caches and URL notification once.
	value, err := st.TranslationValue(ctx, content.Ref{Entity: "package", ID: 702}, "zh-Hant")
	require.NoError(t, err)
	require.Equal(t, "需要管理員權限", value.Fields["summary"])
	require.Equal(t, []int64{702}, traditionalIDs(t, st, "SELECT package_id FROM package_page_dirty ORDER BY package_id"))
	require.Equal(t, []int64{702}, traditionalIDs(t, st, "SELECT package_id FROM catalog_changes WHERE reason='content' ORDER BY seq"))
	require.Equal(t, int64(1), scalar("SELECT count(*) FROM job_outbox WHERE unique_key='cache:invalidate:translation:package:702'"))
	var descriptions []string
	require.NoError(t, st.DB.Raw("SELECT description FROM background_item_content WHERE locale='zh-Hant' ORDER BY label").Scan(&descriptions).Error)
	require.Equal(t, []string{"登入項目", "選單列圖示", "軟體"}, descriptions)
	require.Equal(t, changedItems+2, scalar("SELECT sum(revision) FROM entity_revisions WHERE entity_key IN ('background:com.example.login','background:com.example.menu')"))
	// The two rewritten items share one cache clearing, due a minute after the first.
	require.Equal(t, int64(1), scalar("SELECT count(*) FROM job_outbox WHERE job_type='cache:invalidate' AND payload @> '[\"c:*\"]'"))
	require.Equal(t, int64(1), scalar("SELECT count(*) FROM job_outbox WHERE unique_key='cache:invalidate:traditional:backfill' AND available_at>now()+interval '50 seconds'"))
	require.Equal(t, int64(2), scalar("SELECT count(*) FROM job_outbox WHERE job_type='cache:invalidate'"))

	// The next start passes over everything without waiting or writing.
	clock = &fakeClock{now: clock.now}
	progress = nil
	counts, err = st.RunTraditionalBackfill(ctx, store.TraditionalBackfill{ObjectsPerSecond: 20, WritesPerMinute: 30, Now: clock.Now, Sleep: clock.Sleep,
		Progress: func(p store.TraditionalProgress) { progress = append(progress, p) }})
	require.NoError(t, err)
	require.Empty(t, counts)
	require.Empty(t, clock.slept)
	require.Equal(t, []store.TraditionalProgress{{Objects: 5, Processed: 5, Done: true}}, progress)
}

// A run stopped by a restart continues where it stopped: finished objects are not derived again.
func TestTraditionalBackfillResumesAfterRestart(t *testing.T) {
	st := upgradeFixture(t)
	ctx := context.Background()
	// Stop at the third wait, the write budget of the first background item.
	clock := &fakeClock{now: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC), cancel: func(sleeps int) bool { return sleeps == 2 }}
	counts, err := st.RunTraditionalBackfill(ctx, store.TraditionalBackfill{ObjectsPerSecond: 20, WritesPerMinute: 30, Now: clock.Now, Sleep: clock.Sleep})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, map[string]int{"package": 1, "package:stamped": 1}, counts)
	var held string
	require.NoError(t, st.DB.Raw("SELECT description FROM background_item_content WHERE label='com.example.login' AND locale='zh-Hant'").Scan(&held).Error)
	require.Equal(t, "登入項", held, "a held write is not made")

	clock = &fakeClock{now: clock.now}
	var progress []store.TraditionalProgress
	counts, err = st.RunTraditionalBackfill(ctx, store.TraditionalBackfill{ObjectsPerSecond: 20, WritesPerMinute: 30, Now: clock.Now, Sleep: clock.Sleep,
		Progress: func(p store.TraditionalProgress) { progress = append(progress, p) }})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"background_item": 2, "background_item:stamped": 1}, counts)
	require.Equal(t, 3, progress[0].Pending)
	require.Equal(t, int64(1), int64(len(traditionalIDs(t, st, "SELECT package_id FROM catalog_changes WHERE reason='content'"))), "the app written before the restart is not written again")
}

// A preview evaluates the same way but keeps nothing, and never waits for a write budget.
func TestTraditionalBackfillPreviewKeepsNothing(t *testing.T) {
	st := upgradeFixture(t)
	clock := &fakeClock{now: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)}
	counts, err := st.RunTraditionalBackfill(context.Background(), store.TraditionalBackfill{DryRun: true, ObjectsPerSecond: 20, WritesPerMinute: 30, Now: clock.Now, Sleep: clock.Sleep})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"package": 1, "package:stamped": 1, "background_item": 2, "background_item:stamped": 1}, counts)
	require.Equal(t, []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}, clock.slept)
	var stale int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM derived_traditional WHERE converter=?", previousConverter).Scan(&stale).Error)
	require.EqualValues(t, 5, stale)
	require.Empty(t, traditionalIDs(t, st, "SELECT package_id FROM catalog_changes"))
}

func traditionalIDs(t *testing.T, st *store.Store, query string) []int64 {
	t.Helper()
	out := []int64{}
	require.NoError(t, st.DB.Raw(query).Scan(&out).Error)
	return out
}
