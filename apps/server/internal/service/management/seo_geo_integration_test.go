//go:build integration

package management

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOGeoReceiptReplayRollbackCostsAndQueries(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	body := geoFixture(t)
	snapshot := body["snapshot"].(map[string]any)
	charge := snapshot["charges"].([]any)[0].(map[string]any)
	// A real over-budget invoice is still imported; estimates never enter this field.
	charge["costUsd"] = "101"
	charge["incurredAt"] = svc.now().Format("2006-01-02T15:04:05Z")
	count := func(table string) int64 {
		var n int64
		require.NoError(t, st.DB.Table(table).Count(&n).Error)
		return n
	}
	readCtx := hermesContext("seo:insight:view")
	_, err := svc.Execute(readCtx, "RecordSeoGeo", Input{Body: body})
	require.Error(t, err)
	_, err = svc.Execute(hermesContext("seo:task:handle"), "ListSeoGeo", Input{})
	require.Error(t, err)
	empty, err := svc.geoOverview(context.Background())
	require.NoError(t, err)
	require.Nil(t, empty)
	preview := hermesContext("seo:task:handle")
	domain.CurrentOperation(preview).DryRun = true
	_, err = svc.Execute(preview, "RecordSeoGeo", Input{Body: body})
	require.NoError(t, err)
	for _, table := range []string{"seo_geo_snapshots", "seo_control_receipts", "seo_cost_receipts", "seo_breakers"} {
		require.Zero(t, count(table))
	}
	// Canceled requests cannot leave a partial import.
	canceled, cancel := context.WithCancel(hermesContext("seo:task:handle"))
	cancel()
	_, err = svc.Execute(canceled, "RecordSeoGeo", Input{Body: body})
	require.Error(t, err)
	require.Zero(t, count("seo_geo_snapshots"))
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	results := make(chan any, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
			errs <- e
			results <- r
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		require.NoError(t, err)
	}
	var result map[string]any
	for r := range results {
		m := hermesMap(r)
		if result == nil {
			result = m
		} else {
			require.Equal(t, result, m)
		}
	}
	require.EqualValues(t, 1, count("seo_geo_snapshots"))
	require.EqualValues(t, 1, count("seo_control_receipts"))
	require.EqualValues(t, 1, count("seo_cost_receipts"))
	require.EqualValues(t, 1, count("seo_breakers"))
	require.Zero(t, count("job_outbox"))
	require.Zero(t, count("seo_actions"))
	require.Zero(t, count("content_revisions"))
	// A new import key, or a different run, cannot double-charge the same request ID.
	body["idempotencyKey"] = "fixture-second-key"
	replay, err := svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.NoError(t, err)
	require.Equal(t, result, hermesMap(replay))
	require.EqualValues(t, 1, count("seo_geo_snapshots"))
	snapshot["providerRunKey"] = "fixture-second-run"
	body["idempotencyKey"] = "fixture-third-key"
	_, err = svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.NoError(t, err)
	require.EqualValues(t, 1, count("seo_cost_receipts"))
	// Same key with changed arguments conflicts. Corrections must append explicitly.
	snapshot["artifactCompleteness"] = "incomplete"
	_, err = svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.Error(t, err)
	body["idempotencyKey"] = "fixture-correction-key"
	_, err = svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.Error(t, err)
	var latest int64
	require.NoError(t, st.DB.Raw("SELECT max(id) FROM seo_geo_snapshots").Scan(&latest).Error)
	body["supersedes"] = latest
	_, err = svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.NoError(t, err)
	require.EqualValues(t, 3, count("seo_geo_snapshots"))
	// A conflicting actual fee aborts snapshot and control receipt together.
	beforeReceipts := count("seo_control_receipts")
	body["supersedes"] = nil
	body["idempotencyKey"] = "fixture-cost-conflict"
	snapshot["providerRunKey"] = "fixture-cost-conflict"
	charge["costUsd"] = "102"
	_, err = svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.Error(t, err)
	require.EqualValues(t, 3, count("seo_geo_snapshots"))
	require.Equal(t, beforeReceipts, count("seo_control_receipts"))
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	page, err := svc.Execute(readCtx, "ListSeoGeo", Input{Params: map[string]any{"current": float64(1), "size": float64(1)}})
	require.NoError(t, err)
	view := dashboardJSON(t, spec.Components.Schemas["SeoGeoPage"].Value, page)
	require.EqualValues(t, 3, view["total"])
	require.Len(t, view["records"], 1)
	id := int64(result["id"].(float64))
	detail, err := svc.Execute(readCtx, "GetSeoGeo", Input{ID: id})
	require.NoError(t, err)
	value := dashboardJSON(t, spec.Components.Schemas["SeoGeoRecord"].Value, detail)
	require.Nil(t, value["observationLinks"].([]any)[0].(map[string]any)["url"])
	svc.Config.GEOObservationBaseURL = "https://evidence.example/project"
	detail, err = svc.Execute(readCtx, "GetSeoGeo", Input{ID: id})
	require.NoError(t, err)
	value = hermesMap(detail)
	require.Contains(t, value["observationLinks"].([]any)[0].(map[string]any)["url"], "/observations/observation-fixture-001")
	other := *svc
	other.Config.WebBaseURL = "https://other.example"
	_, err = other.Execute(readCtx, "GetSeoGeo", Input{ID: id})
	require.Error(t, err)
	var receipts []struct{ BeforeData, AfterData, Result string }
	require.NoError(t, st.DB.Raw("SELECT before_data::text,after_data::text,result::text FROM seo_control_receipts WHERE operation='RecordSeoGeo'").Scan(&receipts).Error)
	for _, r := range receipts {
		for _, raw := range []string{r.BeforeData, r.AfterData, r.Result} {
			var v map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &v))
			for k := range v {
				require.Contains(t, []string{"id", "payloadHash"}, k)
			}
		}
	}
}

func TestSEOGeoSummaryOnlyAndComparableHistory(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	v := geoFixture(t)
	snapshot := v["snapshot"].(map[string]any)
	prompts := []any{}
	engines := []any{}
	for i := 0; i < 10; i++ {
		prompts = append(prompts, map[string]any{"id": fmt.Sprintf("p%d", i), "text": fmt.Sprintf("Neutral Mac question %d", i), "hash": snapshot["prompts"].([]any)[0].(map[string]any)["hash"], "classification": "neutral"})
	}
	for i := 0; i < 5; i++ {
		engines = append(engines, map[string]any{"key": fmt.Sprintf("e%d", i), "name": fmt.Sprintf("Engine %d", i), "surface": "unknown", "modelVersion": "unknown", "toolVersion": "unknown"})
	}
	snapshot["prompts"], snapshot["engines"], snapshot["plannedCount"], snapshot["cells"], snapshot["mode"] = prompts, engines, 50, []any{}, "summary_only"
	snapshot["reported"] = map[string]any{"answered": 49, "mentioned": 0, "candidate": nil, "cited": 0, "missingFields": []string{"observationId", "answerHash", "citationExtraction", "actualCharges"}}
	_, err := svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: v})
	require.NoError(t, err)
	latest, err := svc.geoOverview(context.Background())
	require.NoError(t, err)
	require.Nil(t, latest.Counts.Answered)
	require.Nil(t, latest.Trend)
	require.Equal(t, 49, *latest.Counts.Reported.Answered)
	require.Zero(t, latest.Counts.Cited.Known)
	var n int64
	require.NoError(t, st.DB.Table("seo_cost_receipts").Count(&n).Error)
	require.Zero(t, n, "unknown actual charge never becomes zero or the reported ceiling")
	// Complete, matching later periods establish a trend; a different market cannot.
	for i := 0; i < 3; i++ {
		v := geoFixture(t)
		v["idempotencyKey"] = fmt.Sprintf("fixture-history-%d", i)
		snap := v["snapshot"].(map[string]any)
		snap["providerRunKey"] = fmt.Sprintf("fixture-history-%d", i)
		snap["periodStart"] = fmt.Sprintf("2026-10-%02dT09:00:00Z", i+1)
		snap["periodEnd"] = fmt.Sprintf("2026-10-%02dT10:00:00Z", i+1)
		c := snap["cells"].([]any)[0].(map[string]any)
		c["citationExtraction"] = "complete"
		c["cited"] = false
		if i == 1 {
			c["cited"] = true
			c["ownUrls"] = []string{"https://fixture.example/ja/apps/example-editor/versions"}
		}
		if i == 2 {
			snap["context"].(map[string]any)["market"] = "JP"
		}
		result, err := svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: v})
		require.NoError(t, err)
		detail, err := svc.Execute(hermesContext("seo:insight:view"), "GetSeoGeo", Input{ID: int64(hermesMap(result)["id"].(float64))})
		require.NoError(t, err)
		out := detail.(geoRecord)
		if i == 1 {
			require.NotNil(t, out.Trend)
			require.Equal(t, float64(1), out.Trend.CitedRateDelta)
		} else {
			require.Nil(t, out.Trend)
		}
	}
}

func TestSEOGeoAtomicReceiptFailureAndNewChargeOnRetry(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	body := geoFixture(t)
	snap := body["snapshot"].(map[string]any)
	charge := snap["charges"].([]any)[0].(map[string]any)
	charge["costUsd"] = "0.25"
	require.NoError(t, st.DB.Exec(`CREATE FUNCTION fixture_geo_receipt_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='RecordSeoGeo' THEN RAISE EXCEPTION 'fixture receipt unavailable'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fixture_geo_receipt_failure BEFORE INSERT ON seo_control_receipts FOR EACH ROW EXECUTE FUNCTION fixture_geo_receipt_failure()`).Error)
	_, err := svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.Error(t, err)
	for _, table := range []string{"seo_geo_snapshots", "seo_control_receipts", "seo_cost_receipts"} {
		var n int64
		require.NoError(t, st.DB.Table(table).Count(&n).Error)
		require.Zero(t, n, "receipt failure rolls back %s", table)
	}
	require.NoError(t, st.DB.Exec("DROP TRIGGER fixture_geo_receipt_failure ON seo_control_receipts; DROP FUNCTION fixture_geo_receipt_failure()").Error)
	result, err := svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.NoError(t, err)
	body["supersedes"] = hermesMap(result)["id"]
	body["idempotencyKey"] = "fixture-new-charge-retry"
	snap["charges"] = append(snap["charges"].([]any), map[string]any{"vendorTaskId": "fixture-charged-retry", "costUsd": "0.5", "credits": int64(12), "incurredAt": charge["incurredAt"]})
	_, err = svc.Execute(hermesContext("seo:task:handle"), "RecordSeoGeo", Input{Body: body})
	require.NoError(t, err)
	var count, total int64
	require.NoError(t, st.DB.Table("seo_cost_receipts").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, st.DB.Raw("SELECT sum(cost_micros) FROM seo_cost_receipts").Scan(&total).Error)
	require.EqualValues(t, 750000, total, "each newly charged retry is billed even within the same provider run")
}
