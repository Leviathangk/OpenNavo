//go:build integration

package seokeywords

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestOpenSEOObservationsWithoutReceiptAndPaidRetry(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{DB: st.DB, WebURL: "https://fixture.example"}
	volume := int64(100)
	o := Observation{Keyword: "Fixture mac utility", Source: "openseo:dataforseo", Engine: "google", Country: "US", Language: "en-US", Device: "desktop", Period: "2026-10", VolumeState: "known", SearchVolume: &volume, ObservedAt: time.Now().UTC()}
	write := func(observation Observation, dry bool) error {
		_, err := svc.Upsert(context.Background(), Request{Records: []Observation{observation}}, dry, store.AuditActor{})
		return err
	}
	count := func(table string) int64 {
		var n int64
		require.NoError(t, st.DB.Table(table).Count(&n).Error)
		return n
	}
	require.NoError(t, write(o, false))
	require.EqualValues(t, 1, count("seo_keywords"))
	require.Zero(t, count("seo_cost_receipts"))
	var record struct{ VendorTaskID, CostUSD *string }
	require.NoError(t, st.DB.Table("seo_keywords").Take(&record).Error)
	require.Nil(t, record.VendorTaskID)
	require.Nil(t, record.CostUSD)
	task, cost := "fixture-receipt-1", "0.0500"
	o.VendorTaskID, o.CostUSD = &task, &cost
	require.NoError(t, write(o, true))
	require.Zero(t, count("seo_cost_receipts"), "preview must not book charges")
	require.NoError(t, write(o, false))
	require.NoError(t, write(o, false))
	require.EqualValues(t, 1, count("seo_cost_receipts"), "retry must not book twice")
	var micros int64
	require.NoError(t, st.DB.Raw("SELECT cost_micros FROM seo_cost_receipts").Scan(&micros).Error)
	require.EqualValues(t, 50000, micros)
	other := o
	conflicting := "0.0600"
	other.CostUSD = &conflicting
	require.ErrorIs(t, write(other, false), seobudget.ErrReceipt)
	require.EqualValues(t, 1, count("seo_cost_receipts"))
	other.VendorTaskID = nil
	require.Error(t, write(other, false))
	require.EqualValues(t, 1, count("seo_keywords"))
}
