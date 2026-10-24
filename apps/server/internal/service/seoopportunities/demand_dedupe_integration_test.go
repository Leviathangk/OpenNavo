//go:build integration

package seoopportunities

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDailySearchMeasurementsDoNotDuplicateOrReopenPageTasks(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale) VALUES
 (971,'https://fixture.example/topics/fixture','article','en-US'),
 (972,'https://fixture.example/de/topics/fixture','article','de-DE')`).Error)
	importDay := func(daysAgo int) {
		t.Helper()
		require.NoError(t, st.DB.Exec(`UPDATE seo_data_watermarks SET complete_through=current_date-?::int WHERE source='gsc_query'`, daysAgo).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at)
 VALUES('gsc_query',current_date-?::int,true,true,4,'fixture',now())`, daysAgo).Error)
		require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_query_daily(day,url_id,query,clicks,impressions,position_sum)
 SELECT current_date-?::int,id,query,1,10,20 FROM seo_urls CROSS JOIN (VALUES('Best Mac Fixture Tools'),('fixture utility mac')) q(query)`, daysAgo).Error)
		_, err := svc.Run(ctx)
		require.NoError(t, err)
	}
	importDay(5)
	type task struct {
		ID                                           int64
		Kind, Locale, State, EvidenceHash, DedupeKey string
		PendingEvidenceHash                          *string
	}
	read := func() []task {
		var rows []task
		require.NoError(t, st.DB.Table("seo_tasks").Where("kind IN ('topic','listing')").Order("kind,locale").Find(&rows).Error)
		return rows
	}
	first := read()
	require.Len(t, first, 4, "same page in two locales is distinct; topic and listing both get tasks")
	for _, row := range first {
		require.Contains(t, row.DedupeKey, "demand:"+row.Kind+":"+row.Locale+":")
	}
	require.NoError(t, st.DB.Exec(`UPDATE seo_tasks SET state='skipped' WHERE kind='topic';
 UPDATE seo_tasks SET state='leased',lease_owner='fixture-owner',lease_until=now()+interval '1 hour' WHERE kind='listing'`).Error)
	before := read()
	importDay(4)
	after := read()
	require.Equal(t, before, after, "new row IDs must neither reopen completed work nor stash daily evidence on a lease")
	var count int64
	require.NoError(t, st.DB.Table("seo_keywords").Count(&count).Error)
	require.EqualValues(t, 8, count, "daily observations are still stored")
	// Release the lease and finish; another day still must not reopen either kind.
	require.NoError(t, st.DB.Exec(`UPDATE seo_tasks SET state='done',lease_owner=NULL,lease_until=NULL WHERE kind='listing'`).Error)
	before = read()
	importDay(3)
	require.Equal(t, before, read())
}

func TestPageDemandReusesLegacyTaskAndDoesNotRepeatSamePage(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_keywords(keyword,normalized,source,engine,country,language,device,period,volume_state,our_position,observed_at)
 VALUES('Best mac tools','best mac tools','gsc','google','ALL','en-US','all','2026-10-20','unknown',2,now()),
 ('BEST MAC TOOLS','best mac tools','bing','bing','ALL','en-US','all','2026-10-21','unknown',3,now());
 INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,state,evidence,evidence_hash,rule_version)
 VALUES('topic','article:new:best-mac-tools','en-US','demand:123','skipped','{"source":"keywords","keywordId":123,"keyword":"Best mac tools","targetSlug":"best-mac-tools"}','fixture','opportunities-v1')`).Error)
	// Use the application's canonical evidence hash, as legacy rows already do.
	var legacy signal
	require.NoError(t, st.DB.Raw(`SELECT kind,entity_key,locale,dedupe_key,priority,evidence FROM seo_tasks WHERE dedupe_key='demand:123'`).Scan(&legacy).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_tasks SET evidence_hash=? WHERE dedupe_key='demand:123'`, seotasks.Hash(legacy.Evidence)).Error)
	_, err := svc.Run(context.Background())
	require.NoError(t, err)
	var rows []struct{ DedupeKey, State string }
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='topic'").Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "demand:123", rows[0].DedupeKey)
	require.Equal(t, "skipped", rows[0].State)
}
