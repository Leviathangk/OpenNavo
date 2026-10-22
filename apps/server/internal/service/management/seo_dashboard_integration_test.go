//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func dashboardJSON(t *testing.T, schema *openapi3.Schema, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NoError(t, schema.VisitJSON(decoded), string(raw))
	return decoded
}

func TestSEODashboardOverviewMatchesConcreteContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	doc, err := adminapi.GetSpec()
	require.NoError(t, err)
	schema := doc.Components.Schemas["SeoOverview"].Value
	read := func() map[string]any {
		t.Helper()
		value, err := s.Execute(ctx, "GetSeoOverview", Input{})
		require.NoError(t, err)
		return dashboardJSON(t, schema, value)
	}
	empty := read()
	require.Equal(t, false, empty["automationReady"])
	require.Empty(t, empty["breakers"])
	require.Empty(t, empty["yesterday"].(map[string]any)["actions"])
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_daily_entities(site,day,action,entity_key,first_request_id)
 VALUES('fixture.example',(now() AT TIME ZONE 'UTC')::date,'listing','pkg:1','fixture-one'),
 ('other.example',(now() AT TIME ZONE 'UTC')::date,'listing','pkg:2','fixture-two')`).Error)
	require.NoError(t, seobudget.Record(ctx, st.DB, "fixture.example", seobudget.Receipt{VendorTaskID: "fixture-overview-receipt", CostUSD: "16", IncurredAt: time.Now().UTC()}))
	require.NoError(t, seobudget.Open(ctx, st.DB, "fixture.example", "technical", "*", "audit_p0", map[string]any{"unresolved": 2, "layers": []string{"origin", "edge"}}, nil))
	require.NoError(t, seobudget.Open(ctx, st.DB, "fixture.example", "business", "*", "complete_window_decline", map[string]any{"Days": 56, "OldClicks": 200, "NewClicks": 50, "OldImpressions": 2000, "NewImpressions": 500}, nil))
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_breakers(site,kind,scope,reason,evidence,retry_at,closed_at) VALUES
 ('fixture.example','technical','expired','fixture','{}',now()-interval '1 minute',NULL),
 ('fixture.example','technical','closed','fixture','{}',NULL,now()),
 ('other.example','technical','*','fixture','{}',NULL,NULL)`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_tasks(id,kind,entity_key,dedupe_key,rule_version)
 VALUES(9901,'fixture','pkg:1','overview-fixture','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_actions(task_id,idempotency_key,request_hash,owner,mode,operation,object,object_id,fields,before_data,after_data,base_revision,evidence_ids,rule_version,playbook_version,result,created_at,compensated_at)
 SELECT 9901,'overview-fixture-'||n,repeat('a',64),'fixture',CASE WHEN n=3 THEN 'proposal' ELSE 'live' END,
 'UpdatePackageMeta','package','pkg:1','{}','{}','{}',0,'{}','fixture','fixture','{}',
 CASE WHEN n=4 THEN now() ELSE (date_trunc('day',now() AT TIME ZONE 'UTC')-interval '1 hour') AT TIME ZONE 'UTC' END,
 CASE WHEN n=1 THEN now() END FROM generate_series(1,4)n`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_gate_events(request_id,owner,operation,rule_version,result,reason,created_at) VALUES('direct-write','fixture','UpdatePackageMeta','fixture','live','validated',(date_trunc('day',now() AT TIME ZONE 'UTC')-interval '1 hour') AT TIME ZONE 'UTC');
 INSERT INTO seo_data_watermarks(source,complete_through,fresh_through) VALUES('gsc_page',current_date,current_date) ON CONFLICT(source) DO UPDATE SET complete_through=current_date,fresh_through=current_date`).Error)
	view := read()
	require.Equal(t, true, view["automationReady"])
	require.Len(t, view["breakers"], 3, "exclude closed, expired and other-site breakers")
	for _, raw := range view["budgets"].([]any) {
		row := raw.(map[string]any)
		switch row["action"] {
		case "listing":
			require.EqualValues(t, 1, row["used"])
			require.EqualValues(t, 19, row["remaining"])
		case "paid_research":
			require.EqualValues(t, 16000000, row["usedMicros"])
			if row["period"] == "day" {
				require.EqualValues(t, 0, row["remainingMicros"])
			}
		}
	}
	actions := view["yesterday"].(map[string]any)["actions"].([]any)
	require.Equal(t, []any{
		map[string]any{"operation": "UpdatePackageMeta", "mode": "live", "count": float64(3), "compensated": float64(1)},
		map[string]any{"operation": "UpdatePackageMeta", "mode": "proposal", "count": float64(1), "compensated": float64(0)},
	}, actions)
	for _, raw := range view["actionModes"].([]any) {
		row := raw.(map[string]any)
		require.Contains(t, row, "enabled")
		require.Contains(t, []string{"proposal", "live"}, row["mode"])
	}
	require.NoError(t, seobudget.ProviderRefused(ctx, st.DB, "fixture.example", "fixture-reservation"))
	read() // The distinct reservation receipt evidence also conforms without a shape change.
	for _, mutation := range []string{
		"complete_through=current_date-8",
		"fresh_through=current_date-8",
		"updated_at=now()-interval '8 days'",
	} {
		require.NoError(t, st.DB.Exec("UPDATE seo_data_watermarks SET complete_through=current_date,fresh_through=current_date,updated_at=now() WHERE source='gsc_page'").Error)
		require.NoError(t, st.DB.Exec("UPDATE seo_data_watermarks SET "+mutation+" WHERE source='gsc_page'").Error)
		require.Equal(t, false, read()["automationReady"], mutation)
	}
	view["yesterday"].(map[string]any)["madeUpCount"] = 1
	require.Error(t, schema.VisitJSON(view), "dashboard counters must no longer accept arbitrary fields")
}

func TestSEODashboardAlertsPaginationResolutionAndReadOnly(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	s := &Service{Store: st}
	doc, err := adminapi.GetSpec()
	require.NoError(t, err)
	schema := doc.Components.Schemas["SeoAlertPage"].Value
	input := func(params map[string]any) Input {
		t.Helper()
		in, err := Decode(map[string]any{"params": params}, nil, store.AuditActor{}, false)
		require.NoError(t, err)
		return in
	}
	read := func(params map[string]any) map[string]any {
		t.Helper()
		value, err := s.Execute(ctx, "ListSeoAlerts", input(params))
		require.NoError(t, err)
		return dashboardJSON(t, schema, value)
	}
	empty := read(nil)
	require.Empty(t, empty["records"])
	require.EqualValues(t, 1, empty["current"])
	require.EqualValues(t, 20, empty["size"])
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale) VALUES(9901,'https://fixture.example/apps/fixture','overview','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_audit_issues(id,rule,url_id,layer,severity,detail,first_seen,last_seen,resolved_at) VALUES
 (1,'canonical',9901,'edge','p0','wrong canonical','2026-10-01','2026-10-02',NULL),
 (2,'canonical',9901,'origin','p0','wrong canonical','2026-10-01','2026-10-03',NULL),
 (3,'robots',9901,'edge','p0','fixed','2026-10-01','2026-10-05','2026-10-06'),
 (4,'hreflang',9901,'edge','p1','missing link','2026-10-01','2026-10-03',NULL),
 (5,'status',9901,'origin','p0','fixed','2026-10-01','2026-10-01','2026-10-02')`).Error)
	// Completing a task is not evidence that its underlying audit issue is fixed.
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_tasks(kind,entity_key,dedupe_key,rule_version,state,evidence)
 VALUES('tech_alert','url:9901','audit-fixture','fixture','done','{"source":"audit","issueId":1}')`).Error)
	ids := func(view map[string]any) []int64 {
		out := []int64{}
		for _, raw := range view["records"].([]any) {
			out = append(out, int64(raw.(map[string]any)["id"].(float64)))
		}
		return out
	}
	first := read(map[string]any{"size": 2})
	require.EqualValues(t, 5, first["total"])
	require.Equal(t, []int64{4, 2}, ids(first))
	require.Equal(t, []int64{1, 3}, ids(read(map[string]any{"current": 2, "size": 2})))
	require.Equal(t, []int64{5}, ids(read(map[string]any{"current": 3, "size": 2})))
	require.Empty(t, ids(read(map[string]any{"current": 4, "size": 2})))
	open := read(map[string]any{"status": "open"})
	require.Equal(t, []int64{4, 2, 1}, ids(open))
	require.EqualValues(t, 3, open["total"])
	resolved := read(map[string]any{"status": "resolved"})
	require.Equal(t, []int64{3, 5}, ids(resolved))
	require.EqualValues(t, 2, resolved["total"])
	for _, params := range []map[string]any{{"size": 101}, {"current": 0}, {"status": "done"}} {
		_, err := s.Execute(ctx, "ListSeoAlerts", input(params))
		require.Error(t, err)
	}
	var issueCount, taskCount, actionCount int64
	require.NoError(t, st.DB.Table("seo_audit_issues").Count(&issueCount).Error)
	require.NoError(t, st.DB.Table("seo_tasks").Count(&taskCount).Error)
	require.NoError(t, st.DB.Table("seo_actions").Count(&actionCount).Error)
	require.EqualValues(t, 5, issueCount)
	require.EqualValues(t, 1, taskCount)
	require.Zero(t, actionCount)
	require.NoError(t, st.DB.Exec("UPDATE seo_audit_issues SET resolved_at=now() WHERE id=2").Error)
	require.Equal(t, []int64{4, 1}, ids(read(map[string]any{"status": "open"})))
}
