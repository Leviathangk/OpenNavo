//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type pageRow struct {
	ContentModifiedAt  time.Time
	ContentHash        string
	ContentHashVersion int
}

func pageRowOf(t *testing.T, st *store.Store, id int64, kind string) pageRow {
	t.Helper()
	var row pageRow
	require.NoError(t, st.DB.Raw(`SELECT content_modified_at,content_hash,content_hash_version FROM package_page_state
 WHERE package_id=? AND locale='en-US' AND page_kind=? AND page_number=1`, id, kind).Scan(&row).Error)
	require.False(t, row.ContentModifiedAt.IsZero(), kind)
	return row
}

func refreshPages(t *testing.T, st *store.Store) {
	t.Helper()
	for {
		count, err := st.RefreshPackagePageStates(context.Background(), 100)
		require.NoError(t, err)
		if count == 0 {
			return
		}
	}
}

func pagePackage(t *testing.T, st *store.Store, token string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,first_seen_at,version_changed_at)
 VALUES('cask',?,?,'homebrew/cask',?,'1.0','1.0','{}','fixture','en-US','2020-01-01','2020-01-01') RETURNING id`, token, token, token).Scan(&id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,updated_at)
 VALUES(?,'en-US',?,'An editor','A useful editor','source','human','en-US','2020-01-01')`, id, token).Error)
	return id
}

// A check or verification that confirms the same content leaves the sitemap date alone; what pages show moves it.
func TestPageDatesIgnoreVerificationAndFollowShownFields(t *testing.T) {
	st := testutil.NewSEOStore(t)
	id := pagePackage(t, st, "verified")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_facts(package_id,price) VALUES(?,jsonb_build_object('type','paid','amount',9.99,'currency','USD','status','verified',
 'sourceUrl','https://vendor.example/pricing','verifiedAt',to_char(now()-interval '10 days','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'verifiedBy','editor','verifier','admin','method','vendor_site'))`, id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'price','pro-us',
 '{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":false,"amount":"9.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}',
 jsonb_build_object('sourceUrl','https://vendor.example/pricing','status','verified','checkedAt',now()-interval '10 days','validUntil',now()+interval '20 days'))`, id).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,developer) VALUES(?,'Example Inc.')`, id).Error)
	refreshPages(t, st)
	first := pageRowOf(t, st, id, "overview")

	for name, statement := range map[string]string{
		"price verified again": `UPDATE package_facts SET price=price||jsonb_build_object('verifiedAt',to_char(now(),'YYYY-MM-DD"T"HH24:MI:SS"Z"'),'verifiedBy','agent:3:7','verifier','agent') WHERE package_id=?`,
		"record checked again": `UPDATE package_fact_records SET evidence=evidence||jsonb_build_object('checkedAt',now(),'validUntil',now()+interval '30 days'),updated_at=now() WHERE package_id=?`,
		"undisplayed fact key": `UPDATE package_facts SET price=price||'{"reviewNote":"checked twice"}' WHERE package_id=?`,
		"undisplayed meta":     `UPDATE package_meta SET notes='internal',editor_choice=true,icon_source='manual',changelog_excluded=true WHERE package_id=?`,
	} {
		require.NoError(t, st.DB.Exec(statement, id).Error, name)
		refreshPages(t, st)
		require.Equal(t, first, pageRowOf(t, st, id, "overview"), name)
	}
	// A column added later reaches no page until pageFields lists it.
	require.NoError(t, st.DB.Exec(`ALTER TABLE package_meta ADD COLUMN later_column TEXT NOT NULL DEFAULT 'value'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE package_meta SET later_column='changed' WHERE package_id=?`, id).Error)
	refreshPages(t, st)
	require.Equal(t, first, pageRowOf(t, st, id, "overview"))

	previous := first
	for name, statement := range map[string]string{
		"price amount":        `UPDATE package_facts SET price=price||'{"amount":12.99}' WHERE package_id=?`,
		"price record amount": `UPDATE package_fact_records SET value=value||'{"amount":"12.99"}' WHERE package_id=?`,
		"price record region": `UPDATE package_fact_records SET value=value||'{"region":"DE"}' WHERE package_id=?`,
		"developer":           `UPDATE package_meta SET developer='Example GmbH' WHERE package_id=?`,
	} {
		require.NoError(t, st.DB.Exec(statement, id).Error, name)
		var changedAt time.Time
		require.NoError(t, st.DB.Raw("SELECT changed_at FROM package_page_dirty WHERE package_id=?", id).Scan(&changedAt).Error, name)
		refreshPages(t, st)
		current := pageRowOf(t, st, id, "overview")
		require.NotEqual(t, previous.ContentHash, current.ContentHash, name)
		require.Equal(t, changedAt, current.ContentModifiedAt, name)
		previous = current
	}
}

// Raising pageHashVersion while every app has a dirty job (a migration or sync touched them all) keeps every date;
// a page that really changed after its date takes the time of that change, never the job's.
func TestPageHashUpgradeKeepsDatesUnlessShownChanges(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ids := []int64{pagePackage(t, st, "quiet-a"), pagePackage(t, st, "quiet-b"), pagePackage(t, st, "quiet-c")}
	changed := pagePackage(t, st, "released")
	edited := pagePackage(t, st, "edited")
	seoEdited := pagePackage(t, st, "seo-edited")
	refreshPages(t, st)
	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, st.DB.Exec(`UPDATE package_page_state SET content_hash_version=2,content_modified_at=?`, old).Error)
	// A real version and a real text edit after the old dates, and one long before them.
	versionAt := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET version='2.0',version_base='2.0',version_changed_at=? WHERE id=?`, versionAt, changed).Error)
	textAt := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	require.NoError(t, st.DB.Exec(`UPDATE package_i18n SET summary='A faster editor',updated_at=? WHERE package_id=?`, textAt, edited).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET version_changed_at='2026-09-01' WHERE id=?`, ids[0]).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_seo_overrides(package_id,locale,title,updated_at,updated_by) VALUES(?,'en-US','Edited search title',?,'system')`, seoEdited, textAt).Error)
	// Every app has a dirty job from now.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_page_dirty(package_id,changed_at) SELECT id,now() FROM packages
 ON CONFLICT(package_id) DO UPDATE SET changed_at=now()`).Error)
	refreshPages(t, st)

	for _, id := range ids {
		for _, kind := range []string{"overview", "details", "dependencies"} {
			row := pageRowOf(t, st, id, kind)
			require.Equal(t, 4, row.ContentHashVersion)
			require.True(t, row.ContentModifiedAt.Equal(old), "%d %s: %s", id, kind, row.ContentModifiedAt)
		}
	}
	for _, kind := range []string{"overview", "details", "dependencies"} {
		require.True(t, pageRowOf(t, st, changed, kind).ContentModifiedAt.Equal(versionAt), kind)
	}
	require.True(t, pageRowOf(t, st, edited, "overview").ContentModifiedAt.Equal(textAt))
	require.True(t, pageRowOf(t, st, edited, "details").ContentModifiedAt.Equal(textAt))
	require.True(t, pageRowOf(t, st, edited, "dependencies").ContentModifiedAt.Equal(old), "the text is not on the dependencies page")
	require.True(t, pageRowOf(t, st, seoEdited, "overview").ContentModifiedAt.Equal(textAt))
	require.True(t, pageRowOf(t, st, seoEdited, "details").ContentModifiedAt.Equal(old))

}
