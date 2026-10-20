//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func seedEvaluatedPackage(t *testing.T, svc *Service) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	db := svc.Store.DB
	require.NoError(t, db.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(709,'cask','guard-fixture','guard-fixture','homebrew/cask','Guard Fixture','1.0','1.0','{}','fixture')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO package_meta(package_id,notes) VALUES(709,'Original Mac utility')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale,package_id) VALUES(709,'https://fixture.example/apps/guard-fixture','overview','en-US',709)`).Error)
	tasks := &seotasks.Service{DB: db}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "maintenance", EntityKey: "pkg:709", DedupeKey: "guard-fixture", RuleVersion: "test", Evidence: map[string]any{}})
	require.NoError(t, err)
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	evidence := agentgate.Evidence{EntityKey: "pkg:709", SourceURL: "https://vendor.example.test", FinalURL: "https://vendor.example.test", BodyText: "Mac utility", Excerpt: "Mac utility", BodyHash: seotasks.Hash([]byte("Mac utility")), FetchedAt: time.Now(), ValidUntil: time.Now().Add(time.Hour), Authority: "official", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: seotasks.Hash([]byte("fixture"))}
	require.NoError(t, db.Create(&evidence).Error)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "updatePackageMeta", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"catalog:package:edit"}, RequiredPermissions: []string{"catalog:package:edit"}, Execution: &domain.AgentExecution{TaskID: leased[0].ID, Fencing: leased[0].Fencing, IdempotencyKey: uuid.NewString(), EvidenceIDs: []int64{evidence.ID}}}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdatePackageMeta", Input{ID: 709, Body: map[string]any{"notes": "Changed Mac utility", "downloadSize": int64(4096)}})
	require.NoError(t, err)
	_, err = tasks.Complete(ctx, leased[0].ID, leased[0].Fencing, seotasks.Owner(1, 2), "done", "")
	require.NoError(t, err)
	var action int64
	require.NoError(t, db.Raw("SELECT id FROM seo_actions WHERE task_id=?", leased[0].ID).Scan(&action).Error)
	require.Positive(t, action)
	return action, evidence.ID
}

func TestEvaluateSEOTechnicalCompensationAndExpiredEvidence(t *testing.T) {
	for _, scenario := range []string{"atomic compensation", "expired evidence defers", "proposal defers"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewSEOStore(t)
			svc := &Service{Store: st}
			action, evidence := seedEvaluatedPackage(t, svc)
			require.NoError(t, st.DB.Exec(`INSERT INTO seo_audit_issues(rule,url_id,layer,severity,first_seen,last_seen) VALUES('canonical',709,'edge','p0',now(),now())`).Error)
			require.NoError(t, seobudget.Open(ctx, st.DB, "default", "technical", "*", "fixture", map[string]any{}, nil))
			if scenario == "expired evidence defers" {
				require.NoError(t, st.DB.Exec("UPDATE seo_evidence_snapshots SET valid_until=clock_timestamp()-interval '1 second' WHERE id=?", evidence).Error)
			}
			if scenario == "proposal defers" {
				require.NoError(t, st.DB.Exec("UPDATE seo_settings SET automation=jsonb_set(automation,'{actions,compensate,mode}','\"proposal\"')").Error)
			}
			result, err := svc.EvaluateSEO(ctx)
			require.NoError(t, err)
			var count int64
			require.NoError(t, st.DB.Table("seo_actions").Where("id=? AND compensated_at IS NOT NULL", action).Count(&count).Error)
			var notes string
			require.NoError(t, st.DB.Raw("SELECT notes FROM package_meta WHERE package_id=709").Scan(&notes).Error)
			if scenario != "atomic compensation" {
				require.EqualValues(t, 1, result["deferred"])
				require.Zero(t, count)
				require.Equal(t, "Changed Mac utility", notes)
			} else {
				require.EqualValues(t, 1, result["compensated"])
				require.EqualValues(t, 1, count)
				require.Equal(t, "Original Mac utility", notes)
				require.NoError(t, st.DB.Table("seo_daily_entities").Where("action='compensate'").Count(&count).Error)
				require.EqualValues(t, 1, count)
				require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
				require.Positive(t, count)
			}
			result, err = svc.EvaluateSEO(ctx)
			require.NoError(t, err)
			require.Zero(t, result["compensated"], "retries cannot compensate twice")
		})
	}
}

func TestEvaluateSEOIncompleteWindowAndTrafficDeclineNeverRollback(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	action, _ := seedEvaluatedPackage(t, svc)
	require.NoError(t, st.DB.Exec("UPDATE seo_actions SET applied_at=current_date-30 WHERE id=?", action).Error)
	require.NoError(t, st.DB.Exec("UPDATE seo_data_watermarks SET complete_through=current_date-2 WHERE source='gsc_page'").Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at) SELECT 'gsc_page',d,true,d<>current_date-2,1,'fixture',now() FROM generate_series(current_date-58,current_date-2,interval '1 day') d WHERE d<>current_date-30`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_page_daily(day,url_id,country,device,clicks,impressions,position_sum) SELECT d,709,'USA','DESKTOP',CASE WHEN d<current_date-30 THEN 10 ELSE 1 END,CASE WHEN d<current_date-30 THEN 100 ELSE 10 END,1000 FROM generate_series(current_date-58,current_date-2,interval '1 day') d WHERE d<>current_date-30`).Error)
	_, err := svc.EvaluateSEO(ctx)
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("seo_action_guards").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Exec("UPDATE seo_ingest_days SET retrieval_complete=true").Error)
	_, err = svc.EvaluateSEO(ctx)
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_action_guards").Where("outcome='degraded'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='tech_alert'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("seo_actions").Where("compensated_at IS NOT NULL").Count(&count).Error)
	require.Zero(t, count)
}

func TestMissingDesktopReleaseTranslationsUseAppLocales(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	require.NoError(t, st.DB.Exec("INSERT INTO desktop_releases(id,version,artifacts,source_locale) VALUES(901,'1.0','[]','en-US')").Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO desktop_release_i18n(desktop_release_id,locale,notes,status,source_locale)
SELECT 901,locale,'Release notes','manual','en-US' FROM unnest(ARRAY['en-US','zh-CN','ja-JP','es-ES','pt-BR','ru-RU']) locale`).Error)
	for _, locale := range []string{"", "de-DE"} {
		result, err := svc.Execute(ctx, "ListTranslations", Input{Params: map[string]any{"entity": "desktop_release", "status": "missing", "locale": locale}})
		require.NoError(t, err)
		require.Zero(t, result.(map[string]any)["total"])
	}
	require.NoError(t, st.DB.Exec("DELETE FROM desktop_release_i18n WHERE desktop_release_id=901 AND locale='ja-JP'").Error)
	result, err := svc.Execute(ctx, "ListTranslations", Input{Params: map[string]any{"entity": "desktop_release", "status": "missing"}})
	require.NoError(t, err)
	require.EqualValues(t, 1, result.(map[string]any)["total"])
}
