//go:build integration

package seoexperiments

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExperimentSampleGroupsRecrawlAndCooldown(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) SELECT i,'cask','experiment-'||i,'experiment-'||i,'homebrew/cask','Example Editor','1.0','1.0','{}','fixture' FROM generate_series(701,704) i`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source_locale,source) SELECT id,'en-US','Example Editor','A text editor for macOS.','A text editor for macOS that supports editing documents and managing files.','source','en-US','human' FROM packages WHERE id BETWEEN 701 AND 704`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale,package_id) SELECT id,'https://fixture.example/apps/'||token,'overview','en-US',id FROM packages WHERE id BETWEEN 701 AND 704`).Error)
	p := Proposal{Variant: "current_version", Slots: []string{"name", "version"}, PageKind: "overview", Locale: "en-US", BaselineCTR: .01, TargetCTR: .02}
	svc := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	err := st.DB.Transaction(func(tx *gorm.DB) error { _, err := svc.Start(ctx, tx, p); return err })
	require.ErrorIs(t, err, ErrSample)
	require.NoError(t, st.DB.Exec(`UPDATE seo_data_watermarks SET complete_through=(now() AT TIME ZONE 'UTC')::date-3 WHERE source='gsc_page'`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at) SELECT 'gsc_page',d,true,true,4,'complete',now() FROM generate_series((now() AT TIME ZONE 'UTC')::date-30,(now() AT TIME ZONE 'UTC')::date-3,interval '1 day') d`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_page_daily(day,url_id,country,device,clicks,impressions,position_sum) SELECT d,u.id,'US','desktop',10,1000,8000 FROM seo_urls u CROSS JOIN generate_series((now() AT TIME ZONE 'UTC')::date-30,(now() AT TIME ZONE 'UTC')::date-3,interval '1 day') d`).Error)
	var e Experiment
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { var err error; e, err = svc.Start(ctx, tx, p); return err }))
	var groups []struct {
		Arm   int
		Count int64
	}
	require.NoError(t, st.DB.Raw("SELECT arm,count(*) count FROM seo_experiment_pages GROUP BY arm ORDER BY arm").Scan(&groups).Error)
	require.Len(t, groups, 2)
	require.EqualValues(t, 2, groups[0].Count)
	require.EqualValues(t, 2, groups[1].Count)
	a, err := svc.Assignment(ctx, "https://fixture.example/apps/experiment-701")
	require.NoError(t, err)
	b, err := svc.Assignment(ctx, "https://fixture.example/apps/experiment-701")
	require.NoError(t, err)
	require.Equal(t, a, b)
	require.Equal(t, true, a["eligible"])
	require.NoError(t, st.DB.Exec("UPDATE packages SET version='' WHERE id=701").Error)
	a, err = svc.Assignment(ctx, "https://fixture.example/apps/experiment-701")
	require.NoError(t, err)
	require.Equal(t, false, a["eligible"])
	_, err = svc.Checkpoints(ctx)
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("seo_experiment_checkpoints").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Exec("UPDATE seo_experiments SET started_at=now()-interval '43 days' WHERE id=?", e.ID).Error)
	_, err = svc.Checkpoints(ctx)
	require.NoError(t, err)
	var state string
	require.NoError(t, st.DB.Raw("SELECT state FROM seo_experiments WHERE id=?", e.ID).Scan(&state).Error)
	require.Equal(t, "inconclusive", state)
	require.NoError(t, st.DB.Table("seo_experiment_pages").Where("active").Count(&count).Error)
	require.Zero(t, count)
	err = st.DB.Transaction(func(tx *gorm.DB) error { _, err := svc.Start(ctx, tx, p); return err })
	require.ErrorIs(t, err, ErrConflict)
}
