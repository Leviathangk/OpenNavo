//go:build integration

package seobudget

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSharedQuotaConcurrentAndRollback(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(automation,'{dailyEntities,listing}','2')`).Error)
	var success atomic.Int64
	var wg sync.WaitGroup
	errorsOut := make(chan error, 6)
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := st.DB.Transaction(func(tx *gorm.DB) error {
				return Reserve(ctx, tx, "fixture.example", "listing", fmt.Sprintf("pkg:%d", i), fmt.Sprintf("token-%d", i))
			})
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, ErrLimit) {
				errorsOut <- err
			}
		}(i)
	}
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		require.NoError(t, err)
	}
	require.EqualValues(t, 2, success.Load())
	var entity string
	require.NoError(t, st.DB.Raw("SELECT entity_key FROM seo_daily_entities LIMIT 1").Scan(&entity).Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
		return Reserve(ctx, tx, "fixture.example", "listing", entity, "different-token")
	}))
	injected := errors.New("injected transaction failure")
	err := st.DB.Transaction(func(tx *gorm.DB) error {
		if err := Reserve(ctx, tx, "fixture.example", "compensate", "pkg:99", "rollback"); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	var count int64
	require.NoError(t, st.DB.Table("seo_daily_entities").Where("action='compensate'").Count(&count).Error)
	require.Zero(t, count)
}

func TestReceiptReconciliationAndBreakerIsolation(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	site := "fixture.example"
	r := Receipt{VendorTaskID: "fake-provider-task", CostUSD: "15", IncurredAt: time.Now().UTC().Truncate(time.Second)}
	for range 2 {
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return Record(ctx, tx, site, r) }))
	}
	var count int64
	require.NoError(t, st.DB.Table("seo_cost_receipts").Count(&count).Error)
	require.EqualValues(t, 1, count)
	r.CostUSD = "14"
	require.ErrorIs(t, st.DB.Transaction(func(tx *gorm.DB) error { return Record(ctx, tx, site, r) }), ErrReceipt)
	require.ErrorIs(t, st.DB.Transaction(func(tx *gorm.DB) error { return Check(ctx, tx, site, "research", true) }), ErrFrozen)
	for _, action := range []string{"translation", "listing", "article", "fact_fix", "withdraw", "compensate"} {
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return Reserve(ctx, tx, site, action, "pkg:1", "fixture") }), action)
	}
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return ProviderRefused(ctx, tx, "other.example", "fake-rejection") }))
	require.ErrorIs(t, st.DB.Transaction(func(tx *gorm.DB) error { return Check(ctx, tx, "other.example", "research", true) }), ErrFrozen)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return Open(ctx, tx, site, "business", "*", "test", nil, nil) }))
	require.ErrorIs(t, st.DB.Transaction(func(tx *gorm.DB) error { return Reserve(ctx, tx, site, "listing", "pkg:2", "new") }), ErrFrozen)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return Reserve(ctx, tx, site, "maintenance", "pkg:2", "new") }))
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return Open(ctx, tx, site, "technical", "*", "test", nil, nil) }))
	for _, action := range []string{"fact_fix", "withdraw", "compensate"} {
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return Reserve(ctx, tx, site, action, "pkg:2", "repair") }), action)
	}
	require.ErrorIs(t, st.DB.Transaction(func(tx *gorm.DB) error { return Check(ctx, tx, site, "article", false) }), ErrFrozen)
}

func TestBusinessBreakerRequiresCompleteWindows(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_urls(id,url,page_kind,locale) VALUES(701,'https://fixture.example/apps/editor','overview','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_data_watermarks SET complete_through='2026-10-01' WHERE source='gsc_page'`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_gsc_page_daily(day,url_id,country,device,clicks,impressions,position_sum) SELECT day,701,'US','desktop',CASE WHEN day<'2026-09-04' THEN 10 ELSE 1 END,CASE WHEN day<'2026-09-04' THEN 100 ELSE 10 END,1000 FROM generate_series('2026-08-07'::date,'2026-10-01'::date,interval '1 day') day`).Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return AssessBusiness(ctx, tx, "fixture.example") }))
	var count int64
	require.NoError(t, st.DB.Table("seo_breakers").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Exec(`INSERT INTO seo_ingest_days(source,day,temporal_complete,retrieval_complete,row_count,coverage,fetched_at) SELECT 'gsc_page',day,true,true,1,'complete',now() FROM generate_series('2026-08-07'::date,'2026-10-01'::date,interval '1 day') day`).Error)
	require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return AssessBusiness(ctx, tx, "fixture.example") }))
	require.NoError(t, st.DB.Table("seo_breakers").Where("kind='business'").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
