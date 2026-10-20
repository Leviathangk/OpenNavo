//go:build integration

package catalog_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestFactRecordsPriceExpiryAndAtomicity(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"fact-example","name":["Example"],"version":"1.2","artifacts":[{"app":["Example.app"]},{"uninstall":[{"launchctl":"com.example.agent"}]},{"zap":[{"trash":["~/Library/Application Support/Example"]}]}]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var id int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='fact-example'").Scan(&id).Error)
	now := time.Now().UTC()
	proof := facts.NewProof("https://vendor.example/pricing", now, time.Hour, "verified", now)
	r := facts.Record{Kind: "price", Key: "us-pro", Value: json.RawMessage(`{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":true,"amount":"19.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}`), Evidence: proof}
	require.NoError(t, st.UpsertFactRecords(ctx, id, []facts.Record{r}, now))
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	detail, err := svc.Detail(ctx, "cask", "fact-example", "en-US")
	require.NoError(t, err)
	require.NotNil(t, detail.Facts)
	require.Len(t, *detail.Facts.Offers, 1)
	require.Len(t, detail.Facts.Uninstall, 2)
	now = now.Add(time.Hour)
	detail, err = svc.Detail(ctx, "cask", "fact-example", "en-US")
	require.NoError(t, err)
	require.Empty(t, *detail.Facts.Offers)
	require.Equal(t, "stale", string((*detail.Facts.Prices)[0].Evidence.Status))
	bad := r
	bad.Key = "bad"
	bad.Kind = "invalid"
	require.Error(t, st.UpsertFactRecords(ctx, id, []facts.Record{r, bad}, now))
	records, err := st.FactRecords(ctx, id)
	require.NoError(t, err)
	require.Len(t, records, 1)
}
