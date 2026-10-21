//go:build integration

package seoopportunities

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/service/seotasks"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestOpportunitiesReplayWatermarkAndEvidenceChange(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','fixture-editor','fixture-editor','homebrew/cask','Fixture Editor','1.0','1.0','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(701,'cask','fixture-editor','upsert','homebrew')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale,package_id) VALUES(701,'https://fixture.example/apps/fixture-editor','overview','en-US',701)`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_audit_issues(rule,url_id,layer,severity,first_seen,last_seen) VALUES('canonical',701,'edge','p0',now(),now())`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_watermark_events(source,complete_through) VALUES('gsc_page',current_date-3)`).Error)
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='maintenance'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='tech_alert'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='maintenance'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Exec(`UPDATE seo_tasks SET state='deferred',next_retry_at=now()+interval '7 days' WHERE kind='maintenance'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET version='2.0' WHERE id=701`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(701,'cask','fixture-editor','upsert','homebrew')`).Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	var state string
	require.NoError(t, st.DB.Raw("SELECT state FROM seo_tasks WHERE kind='maintenance'").Scan(&state).Error)
	require.Equal(t, "open", state)
	require.NoError(t, st.DB.Table("seo_watermark_events").Where("consumed_at IS NULL").Count(&count).Error)
	require.Zero(t, count)
}

func TestMeasurementsRequireCompleteQueryDataAndPreserveUnknownVolume(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale) VALUES(702,'https://fixture.example/apps/measured','overview','de-DE')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_data_watermarks(source,complete_through) VALUES('gsc_query',current_date-3),('bing',NULL) ON CONFLICT(source) DO UPDATE SET complete_through=EXCLUDED.complete_through`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at) VALUES('gsc_query',current_date-3,true,true,2,'fixture',now()),('gsc_query',current_date-4,true,false,1,'fixture',now())`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_query_daily(day,url_id,query,clicks,impressions,position_sum,anomaly) VALUES(current_date-3,702,'Fixture mac preis',2,100,500,NULL),(current_date-3,702,'Fixture spam123456',0,999,999,'digit_suffix_burst'),(current_date-4,702,'incomplete query',1,100,100,NULL)`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_bing_daily(day,dimension,value,clicks,impressions,position_sum) VALUES(current_date-3,'query','bing mac demand',1,500,2000)`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO search_queries(query,normalized,locale,platform,result_count) VALUES('new mac app','new mac app','en-US','web',0),('new mac app','new mac app','en-US','desktop',0)`).Error)
	require.NoError(t, svc.measurements(ctx, st.DB))
	var records []struct {
		Source, Language, VolumeState string
		SearchVolume                  *int64
		OurPosition                   *float64
	}
	require.NoError(t, st.DB.Table("seo_keywords").Order("source").Find(&records).Error)
	require.Len(t, records, 2)
	require.Equal(t, "gsc", records[0].Source)
	require.Equal(t, "de-DE", records[0].Language)
	require.Nil(t, records[0].SearchVolume)
	require.Equal(t, "unknown", records[0].VolumeState)
	require.Equal(t, 5.0, *records[0].OurPosition)
	require.EqualValues(t, 2, *records[1].SearchVolume)
	var before, after int64
	require.NoError(t, st.DB.Raw("SELECT revision FROM seo_entity_revisions WHERE entity_key='site:keywords'").Scan(&before).Error)
	require.NoError(t, svc.measurements(ctx, st.DB))
	require.NoError(t, st.DB.Raw("SELECT revision FROM seo_entity_revisions WHERE entity_key='site:keywords'").Scan(&after).Error)
	require.Equal(t, before, after, "unchanged watermarks must not rewrite the ledger")
	require.NoError(t, st.DB.Exec("UPDATE seo_data_watermarks SET complete_through=current_date-3 WHERE source='bing'").Error)
	require.NoError(t, svc.measurements(ctx, st.DB))
	var count int64
	require.NoError(t, st.DB.Table("seo_keywords").Where("source='bing' AND volume_state='unknown' AND search_volume IS NULL").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("seo_gsc_query_daily").Where("anomaly IS NOT NULL").Count(&count).Error)
	require.EqualValues(t, 1, count, "raw anomalies remain available for audit")
}

func TestLeasedOpportunityRetainsCatalogEvent(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(701,'cask','leased-editor','leased-editor','homebrew/cask','Editor','1.0','1.0','{}','fixture')`).Error)
	event := func() {
		require.NoError(t, st.DB.Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(701,'cask','leased-editor','upsert','homebrew')`).Error)
	}
	event()
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	tasks := &seotasks.Service{DB: st.DB}
	claimed, err := tasks.Claim(ctx, "test-owner", 1, []string{"maintenance"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	var before, after int64
	require.NoError(t, st.DB.Raw("SELECT cursor FROM seo_opportunity_cursors WHERE source='catalog_changes'").Scan(&before).Error)
	require.NoError(t, st.DB.Exec("UPDATE packages SET version='2.0' WHERE id=701").Error)
	event()
	_, err = svc.Run(ctx)
	require.Error(t, err)
	require.NoError(t, st.DB.Raw("SELECT cursor FROM seo_opportunity_cursors WHERE source='catalog_changes'").Scan(&after).Error)
	require.Equal(t, before, after)
	_, err = tasks.Complete(ctx, claimed[0].ID, claimed[0].Fencing, "test-owner", "skipped", "already reviewed")
	require.NoError(t, err)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	task, err := tasks.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, "open", task.State)
	require.Contains(t, string(task.Evidence), "2.0")
}

func TestMeasurementsAcceptHomebrewSnapshotDatedAheadOfUTC(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	// The 03:00 Asia/Shanghai analytics sync dates its snapshot one day ahead of UTC.
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(703,'cask','fixture-player','fixture-player','homebrew/cask','Fixture Player','1.0','1.0','{}','fixture');
 INSERT INTO analytics_snapshots(package_id,snapshot_date,installs_30d,installs_90d,installs_365d,on_request_30d,rank_30d) VALUES(703,(now() AT TIME ZONE 'UTC')::date+1,120,300,900,100,1)`).Error)
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	var observed struct {
		Period   string
		Future   bool
		Keywords int64
	}
	require.NoError(t, st.DB.Raw(`SELECT max(period) period,bool_or(observed_at>now()) future,count(*) keywords FROM seo_keywords WHERE source='homebrew'`).Scan(&observed).Error)
	require.EqualValues(t, 1, observed.Keywords)
	require.False(t, observed.Future)
}

func TestInstallPriorityIsWrittenOnlyWhenItRises(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(704,'cask','fixture-popular','fixture-popular','homebrew/cask','Fixture Popular','1.0','1.0','{}','fixture');
 INSERT INTO analytics_snapshots(package_id,snapshot_date,installs_30d,installs_90d,installs_365d,on_request_30d,rank_30d) VALUES(704,(now() AT TIME ZONE 'UTC')::date,500,900,2000,400,1);
 INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES('translate','package:704','de-DE','translate:fixture-popular:de-DE',10,'{}','fixture','translation-v1')`).Error)
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	var first struct {
		Priority float64
		Xmin     string
	}
	require.NoError(t, st.DB.Raw("SELECT priority,xmin::text xmin FROM seo_tasks WHERE dedupe_key='translate:fixture-popular:de-DE'").Scan(&first).Error)
	require.Greater(t, first.Priority, 16.0)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	var second string
	require.NoError(t, st.DB.Raw("SELECT xmin::text FROM seo_tasks WHERE dedupe_key='translate:fixture-popular:de-DE'").Scan(&second).Error)
	require.Equal(t, first.Xmin, second, "an unchanged priority must not rewrite the task")
}

func TestSkippedArticleRefreshIsRetriedAfterAWeek(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB}
	require.NoError(t, st.DB.Exec(`INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) VALUES(9101,'legacy-fixture','published',9101,9101,now());
 INSERT INTO article_revisions(id,article_id,number,source_locale,type,published_at) VALUES(9101,9101,1,'zh-CN','tutorial',now())`).Error)
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	state := func() string {
		var s string
		require.NoError(t, st.DB.Raw("SELECT state FROM seo_tasks WHERE dedupe_key='article-refresh:9101:9101'").Scan(&s).Error)
		return s
	}
	require.Equal(t, "open", state())
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state='skipped',updated_at=now()-interval '2 days' WHERE dedupe_key='article-refresh:9101:9101'").Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, "skipped", state(), "a fresh skip is not retried at once")
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET updated_at=now()-interval '8 days' WHERE dedupe_key='article-refresh:9101:9101'").Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, "open", state(), "a week later the article is tried again")
	require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state='quarantined',updated_at=now()-interval '8 days' WHERE dedupe_key='article-refresh:9101:9101'").Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, "quarantined", state(), "quarantined refreshes wait for a person")
	require.NoError(t, st.DB.Exec(`UPDATE seo_tasks SET state='skipped',updated_at=now()-interval '8 days' WHERE dedupe_key='article-refresh:9101:9101';
 UPDATE article_revisions SET claim_evidence='[{"evidenceId":1}]'::jsonb WHERE id=9101`).Error)
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, "skipped", state(), "an article republished with receipts needs no retry")
}
