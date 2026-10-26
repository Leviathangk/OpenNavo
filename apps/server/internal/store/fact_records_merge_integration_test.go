//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFactMergeKeepsIDsEvidenceAndNewestPrice(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC()
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"merge-target","name":["Target"],"version":"1.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	target, err := st.CatalogRow(ctx, "cask", "merge-target")
	require.NoError(t, err)
	homepage := "https://vendor.example"
	from, err := st.WriteLinkApp(ctx, 0, domain.LinkInput{Token: "merge-source", Name: "Source", Homepage: &homepage, Pricing: "unknown", SourceLocale: "en-US", BundleIDs: []string{}})
	require.NoError(t, err)
	proof := facts.NewProof(homepage, now, time.Hour, "verified", now)
	price := facts.Record{Kind: "price", Key: "canonical", Evidence: proof, Value: json.RawMessage(`{"region":"US","edition":"Pro","period":"one_time","usage":"paid","downloadFree":true,"amount":"19.99","currency":"USD","purchaseUrl":"https://vendor.example/buy","channel":"vendor"}`)}
	require.NoError(t, st.UpsertFactRecords(ctx, target.ID, []facts.Record{price}, now))
	price.Key = "old-price"
	attribute := facts.Record{Kind: "attribute", Key: "offline", Evidence: proof, Value: json.RawMessage(`{"name":"offline","booleanValue":true}`)}
	require.NoError(t, st.UpsertFactRecords(ctx, from, []facts.Record{price, attribute}, now))
	before, err := st.FactRecords(ctx, from)
	require.NoError(t, err)
	require.Len(t, before, 2)
	require.NoError(t, st.DB.Exec("UPDATE package_fact_records SET updated_at=CASE WHEN package_id=? THEN '2026-01-01'::timestamptz ELSE '2026-02-01'::timestamptz END", target.ID).Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error { return store.MergeFactRelationshipsForTest(ctx, tx, from, target.ID) }))
	after, err := st.FactRecords(ctx, target.ID)
	require.NoError(t, err)
	require.Len(t, after, 2)
	require.Equal(t, before[0].ID, after[0].ID)
	require.Equal(t, before[0].Evidence, after[0].Evidence)
	require.Equal(t, "old-price", after[1].Key)
	require.Equal(t, before[1].ID, after[1].ID)
	left, err := st.FactRecords(ctx, from)
	require.NoError(t, err)
	require.Empty(t, left)
}
