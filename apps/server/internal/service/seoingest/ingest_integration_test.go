//go:build integration

package seoingest

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOMeasurementsAreAtomicIdempotentAndLayered(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s := &Service{DB: st.DB, WebURL: "https://example.test", Now: func() time.Time { return now }}
	page := SearchRow{Keys: []string{"2026-10-16", "https://example.test/apps/sample", "usa", "DESKTOP"}, Clicks: 10, Impressions: 100, Position: 3}
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-16", []SearchRow{page}, "", true, now))
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-16", []SearchRow{page}, "", true, now))
	var total int64
	require.NoError(t, st.DB.Raw("SELECT sum(clicks) FROM seo_gsc_page_daily").Scan(&total).Error)
	require.Equal(t, int64(10), total)
	query := SearchRow{Keys: []string{"2026-10-16", page.Keys[1], "sample 12345678"}, Clicks: 2, Impressions: 5, Position: 4}
	require.NoError(t, s.saveGSC(ctx, "gsc_query", "2026-10-16", []SearchRow{query}, "", true, now))
	var anomaly string
	require.NoError(t, st.DB.Raw("SELECT anomaly FROM seo_gsc_query_daily").Scan(&anomaly).Error)
	require.Equal(t, "digit_suffix_burst", anomaly)
	// A hole, a partial day, and a row cap must each prevent watermark advancement.
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-18", nil, "", true, now))
	watermark := func() string {
		var v string
		require.NoError(t, st.DB.Raw("SELECT complete_through::text FROM seo_data_watermarks WHERE source='gsc_page'").Scan(&v).Error)
		return v
	}
	require.Equal(t, "2026-10-16", watermark())
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-17", nil, "2026-10-17", true, now))
	require.Equal(t, "2026-10-16", watermark())
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-17", nil, "", false, now))
	require.Equal(t, "2026-10-16", watermark())
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-17", nil, "", true, now))
	require.Equal(t, "2026-10-18", watermark())
	// An invalid URL rolls back deletion of the previous good day's metrics.
	bad := page
	bad.Keys = []string{page.Keys[0], "not a URL", "usa", "DESKTOP"}
	require.Error(t, s.saveGSC(ctx, "gsc_page", "2026-10-16", []SearchRow{bad}, "", true, now.Add(time.Second)))
	require.NoError(t, st.DB.Raw("SELECT sum(clicks) FROM seo_gsc_page_daily").Scan(&total).Error)
	require.Equal(t, int64(10), total)
	// A slower earlier run cannot overwrite newer measurements.
	old := page
	old.Clicks = 1
	require.NoError(t, s.saveGSC(ctx, "gsc_page", "2026-10-16", []SearchRow{old}, "", true, now.Add(-time.Second)))
	require.NoError(t, st.DB.Raw("SELECT sum(clicks) FROM seo_gsc_page_daily").Scan(&total).Error)
	require.Equal(t, int64(10), total)
	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM seo_urls LIMIT 1").Scan(&id).Error)
	issue := []Issue{{"seo.canonical", "p0", "wrong canonical"}}
	require.NoError(t, s.recordAudit(ctx, id, "origin", issue, true, now))
	require.NoError(t, s.recordAudit(ctx, id, "edge", issue, true, now))
	require.NoError(t, s.recordAudit(ctx, id, "origin", nil, true, now.Add(time.Second)))
	require.NoError(t, s.recordAudit(ctx, id, "edge", []Issue{{"seo.fetch", "p1", "timeout"}}, false, now.Add(time.Second)))
	var open int64
	require.NoError(t, st.DB.Table("seo_audit_issues").Where("layer='edge' AND resolved_at IS NULL").Count(&open).Error)
	require.Equal(t, int64(2), open)
	require.NoError(t, st.DB.Table("seo_audit_issues").Where("layer='origin' AND resolved_at IS NULL").Count(&open).Error)
	require.Zero(t, open)
	// Old in-flight probe results cannot reopen a subsequently resolved problem.
	require.NoError(t, s.recordAudit(ctx, id, "origin", issue, true, now))
	require.NoError(t, st.DB.Table("seo_audit_issues").Where("layer='origin' AND resolved_at IS NULL").Count(&open).Error)
	require.Zero(t, open)

	// Two workers share the same remaining quota; a timeout consumes its reservation.
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("INSERT INTO seo_inspection_quota(day,used) VALUES(?,1499)", day(now.In(loc))).Error)
	var calls atomic.Int32
	s.Google = func(context.Context) (Google, error) {
		return fakeGoogle{inspect: func(string) (Inspection, error) { calls.Add(1); return Inspection{}, context.DeadlineExceeded }}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Inspect(ctx) }()
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	var used int
	require.NoError(t, st.DB.Raw("SELECT used FROM seo_inspection_quota").Scan(&used).Error)
	require.Equal(t, 1500, used)
}

func TestGSCSkipsOtherOriginsWithoutLosingSiteRows(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	s := &Service{DB: st.DB, WebURL: "https://example.test"}
	for _, source := range []string{"gsc_page", "gsc_query"} {
		rows := []SearchRow{}
		for _, page := range []string{"http://example.test/apps/sample", "https://other.example.test/", "https://example.test/apps/sample"} {
			keys := []string{"2026-10-16", page, "sample"}
			if source == "gsc_page" {
				keys = []string{"2026-10-16", page, "usa", "DESKTOP"}
			}
			rows = append(rows, SearchRow{Keys: keys, Clicks: 2, Impressions: 10, Position: 3})
		}
		require.NoError(t, s.saveGSC(ctx, source, "2026-10-16", rows, "", true, time.Now()))
		var count int64
		table := "seo_gsc_page_daily"
		if source == "gsc_query" {
			table = "seo_gsc_query_daily"
		}
		require.NoError(t, st.DB.Table(table).Count(&count).Error)
		require.EqualValues(t, 1, count)
		var watermark string
		require.NoError(t, st.DB.Raw("SELECT complete_through::text FROM seo_data_watermarks WHERE source=?", source).Scan(&watermark).Error)
		require.Equal(t, "2026-10-16", watermark)
	}
}

// GSC and Bing register URLs row by row inside one transaction, while registry
// sync upserts the same table in bulk. Both take the registry lock first, so a
// day's save waits for a sync instead of deadlocking with it.
func TestURLRegistrationWaitsForRegistrySync(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s := &Service{DB: st.DB, WebURL: "https://example.test", Now: func() time.Time { return now }}
	holder := st.DB.Begin()
	require.NoError(t, holder.Error)
	released := false
	t.Cleanup(func() {
		if !released {
			holder.Rollback()
		}
	})
	require.NoError(t, lockRegistry(holder))
	done := make(chan error, 1)
	go func() {
		page := SearchRow{Keys: []string{"2026-10-16", "https://example.test/apps/sample", "usa", "DESKTOP"}, Clicks: 1, Impressions: 2, Position: 3}
		done <- s.saveGSC(ctx, "gsc_page", "2026-10-16", []SearchRow{page}, "", true, now)
	}()
	select {
	case err := <-done:
		t.Fatalf("GSC save finished while registry sync held the lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, holder.Rollback().Error)
	released = true
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("GSC save did not resume after registry sync released the lock")
	}
}
