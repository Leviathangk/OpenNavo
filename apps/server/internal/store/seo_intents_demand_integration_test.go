//go:build integration

package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestIntentDemandAcceptsRecentConfirmedSearchObservations(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,artifacts)
 VALUES(973,'cask','fixture-demand','fixture-demand','homebrew/cask','Fixture Demand','1','1','{}','fixture',
 '{"entries":[{"phase":"uninstall","declaration":{"uninstall":[{"pkgutil":"com.fixture.example"}]}}]}')`).Error)
	for n := 0; n < 3; n++ {
		id := 974 + n
		require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES(?,'cask',?,?,'homebrew/cask','Fixture Alternative','1','1','{}','fixture')`, id, fmt.Sprint("fixture-alt-", n), fmt.Sprint("fixture-alt-", n)).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,source) VALUES(973,?,?,ARRAY['open_source'],'admin')`, id, n).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,'attribute','open-source','{"name":"open_source","booleanValue":true}',
 jsonb_build_object('sourceUrl','https://vendor.fixture.example/docs','checkedAt',now()-interval '1 day','validUntil',now()+interval '30 days','status','verified'))`, id).Error)
	}
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keywords(id,keyword,normalized,source,engine,country,language,device,period,volume_state,observed_at)
 VALUES(973,'fixture demand','fixture demand','gsc','google','ALL','en-US','all','2026-10','unknown',now());
 INSERT INTO seo_keyword_links(keyword_id,package_id,intent,confidence,state) VALUES(973,973,'alternative',1,'confirmed'),(973,973,'uninstall',1,'confirmed');
 INSERT INTO seo_targets(entity_key,intent,locale,target_url) VALUES
 ('pkg:973','alternative','en-US','https://fixture.example/apps/fixture-demand/alternatives'),
 ('pkg:973','uninstall','en-US','https://fixture.example/apps/fixture-demand/uninstall')`).Error)
	position, volume := 4.0, int64(10)
	for _, tc := range []struct {
		name, source, state, locale, volumeState string
		position                                 *float64
		volume                                   *int64
		days                                     int
		anomaly                                  *string
		want                                     bool
	}{
		{name: "gsc", source: "gsc", position: &position, days: -1, want: true},
		{name: "bing", source: "bing", position: &position, days: -59, want: true},
		{name: "old", source: "gsc", position: &position, days: -61},
		{name: "future", source: "bing", position: &position, days: 1},
		{name: "no_position", source: "gsc", days: -1},
		{name: "wrong_source", source: "homebrew", position: &position, days: -1},
		{name: "unconfirmed", source: "bing", position: &position, days: -1, state: "rejected"},
		{name: "other_locale", source: "gsc", position: &position, days: -1, locale: "de-DE"},
		{name: "paid_volume_preserved", source: "openseo:dataforseo", volumeState: "known", volume: &volume, days: -90, want: true},
		{name: "unknown_paid_volume", source: "openseo:dataforseo", days: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, locale, volumeState := tc.state, tc.locale, tc.volumeState
			if state == "" {
				state = "confirmed"
			}
			if locale == "" {
				locale = "en-US"
			}
			if volumeState == "" {
				volumeState = "unknown"
			}
			require.NoError(t, st.DB.Exec(`UPDATE seo_keywords SET source=?,language=?,volume_state=?,search_volume=?,our_position=?,observed_at=clock_timestamp()+?*interval '1 day',anomaly=? WHERE id=973`, tc.source, locale, volumeState, tc.volume, tc.position, tc.days, tc.anomaly).Error)
			require.NoError(t, st.DB.Exec(`UPDATE seo_keyword_links SET state=? WHERE keyword_id=973`, state).Error)
			row, found, err := st.IndexRowByID(ctx, 973)
			require.NoError(t, err)
			require.True(t, found)
			rows := []store.IndexRow{row}
			require.NoError(t, st.PopulateIntentEligibility(ctx, rows, seo.Policy{IntentPagesEnabled: true}, "https://fixture.example"))
			for _, kind := range []seo.PageKind{seo.PageAlternatives, seo.PageUninstall} {
				if tc.want {
					require.Equal(t, []string{"en-US"}, rows[0].IntentLocales[kind])
				} else {
					require.Empty(t, rows[0].IntentLocales[kind])
				}
			}
		})
	}
}
