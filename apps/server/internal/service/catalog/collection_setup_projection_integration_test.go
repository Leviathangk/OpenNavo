//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Count statements, never elapsed time: setup must not grow a Detail query tree
// for each member, even on a cold cache.
type setupQueryCounter struct {
	logger.Interface
	count atomic.Int64
}

func (l *setupQueryCounter) Trace(context.Context, time.Time, func() (string, int64), error) {
	l.count.Add(1)
}

func TestCollectionSetupBatchMatchesLegacyJSON(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := domain.WithLinks(context.Background(), true)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := map[string]int64{}
	tokens := []string{"setup-standard", "setup-legacy", "setup-ambiguous", "setup-expired", "setup-invalid", "setup-review", "setup-hidden", "setup-disabled", "setup-stale", "setup-removed"}
	for _, token := range tokens {
		p, err := homebrew.Normalize("cask", json.RawMessage(fmt.Sprintf(`{"token":%q,"name":["Setup fixture"],"version":"2.0","homepage":"https://vendor.example.test","ruby_source_path":"Casks/s/setup-fixture.rb","depends_on":{"macos":{">=":["14"]}},"artifacts":[{"app":["Fixture.app"]}]}`, token)))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
		var id int64
		require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token=?", token).Scan(&id).Error)
		ids[token] = id
	}
	homepage := "https://external.example.test"
	linkID, err := st.WriteLinkApp(ctx, 0, domain.LinkInput{Token: "setup-external", Name: "External fixture", SourceLocale: "en-US", Homepage: &homepage, Pricing: "unknown", BundleIDs: []string{}})
	require.NoError(t, err)
	ids["setup-external"] = linkID
	proof := facts.NewProof("https://vendor.example.test/help", now.Add(-time.Minute), time.Hour, "verified", now)
	insertRecord := func(token, kind, key, value string, evidence facts.Proof) {
		t.Helper()
		raw, err := json.Marshal(evidence)
		require.NoError(t, err)
		require.NoError(t, st.DB.Exec(`INSERT INTO package_fact_records(package_id,kind,key,value,evidence) VALUES(?,?,?,?::jsonb,?::jsonb)`, ids[token], kind, key, value, string(raw)).Error)
	}
	price := `{"region":"US","edition":"Standard","period":"one_time","usage":"paid","downloadFree":true,"amount":"0.10","currency":"USD","purchaseUrl":"https://vendor.example.test/buy","channel":"vendor"}`
	insertRecord("setup-standard", "price", "us", price, proof)
	insertRecord("setup-ambiguous", "price", "standard", price, proof)
	insertRecord("setup-ambiguous", "price", "pro", strings.ReplaceAll(price, "Standard", "Pro"), proof)
	insertRecord("setup-standard", "price", "eu", strings.NewReplacer(`"US"`, `"DE"`, `"USD"`, `"EUR"`, `"one_time"`, `"monthly"`).Replace(price), proof)
	expired := facts.NewProof(proof.SourceURL, now.Add(-2*time.Hour), time.Hour, "verified", now)
	expired.Status = "verified" // Persisted verification ages out at read time.
	insertRecord("setup-expired", "price", "us", price, expired)
	review := proof
	review.Status = "needs_review"
	insertRecord("setup-review", "price", "us", price, review)
	insertRecord("setup-invalid", "price", "invalid", `{}`, proof)
	// Stale dimensioned records still suppress legacy fallback; invalid records do not.
	amount, currency, region, edition, period := 0.2, "USD", "US", "Standard", "one_time"
	version, minimum := "2.0", "13"
	legacy := facts.Evidence{AppVersion: &version, SourceURL: proof.SourceURL, Method: "official", VerifiedAt: now.Add(-time.Minute), VerifiedBy: "Fixture", Verifier: "admin", Status: "verified"}
	for _, token := range []string{"setup-legacy", "setup-expired", "setup-invalid", "setup-review"} {
		require.NoError(t, st.SavePackageFact(ctx, ids[token], "price", facts.Price{Type: "paid", Amount: &amount, Currency: &currency, Region: &region, Plan: &edition, Period: &period, Evidence: legacy}, nil))
	}
	require.NoError(t, st.SavePackageFact(ctx, ids["setup-legacy"], "macos", facts.Macos{Ranges: []facts.Range{{Arch: "arm64", Support: "native", Minimum: &minimum}, {Arch: "x86_64", Support: "native", Minimum: &minimum}}, Evidence: legacy}, nil))
	oldVersion := "1.0"
	legacy.AppVersion = &oldVersion
	require.NoError(t, st.SavePackageFact(ctx, ids["setup-standard"], "macos", facts.Macos{Ranges: []facts.Range{{Arch: "arm64", Support: "rosetta", Minimum: &minimum}}, Evidence: legacy}, nil))
	compat := `{"appVersion":"2.0","minimumMacOS":"14","maximumMacOS":"15.0","appleSilicon":"yes"}`
	insertRecord("setup-standard", "compatibility", "current", compat, proof)
	insertRecord("setup-standard", "compatibility", "old", strings.Replace(compat, "2.0", "1.0", 1), proof)
	insertRecord("setup-external", "compatibility", "link", strings.Replace(compat, "2.0", "", 1), proof)
	insertRecord("setup-review", "compatibility", "current", compat, review)
	insertRecord("setup-expired", "compatibility", "current", compat, expired)
	// Include the architecture-independent snapshot path as well as normal arch rows.
	require.NoError(t, st.DB.Exec(`INSERT INTO package_version_requirements(package_id,app_version,platforms,source_url,checked_at)
 VALUES(?,'2.0','[{"arch":"","version":"2.0","minMacos":"14"}]','https://vendor.example.test/requirements',?)
 ON CONFLICT(package_id,app_version) DO UPDATE SET platforms=EXCLUDED.platforms,source_url=EXCLUDED.source_url`, ids["setup-standard"], now).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_source_facts(package_id,source,input,status,data,checked_at) VALUES(?,'app_store','fixture','found','{"name":"Store fixture","url":"https://apps.example.test/fixture","region":"US","price":0,"currency":"USD"}',?)`, ids["setup-standard"], now).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,hidden) VALUES(?,true) ON CONFLICT(package_id) DO UPDATE SET hidden=true`, ids["setup-hidden"]).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled=true WHERE id=?`, ids["setup-disabled"]).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET stale_disabled=true WHERE id=?`, ids["setup-stale"]).Error)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET removed_at=? WHERE id=?`, now, ids["setup-removed"]).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO collections(slug,status) VALUES('setup-batch','published'),('setup-empty','published')`).Error)
	// Reverse the ID order to catch accidental reordering by the batch query.
	require.NoError(t, st.DB.Exec(`INSERT INTO collection_items(collection_id,package_id,sort) SELECT c.id,p.id,1000-p.id FROM collections c CROSS JOIN packages p WHERE c.slug='setup-batch'`).Error)
	var collectionID int64
	require.NoError(t, st.DB.Raw(`SELECT id FROM collections WHERE slug='setup-batch'`).Scan(&collectionID).Error)
	require.NoError(t, st.SaveCollectionCriteria(ctx, collectionID, store.CollectionCriteria{Locale: "en-US", Criteria: "Fixture selection criteria", Evidence: proof}))
	svc := &PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	encode := func(value map[string]any) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		var setup publicapi.CollectionSetup
		require.NoError(t, json.Unmarshal(raw, &setup))
		raw, err = json.Marshal(setup)
		require.NoError(t, err)
		return raw
	}
	for _, elapsed := range []time.Duration{0, 2 * time.Hour} {
		now = now.Add(elapsed)
		for _, links := range []bool{true, false} {
			for _, slug := range []string{"setup-batch", "setup-empty"} {
				t.Run(fmt.Sprintf("%s/links=%t/elapsed=%s", slug, links, elapsed), func(t *testing.T) {
					ctx := domain.WithLinks(ctx, links)
					old, err := svc.legacyCollectionSetupForTest(ctx, slug, "fr-FR")
					require.NoError(t, err)
					counter := &setupQueryCounter{Interface: logger.Default.LogMode(logger.Silent)}
					projected := *svc
					projected.Store = &store.Store{DB: st.DB.Session(&gorm.Session{Logger: counter}), Redis: st.Redis}
					got, err := projected.collectionSetup(ctx, slug, "fr-FR")
					require.NoError(t, err)
					require.JSONEq(t, string(encode(old)), string(encode(got)))
					require.LessOrEqual(t, counter.count.Load(), int64(3), "fixed number of reads, no per-member Detail")
					if slug == "setup-batch" {
						require.Equal(t, []string{"setup-review", "setup-invalid", "setup-expired", "setup-ambiguous", "setup-legacy", "setup-standard"}, got["installableTokens"])
						require.Equal(t, "en-US", got["criteria"].(*store.CollectionCriteria).Locale)
					}
				})
			}
		}
	}
}
