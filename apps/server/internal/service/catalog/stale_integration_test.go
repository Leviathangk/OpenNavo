//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCatalogStalePolicyDatesVisibilityIncrementalsAndRecovery(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	source := &testSource{Previous: map[string]homebrew.Conditional{}, Data: map[string][]string{"cask": {
		`{"token":"old","version":"1,10","name":["Old App"]}`,
		`{"token":"recent","version":"1"}`,
		`{"token":"boundary","version":"1"}`,
		`{"token":"font-sample","version":"1"}`,
		`{"token":"unknown","version":"1"}`,
		`{"token":"latest-app","version":"latest"}`,
		`{"token":"build-app","version":"2,20"}`,
		`{"token":"zero-date","version":"1"}`,
		`{"token":"future-date","version":"1"}`,
		`{"token":"upstream","version":"1","disabled":true,"disable_reason":"unsafe","disable_date":"2026-01-01"}`,
		`{"token":"hidden","version":"1"}`,
		`{"token":"excluded","version":"1"}`,
	}}}
	svc := &Service{Store: st, Source: source, Queue: &testQueue{}, Now: func() time.Time { return now }}
	_, err := svc.Run(ctx)
	require.NoError(t, err)
	legacy, err := homebrew.Normalize("formula", json.RawMessage(`{"name":"legacy","versions":{"stable":"1"}}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: legacy}}))
	oldDate := now.Add(-store.StalePackageAge - time.Second)
	for _, query := range []struct {
		SQL  string
		Args []any
	}{
		{`UPDATE package_versions v SET brew_committed_at=? FROM packages p WHERE v.package_id=p.id AND p.token IN ('old','font-sample','upstream','hidden','excluded','legacy','latest-app')`, []any{oldDate}},
		{`INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden'`, nil},
		{`INSERT INTO package_meta(package_id,changelog_excluded) SELECT id,true FROM packages WHERE token='excluded'`, nil},
		{`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) SELECT id,'2,19','2',? FROM packages WHERE token='build-app'`, []any{oldDate}},
	} {
		require.NoError(t, st.DB.Exec(query.SQL, query.Args...).Error)
	}
	for token, date := range map[string]time.Time{
		"recent": now.Add(-24 * time.Hour), "boundary": now.Add(-store.StalePackageAge),
		"zero-date": {}, "future-date": now.Add(time.Hour),
	} {
		require.NoError(t, st.DB.Exec(`UPDATE package_versions v SET brew_committed_at=? FROM packages p WHERE v.package_id=p.id AND p.token=?`, date, token).Error)
	}
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	require.NoError(t, st.ApplyAnalytics(ctx, []domain.AnalyticsRow{{ID: rows["old"].ID, Installs30d: 100}, {ID: rows["recent"].ID, Installs30d: 80}}, "2026-10-10"))
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	_, cursor, err := st.CatalogBounds(ctx)
	require.NoError(t, err)
	stop, err := cache.Subscribe(ctx, st.Redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	defer stop()
	keys := []string{"c:pkg:stale", "c:rank:stale", "c:home:stale", "c:cat:stale", "c:sug:stale", "c:related:stale"}
	for _, key := range keys {
		require.NoError(t, st.Redis.Set(ctx, key, "old", time.Hour).Err())
	}
	source.NotModified = true
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	result := stats["kinds"].([]Stats)[0]
	require.True(t, result.NotModified)
	require.Equal(t, 4, result.AutoDisabled)
	require.Zero(t, result.Reactivated)
	require.Equal(t, 5, result.MissingVersionDate)
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, keys...).Val() == 0 }, 2*time.Second, 10*time.Millisecond)
	for token, disabled := range map[string]bool{"old": true, "upstream": true, "hidden": true, "excluded": true, "recent": false, "boundary": false, "font-sample": false, "unknown": false, "latest-app": false, "build-app": false, "zero-date": false, "future-date": false} {
		p, err := st.PublicPackage(ctx, "cask", token, "en-US")
		require.NoError(t, err)
		require.Equal(t, disabled, p.StaleDisabled, token)
		require.Equal(t, token == "upstream", p.UpstreamDisabled, token)
	}
	var formulaStale bool
	require.NoError(t, st.DB.Raw(`SELECT stale_disabled FROM packages WHERE token='legacy'`).Scan(&formulaStale).Error)
	require.False(t, formulaStale)
	filter := store.PublicFilter{Locale: "en-US", Current: 1, Size: 100, IncludeFonts: true}
	public := &PublicService{Store: st}
	page, err := public.List(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 8, page.Total)
	for _, p := range page.Records {
		require.False(t, p.Disabled)
	}
	filter.IncludeDisabled = true
	page, err = public.List(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 11, page.Total)
	detail, err := public.Detail(ctx, "cask", "old", "zh-CN")
	require.NoError(t, err)
	require.Equal(t, "超过 90 天没有版本更新", *detail.Disable.Reason)
	detail, err = public.Detail(ctx, "cask", "upstream", "en-US")
	require.NoError(t, err)
	require.Equal(t, "unsafe", *detail.Disable.Reason)
	lookup, err := public.Lookup(ctx, []publicapi.PackageRef{{Kind: "cask", Token: "old"}}, "en-US")
	require.NoError(t, err)
	require.True(t, *lookup[0].Disabled)
	_, total, err := st.Search(ctx, domain.SearchInput{Kind: "cask", Current: 1, Size: 100}, []string{"old"}, 1, 1)
	require.NoError(t, err)
	require.Zero(t, total)
	_, total, err = st.Search(ctx, domain.SearchInput{Kind: "cask", Current: 1, Size: 100, IncludeDisabled: true}, []string{"old"}, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	suggestions, err := st.Suggest(ctx, "old", "en-US", 10)
	require.NoError(t, err)
	require.Empty(t, suggestions)
	admin, err := st.AdminPackage(ctx, rows["old"].ID)
	require.NoError(t, err)
	var adminState struct{ Disabled bool }
	require.NoError(t, json.Unmarshal(admin, &adminState))
	require.True(t, adminState.Disabled)
	for _, sql := range []string{
		`INSERT INTO categories(slug,icon) VALUES('stale-policy','lucide:box')`,
		`INSERT INTO package_categories(package_id,category_id,is_primary,source) SELECT p.id,c.id,true,'human' FROM packages p CROSS JOIN categories c WHERE p.token IN ('old','recent') AND c.slug='stale-policy'`,
		`INSERT INTO collections(slug,status) VALUES('stale-policy','published')`,
		`INSERT INTO collection_i18n(collection_id,locale,title) SELECT id,'en-US','Policy collection' FROM collections`,
		`INSERT INTO collection_items(collection_id,package_id,sort) SELECT c.id,p.id,0 FROM collections c CROSS JOIN packages p WHERE p.token IN ('old','recent')`,
		`INSERT INTO features(placement,target_type,package_id,status) SELECT 'home_hero','package',id,'published' FROM packages WHERE token='old'`,
		`INSERT INTO feature_i18n(feature_id,locale,title) SELECT id,'en-US','Old app' FROM features`,
	} {
		require.NoError(t, st.DB.Exec(sql).Error)
	}
	collection, err := public.Collection(ctx, "stale-policy", "en-US")
	require.NoError(t, err)
	require.Equal(t, 1, collection.ItemCount)
	require.Len(t, collection.Items, 1)
	require.Equal(t, "recent", collection.Items[0].Package.Token)
	require.Len(t, *collection.PreviewItems, 1)
	brewfile, err := public.Brewfile(ctx, "stale-policy")
	require.NoError(t, err)
	require.Equal(t, "cask \"recent\"\n", brewfile)
	features, err := public.Features(ctx, "en-US")
	require.NoError(t, err)
	require.Empty(t, features)
	categories, err := st.PublicCategories(ctx, "en-US")
	require.NoError(t, err)
	require.Len(t, categories, 1)
	require.Equal(t, 1, categories[0].PackageCount)
	related, err := public.Related(ctx, "cask", "recent", "en-US", 10)
	require.NoError(t, err)
	require.Empty(t, related)
	rankings, err := public.Rankings(ctx, store.PublicFilter{Kind: "cask", Period: "30d", Locale: "en-US", Current: 1, Size: 100, IncludeFonts: true})
	require.NoError(t, err)
	for _, row := range rankings.Records {
		require.False(t, row.Package.Disabled)
	}
	// The desktop receives the disabled flag as a change. "recent" only moved up a rank: that is
	// no change of its own and reaches desktops with the next snapshot.
	changes, err := (&snapshot.Service{Store: st}).Changes(ctx, cursor, 100)
	require.NoError(t, err)
	found := map[string]publicapi.CatalogItem{}
	for _, change := range changes.Changes {
		if change.Item != nil {
			found[change.Token] = *change.Item
		}
	}
	require.True(t, found["old"].Disabled)
	require.False(t, found["font-sample"].Disabled)
	require.NotContains(t, found, "recent")
	recent, err := st.PublicPackage(ctx, "cask", "recent", "en-US")
	require.NoError(t, err)
	require.NotNil(t, recent.Rank30d)
	require.Equal(t, 1, *recent.Rank30d)
	_, cursorAfter, err := st.CatalogBounds(ctx)
	require.NoError(t, err)
	// Unchanged raw data does not remove the local flag or create duplicate changes.
	source.NotModified = false
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	result = stats["kinds"].([]Stats)[0]
	require.Zero(t, result.AutoDisabled+result.Reactivated)
	_, repeatedCursor, err := st.CatalogBounds(ctx)
	require.NoError(t, err)
	require.Equal(t, cursorAfter, repeatedCursor)
	// Editorial/source metadata changes are not version updates.
	source.Data["cask"][0] = `{"token":"old","version":"1,10","name":["Renamed App"],"desc":"New description"}`
	_, err = svc.Run(ctx)
	require.NoError(t, err)
	p, err := st.PublicPackage(ctx, "cask", "old", "en-US")
	require.NoError(t, err)
	require.True(t, p.Disabled)
	// A build-only bump restores the app until its exact commit date is resolved.
	source.Data["cask"][0] = `{"token":"old","version":"1,11"}`
	source.Data["cask"][9] = `{"token":"upstream","version":"2","disabled":true,"disable_reason":"unsafe"}`
	source.Data["cask"][10] = `{"token":"hidden","version":"2"}`
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, stats["kinds"].([]Stats)[0].Reactivated)
	p, err = st.PublicPackage(ctx, "cask", "old", "en-US")
	require.NoError(t, err)
	require.False(t, p.Disabled)
	p, err = st.PublicPackage(ctx, "cask", "upstream", "en-US")
	require.NoError(t, err)
	require.True(t, p.Disabled)
	require.False(t, p.StaleDisabled)
	p, err = st.PublicPackage(ctx, "cask", "hidden", "en-US")
	require.NoError(t, err)
	require.True(t, p.Meta.Hidden)
	// The exact 90-day boundary crosses without any upstream content change.
	now = now.Add(time.Second)
	source.NotModified = true
	stats, err = svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, stats["kinds"].([]Stats)[0].AutoDisabled)
	p, err = st.PublicPackage(ctx, "cask", "boundary", "en-US")
	require.NoError(t, err)
	require.True(t, p.Disabled)
}
