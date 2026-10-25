//go:build integration

package catalog_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestBackgroundDeclarationsKeepSharedOwnersAndWithdrawRemoved(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	for _, token := range []string{"example-one", "example-two"} {
		raw := `{"token":"` + token + `","name":["Example"],"version":"1","artifacts":[{"app":["Example.app"]},{"uninstall":[{"launchctl":"com.example.shared","pkgutil":["com.example.pkg","com.example.*\\.pkg"]}]},{"zap":[{"launchctl":["com.example.shared"],"login_item":"Example Login"}]}]}`
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	now := time.Now()
	svc := &catalog.PublicService{Store: st, Now: func() time.Time { return now }}
	view, err := svc.BackgroundItem(ctx, "com.example.shared", "en-US")
	require.NoError(t, err)
	require.Equal(t, true, view["shared"])
	require.Len(t, view["apps"], 4)
	require.Equal(t, false, view["indexable"])
	require.Equal(t, "declared_candidates", view["ownership"])
	proof := facts.NewProof("https://vendor.example/helper", now, time.Hour, "verified", now)
	require.NoError(t, st.SaveBackgroundContent(ctx, "com.example.shared", facts.BackgroundContent{Locale: "en-US", Description: "Shared vendor update helper. Verify signature, path and installed version locally.", ItemType: "launch_agent", CommonPaths: []string{"~/Library/LaunchAgents/com.example.shared.plist"}, Evidence: proof}))
	view, err = svc.BackgroundItem(ctx, "com.example.shared", "en-US")
	require.NoError(t, err)
	require.Equal(t, true, view["indexable"])
	foreign, err := svc.BackgroundItem(ctx, "com.example.shared", "zh-CN")
	require.NoError(t, err)
	require.Equal(t, false, foreign["indexable"])
	_, err = svc.BackgroundItems(ctx, "", "en-US", 1, 100)
	require.NoError(t, err)

	require.NoError(t, st.DB.Exec(`UPDATE packages SET artifacts='{"entries":[]}' WHERE token='example-one'`).Error)
	view, err = svc.BackgroundItem(ctx, "com.example.shared", "en-US")
	require.NoError(t, err)
	require.Equal(t, false, view["shared"])
	require.Len(t, view["apps"], 2)
	require.NoError(t, st.DB.Exec(`UPDATE packages SET artifacts='{"entries":[]}' WHERE token='example-two'`).Error)
	_, err = svc.BackgroundItem(ctx, "com.example.shared", "en-US")
	require.Error(t, err)
}

func TestBackgroundDeveloperPaginationHiddenOwnersAndReadyLocales(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, fixture := range []struct {
		token, developer string
		labels           []string
		hidden           bool
	}{
		{"alpha", " Alpha ", []string{"z.alpha1", "z.alpha2", "x.partial", "a.shared"}, false},
		{"beta", "Beta", []string{"a.beta", "a.shared"}, false},
		{"zeta", "Zeta", []string{"b.zeta"}, false},
		{"unknown", "   ", []string{"b.unknown"}, false},
		{"hidden", "Hidden vendor", []string{"a.hidden", "x.partial"}, true},
	} {
		raw, err := json.Marshal(map[string]any{"token": fixture.token, "name": []string{fixture.token}, "version": "1", "artifacts": []any{map[string]any{"uninstall": []any{map[string]any{"launchctl": fixture.labels}}}}})
		require.NoError(t, err)
		p, err := homebrew.Normalize("cask", raw)
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
		require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,developer,hidden) SELECT id,?,? FROM packages WHERE token=? ON CONFLICT(package_id) DO UPDATE SET developer=EXCLUDED.developer,hidden=EXCLUDED.hidden", fixture.developer, fixture.hidden, fixture.token).Error)
	}
	svc := &catalog.PublicService{Store: st, Now: func() time.Time { return now }}
	want := []string{"x.partial", "z.alpha1", "z.alpha2", "a.beta", "b.zeta", "a.shared", "a.hidden", "b.unknown"}
	for round := 0; round < 2; round++ {
		got := []string{}
		for page := 1; page <= 4; page++ {
			view, err := svc.BackgroundItems(ctx, "", "en-US", page, 2)
			require.NoError(t, err)
			require.EqualValues(t, len(want), view["total"])
			for _, item := range view["records"].([]any) {
				got = append(got, item.(map[string]any)["label"].(string))
			}
		}
		require.Equal(t, want, got)
	}
	partial, err := svc.BackgroundItem(ctx, "x.partial", "en-US")
	require.NoError(t, err)
	require.Len(t, partial["apps"], 1)
	require.Equal(t, []string{"Alpha"}, partial["developers"])
	shared, err := svc.BackgroundItem(ctx, "a.shared", "en-US")
	require.NoError(t, err)
	require.Equal(t, []string{"Alpha", "Beta"}, shared["developers"])
	hidden, err := svc.BackgroundItem(ctx, "a.hidden", "en-US")
	require.NoError(t, err)
	require.Empty(t, hidden["apps"])
	require.Empty(t, hidden["developers"])
	require.Empty(t, hidden["locales"])
	require.Equal(t, false, hidden["indexable"])
	filtered, err := svc.BackgroundItems(ctx, "a.hidden", "en-US", 1, 24)
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered["total"])
	require.Len(t, filtered["records"], 1)

	// Every language open for indexing (no list), so only content readiness decides below.
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":null}'::jsonb)`).Error)
	for _, locale := range []string{"en-US", "zh-CN", "ja-JP", "es-ES", "pt-BR", "ru-RU", "de-DE", "fr-FR", "it-IT"} {
		proof := facts.NewProof("https://vendor.example/helper", now, time.Hour, "verified", now)
		description := "Verified background helper description."
		switch locale {
		case "fr-FR":
			proof = facts.NewProof(proof.SourceURL, now.Add(-2*time.Hour), time.Hour, "verified", now)
		case "de-DE":
			proof.Status = "needs_review"
		case "ja-JP":
			description = "   "
		}
		require.NoError(t, st.SaveBackgroundContent(ctx, "a.shared", facts.BackgroundContent{Locale: locale, Description: description, ItemType: "launch_agent", CommonPaths: []string{}, Evidence: proof}))
	}
	shared, err = svc.BackgroundItem(ctx, "a.shared", "zh-CN")
	require.NoError(t, err)
	require.Equal(t, []string{"en-US", "es-ES", "it-IT", "pt-BR", "ru-RU", "zh-CN", "zh-Hant"}, shared["locales"])
	require.Equal(t, true, shared["indexable"])
	missing, err := svc.BackgroundItem(ctx, "a.shared", "ja-JP")
	require.NoError(t, err)
	require.Equal(t, false, missing["indexable"])
	// Valid text alone does not make a hidden-only label eligible for hreflang.
	require.NoError(t, st.SaveBackgroundContent(ctx, "a.hidden", facts.BackgroundContent{Locale: "en-US", Description: "Verified hidden owner description.", ItemType: "launch_agent", CommonPaths: []string{}, Evidence: facts.NewProof("https://vendor.example/helper", now, time.Hour, "verified", now)}))
	hidden, err = svc.BackgroundItem(ctx, "a.hidden", "en-US")
	require.NoError(t, err)
	require.Empty(t, hidden["locales"])
	require.Equal(t, false, hidden["indexable"])
	now = now.Add(2 * time.Hour)
	shared, err = svc.BackgroundItem(ctx, "a.shared", "zh-CN")
	require.NoError(t, err)
	require.Empty(t, shared["locales"])
	require.Equal(t, false, shared["indexable"])
}

func TestBackgroundItemsStayOutOfTheIndexInClosedLocales(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	raw := `{"token":"helper-owner","name":["Helper Owner"],"version":"1","artifacts":[{"uninstall":[{"launchctl":"com.example.closed"}]}]}`
	p, err := homebrew.Normalize("cask", json.RawMessage(raw))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	for _, locale := range []string{"en-US", "zh-CN", "pt-BR", "de-DE"} {
		proof := facts.NewProof("https://vendor.example/helper", now, time.Hour, "verified", now)
		require.NoError(t, st.SaveBackgroundContent(ctx, "com.example.closed", facts.BackgroundContent{Locale: locale, Description: "Verified background helper description.", ItemType: "launch_agent", CommonPaths: []string{}, Evidence: proof}))
	}
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',coalesce(settings->'pages','{}')||'{"openLocales":["en-US","zh-CN","ja-JP","ru-RU","es-ES"]}'::jsonb)`).Error)
	svc := &catalog.PublicService{Store: st, Now: func() time.Time { return now }}
	for locale, indexable := range map[string]bool{"en-US": true, "zh-CN": true, "pt-BR": false, "de-DE": false} {
		view, err := svc.BackgroundItem(ctx, "com.example.closed", locale)
		require.NoError(t, err)
		require.Equal(t, indexable, view["indexable"], locale)
		// The description still shows in a closed language; only indexing and hreflang leave it out.
		require.Equal(t, locale, view["content"].(*facts.BackgroundContent).Locale, locale)
		require.Equal(t, []string{"en-US", "zh-CN"}, view["locales"], locale)
		list, err := svc.BackgroundItems(ctx, "", locale, 1, 24)
		require.NoError(t, err)
		require.Equal(t, indexable, list["records"].([]any)[0].(map[string]any)["indexable"], locale)
	}
}
