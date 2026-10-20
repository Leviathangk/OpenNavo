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

func TestPageContentDatesTrackChangesAndDeletions(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	add := func(token string) int64 {
		var id int64
		require.NoError(t, st.DB.Raw(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,first_seen_at) VALUES('cask',?,?, 'homebrew/cask',?,'1.0','1.0','{}','fixture','en-US','2020-01-01') RETURNING id`, token, token, token).Scan(&id).Error)
		return id
	}
	id, other := add("example"), add("alternative")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale) VALUES(?,'en-US','Example','An editor','A useful editor','source','human','en-US'),(?,'de-DE','Beispiel','Ein Editor','Ein hilfreicher Editor','manual','human','en-US')`, id, id).Error)
	refresh := func() {
		count, err := st.RefreshPackagePageStates(ctx, 100)
		require.NoError(t, err)
		require.Positive(t, count)
	}
	date := func(locale, kind string) time.Time {
		var row store.PageState
		require.NoError(t, st.DB.Raw(`SELECT * FROM package_page_state WHERE package_id=? AND locale=? AND page_kind=? AND page_number=1`, id, locale, kind).Scan(&row).Error)
		require.False(t, row.ContentModifiedAt.IsZero())
		return row.ContentModifiedAt
	}
	refresh()
	first := date("en-US", "overview")
	require.NoError(t, st.DB.Exec("UPDATE packages SET updated_at=now(),installs_30d=installs_30d+1 WHERE id=?", id).Error)
	require.NoError(t, st.DB.Exec("UPDATE package_i18n SET updated_at=now() WHERE package_id=?", id).Error)
	count, err := st.RefreshPackagePageStates(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, count, "metadata-only updates must not requeue page hashing")
	require.Equal(t, first, date("en-US", "overview"))
	german := date("de-DE", "overview")
	require.NoError(t, st.DB.Exec("UPDATE package_i18n SET description='A newly documented feature' WHERE package_id=? AND locale='en-US'", id).Error)
	refresh()
	require.True(t, date("en-US", "overview").After(first))
	require.Equal(t, german, date("de-DE", "overview"))
	previous := date("en-US", "overview")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_facts(package_id,price) VALUES(?, '{"type":"paid","status":"verified","sourceUrl":"https://vendor.example/pricing","verifiedAt":"2026-01-01T00:00:00Z"}')`, id).Error)
	refresh()
	require.True(t, date("en-US", "overview").After(previous))
	previous = date("en-US", "overview")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(?,?,0,ARRAY['similar'],'admin')`, id, other).Error)
	refresh()
	require.True(t, date("en-US", "overview").After(previous))
	previous = date("en-US", "overview")
	require.NoError(t, st.DB.Exec(`DELETE FROM package_alternatives WHERE package_id=?`, id).Error)
	refresh()
	require.True(t, date("en-US", "overview").After(previous))
	var assetID int64
	require.NoError(t, st.DB.Raw(`INSERT INTO assets(kind,storage_key,url,mime,bytes,sha256) VALUES('screenshot','fixture-shot','https://images.example/shot.png','image/png',100,'fixture') RETURNING id`).Scan(&assetID).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_screenshots(package_id,asset_id) VALUES(?,?)`, id, assetID).Error)
	refresh()
	previous = date("en-US", "overview")
	require.NoError(t, st.DB.Exec(`DELETE FROM package_screenshots WHERE package_id=?`, id).Error)
	refresh()
	require.True(t, date("en-US", "overview").After(previous))
	versions := date("en-US", "versions")
	require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,body_markdown,source_url,source_locale) VALUES(?,'manual','1.0','1.0','New release notes','https://vendor.example/notes','en-US')`, id).Error)
	refresh()
	require.True(t, date("en-US", "versions").After(versions))
	versions = date("en-US", "versions")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_version_requirements(package_id,app_version,platforms,source_url,checked_at)
 VALUES(?,'1.0','[{"version":"1.0","arch":"arm64","minMacos":"14"}]','https://vendor.example/requirements',now())`, id).Error)
	refresh()
	require.True(t, date("en-US", "versions").After(versions), "published compatibility belongs to the version page content")
	previous = date("en-US", "overview")
	require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'attribute','offline','{"name":"offline","value":true}',
 '{"status":"verified","sourceUrl":"https://vendor.example/features","checkedAt":"2020-01-01T00:00:00Z","validUntil":"2020-02-01T00:00:00Z"}')`, id).Error)
	refresh()
	require.True(t, date("en-US", "overview").After(previous))
	count, err = st.RefreshPackagePageStates(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, count, "already expired evidence is inspected once without endlessly requeuing")
}
